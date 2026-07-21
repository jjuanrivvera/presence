# presence

Session registry for an ambient agent mesh. Every coding-agent session — Claude Code or Codex,
on any machine — publishes its live state (repo, branch, inject port, agent, freshness) to a tiny
HTTP service backed by SQLite. A router (or you) queries it to decide whether to run an event
locally or delegate it into a session that already has related work in progress.

It is a read-mostly blackboard (choreography, not orchestration): sessions write their own row, a
reader queries and decides.

## Architecture

```
   machine A ──register/heartbeat (HTTP, over a private network)──┐
   machine B ──register/heartbeat (HTTP, over a private network)──┤
                                                                   ▼
                     server:  presence serve  →  SQLite (~/.local/state/presence/presence.db)
                                                                   ▲
                     router  ── presence get/list ─────────────────┘
```

One static Go binary (pure-Go SQLite via `modernc.org/sqlite`, `CGO_ENABLED=0`):

- `presence serve` — the service. Binds an explicit private address only, never `0.0.0.0`
  (a private overlay such as Tailscale/WireGuard is the intended perimeter). Stamps all
  timestamps server-side (no clock skew) and auto-prunes rows older than the TTL (default `300s`).
- `presence register/heartbeat/deregister/list/get/prune/version` — the client, run from any
  machine, typically via agent session hooks.

## Agents

Each row carries an `agent` field (`claude` by default, `codex`, or any future agent). It lets a
router route and dedup **per agent** — "is there already a Codex session on this repo?" — without
overloading the session id.

