# Command reference

## `plexus` — launch & attach

`plexus` is a symlink to `presence`; these are the ergonomic verbs.

| Command | Does |
|---|---|
| `plexus claude [dir]` | launch Claude Code in a tmux session on the `plexus` socket, and attach |
| `plexus codex [dir]` | same, for Codex |
| `plexus opencode [dir]` | same, for OpenCode (decoupled stack: attachable **and** injectable) |
| `plexus attach <name>` | reattach to a running plexus session |
| `plexus kill <name>` | end a session (kills the agent + its web terminal) |
| `plexus ls` | list live sessions (alias of `presence list`) |
| `plexus watch` | live full-screen cockpit in the terminal |

**Launcher flags** (`plexus <agent> …`):

| Flag | Effect |
|---|---|
| `[dir]` | working directory (default: cwd); the session name is the repo basename |
| `--detach` | create the session without attaching — background / headless agents |
| `-- <args…>` | pass the rest to the agent, e.g. `-- --dangerously-skip-permissions` |

`dir` defaults to the current directory; re-running for a dir that's already open **reattaches** instead
of duplicating.

## `presence` — registry & server

```
presence serve      [--bind ADDR] [--db PATH] [--ttl 300s]
presence register   [--session-id ID] [--agent claude|codex|opencode] [--inject-port N]
                    [--attach-addr HOST:PORT] [--host LABEL]
presence heartbeat  [--session-id ID] [--state busy|idle|blocked]
presence deregister [--session-id ID]
presence list       [--host H] [--repo R] [--agent A] [--fresh 2m] [-o json|table]
presence get        --repo R [--host laptop,server] [--agent A] [--fresh 2m] [-o json]
presence watch      [-n 2]
presence prune      [--older-than 10m]
presence launch     <claude|codex|opencode> [dir] [--detach] [-- args…]
presence attach     <name>
presence kill       <name>
presence ttyd       spawn <sid> <tmux-session> [socket] | kill <sid> | reap
presence version
```

`presence get` is the routing query: the freshest **injectable** session for a repo. Exit `1` + empty
output when there's no match, so it scripts cleanly.

**Server config** (`presence serve`): `PRESENCE_BIND`, `PRESENCE_TTL`, `PRESENCE_TOKEN`. **Client config:**
`PRESENCE_URL`, `PRESENCE_TOKEN`, `PRESENCE_HOST`. Precedence: flag → env → `~/.config/presence/env`.

## `edc` — event injection

```
edc                    # default: the Claude Code channel MCP stdio server
edc codex serve        # front a codex app-server thread, serve /inject
edc opencode serve     # front an opencode server, serve /inject
```

**Config** — env or `~/.config/edc/config.json`:

| Var | For |
|---|---|
| `EDC_INJECT_SECRET` | required; the `/inject` bearer secret (fails closed without it) |
| `EDC_INJECT_PORT` | `/inject` port (`auto` = kernel-assigned) |
| `EDC_CODEX_CWD` / `EDC_CODEX_MODEL` | Codex adapter: working dir / model override |
| `EDC_OPENCODE_URL` | OpenCode: connect to an existing server instead of spawning one |
| `EDC_OPENCODE_TUI` | OpenCode: inject into the attached TUI (`/tui/*`) — visible |
| `EDC_OPENCODE_CWD` / `EDC_OPENCODE_MODEL` / `EDC_OPENCODE_AGENT` | OpenCode overrides |

**Inject an event:**

```sh
curl -X POST "http://127.0.0.1:$PORT/inject" \
  -H "Authorization: Bearer $EDC_INJECT_SECRET" \
  -d '{"source":"cron","event":"nightly","text":"run the report"}'
```

## The `plexus` skill

Install the agent skill that teaches an agent to drive Plexus:

```sh
npx skills add jjuanrivvera/presence          # into this project
npx skills add jjuanrivvera/presence --global # for all projects
```