- **Claude Code** sessions register via the hooks in `hooks/` (see below).
- **Codex** sessions register via the [`edc`](https://github.com/jjuanrivvera/edc) `.codex-plugin`
  hooks (`agent=codex`). Interactive Codex sessions register with `inject_port=0` (visible but not
  an injection target); the `edc codex serve` daemon registers with a real inject port.

Register with `--agent codex` (or `$PRESENCE_AGENT`); filter with `list --agent` / `get --agent`.
An empty agent defaults to `claude` server-side, so pre-agent clients keep working unchanged.

## API

All routes except `/healthz` require `Authorization: Bearer $PRESENCE_TOKEN` (constant-time
compare). Bodies are JSON, capped at 16 KiB. Errors are `{"ok":false,"error":"..."}` with
400/401/404/405/500.

| Method · route | Body / query | Effect |
|---|---|---|
| `POST /register` | `{session_id, host, repo, repo_path, branch, inject_port, pid, agent?}` | Upsert; `started_at` set on first insert, `last_seen` always; state forced `busy`; `agent` defaults `claude` |
| `POST /heartbeat` | `{session_id, state?}` | Bump `last_seen`; `state` defaults `busy`; **404** if unknown |
| `POST /deregister` | `{session_id}` | Delete row (idempotent) |
| `GET /list?host=&repo=&agent=&fresh=` | — | Live rows; exact filters; `fresh` Go duration, default `120s` |
| `GET /get?repo=&host=&agent=&fresh=` | — | Delegation query: freshest row matching `repo` (required) + `host` (optional CSV = OR) + `agent` (optional) with `inject_port>0`; `200 {row}` or `204` |
| `POST /prune` | `{older_than?}` | Delete rows older than `older_than` (default: server TTL); returns `{ok, pruned}` |
| `GET /healthz` | — | Liveness, no auth, `text/plain "ok"` |
| `GET /ui` | — | Live dashboard (static HTML, no auth — its JS calls `/list` with the token, asked once and kept in localStorage) |

Ties in `/get` break by `last_seen` DESC then `session_id` ASC (deterministic).

```bash
curl -s -H "Authorization: Bearer $PRESENCE_TOKEN" -X POST $PRESENCE_URL/register \
  -d '{"session_id":"abc-123","host":"laptop","repo":"myrepo","repo_path":"/path/to/myrepo","branch":"main","inject_port":8801,"pid":4242,"agent":"codex"}'
# → {"ok":true}

curl -s -H "Authorization: Bearer $PRESENCE_TOKEN" "$PRESENCE_URL/get?repo=myrepo&agent=codex"
# → 200 {"session_id":"abc-123",...,"agent":"codex","inject_port":8801,...}   (injectable match)
# → 204                                                                       (none)
```

## CLI

```
presence serve      [--bind ADDR] [--db PATH] [--ttl 300s]
presence register   [--session-id ID] [--inject-port N] [--host LABEL] [--agent claude|codex]
presence heartbeat  [--session-id ID] [--state busy|idle]
presence deregister [--session-id ID]
presence list       [--host H] [--repo R] [--agent A] [--fresh 2m] [-o json|table]
presence get        --repo R [--host a,b] [--agent A] [-o json]   # exit 0 = match, 1 = none, 2 = error
presence prune      [--older-than 10m]
presence version
```

- `register` auto-detects: session id (`$CLAUDE_SESSION_ID` or `--session-id`), host label
  (`$PRESENCE_HOST`, fallback = hostname lowercased and truncated at the first dot), repo/branch
  from the launch cwd via git (empty outside a repo), inject port (`--inject-port` or
  `$EDC_INJECT_PORT`, 0 = not injectable), agent (`--agent` or `$PRESENCE_AGENT`, else `claude`).
  Repo info is captured **once at register** — a session belongs to the dir it opened in;
  `cd`/`checkout` mid-session are not tracked by design.
- A `heartbeat` that gets a **404** (server pruned the row, e.g. the machine slept past the TTL)
  re-registers automatically and retries once.
- Client HTTP timeout is 2s so hooks can never hang a session.
- Exit codes: `0` success, `1` no match (`get` only), `2` network/auth/server error.

## Config

Precedence: **flag > env var > `~/.config/presence/env`**. The env-file is read by the binary
itself (`KEY=VALUE` lines, `#` comments) — hooks don't depend on your shell sourcing anything.

```sh
# ~/.config/presence/env (all machines)
PRESENCE_URL=http://<server-address>:8799
PRESENCE_TOKEN=<shared mesh secret>
PRESENCE_HOST=<this-machine-label>
PRESENCE_AGENT=claude          # or codex, per the agent running on this machine
# server only:
PRESENCE_BIND=<private-address>:8799   # an explicit private address, never 0.0.0.0
PRESENCE_TTL=300s
```

The token is a shared secret of the mesh (the private overlay is the perimeter); it lives on each
machine, never in the store. `serve` fails closed if the token is unset.

## Claude Code hooks

Copy `hooks/*.sh` somewhere stable (e.g. `~/.claude/hooks/presence/`), make them executable, and
add to `settings.json`:

```json
{
  "hooks": {
    "SessionStart": [ { "hooks": [ { "type": "command", "command": "~/.claude/hooks/presence/session-start.sh" } ] } ],
    "PostToolUse":  [ { "hooks": [ { "type": "command", "command": "~/.claude/hooks/presence/post-tool-use.sh" } ] } ],
    "SessionEnd":   [ { "hooks": [ { "type": "command", "command": "~/.claude/hooks/presence/session-end.sh" } ] } ]
  }
}
```

- `session-start.sh` → `presence register` + persists `session → pid, cwd, port` under
  `~/.local/state/presence/sessions/` (consumed by keepalive).
- `post-tool-use.sh` → `presence heartbeat`, throttled to ≤1/min via `~/.local/state/presence/last-hb`.
- `session-end.sh` → `presence deregister` + cleans the mapping.

All three are silent and fail-soft: if the server is unreachable they no-op within 2 seconds and
never break the session. Codex sessions are wired equivalently through `edc`'s `.codex-plugin`
(SessionStart/PreToolUse/Stop → register/heartbeat with `--agent codex`).

## Keepalive (idle long-lived sessions)

`PostToolUse` heartbeats only fire while a session is using tools; a long-lived session that waits
idle for events would age out of the registry at the TTL. `hooks/keepalive.sh` — installed as a
periodic timer (e.g. a systemd user timer, every 60s) — sends `--state idle` for each mapped
session whose process is still alive, and deregisters the dead ones (covering sessions that die
without a clean end hook). Because heartbeat re-registers on 404, a pruned-but-alive session
recovers on its next tick.

## Install

From a GitHub release (checksum-verified, no Go needed):

```sh
curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/presence/main/install.sh | sh
```

From source: `go build -o presence .` (Go 1.25+, `CGO_ENABLED=0`).

## Deploy (server)

systemd user unit at `~/.config/systemd/user/presence.service`:

```ini
[Unit]
Description=presence — ambient mesh session registry
After=network-online.target

[Service]
EnvironmentFile=%h/.config/presence/env
ExecStart=%h/.local/bin/presence serve
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
```

DB lives at `~/.local/state/presence/presence.db` (WAL + busy_timeout). The `agent` column is
added by an idempotent migration on open, so upgrading an existing DB is safe (existing rows
backfill to `claude`).
