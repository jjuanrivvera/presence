# Command reference

## `plexus` — launch & attach

`plexus` is the binary; these are the ergonomic verbs.

| Command | Does |
|---|---|
| `plexus claude [dir]` | launch Claude Code in a tmux session on the `plexus` socket, and attach |
| `plexus codex [dir]` | same, for Codex |
| `plexus opencode [dir]` | same, for OpenCode (decoupled stack: attachable **and** injectable) |
| `plexus attach <name>` | reattach to a running plexus session |
| `plexus kill <name>` | end a session (kills the agent + its web terminal) |
| `plexus ls` | list live sessions (alias of `plexus list`) |
| `plexus watch` | live full-screen cockpit in the terminal |

**Launcher flags** (`plexus <agent> …`):

| Flag | Effect |
|---|---|
| `[dir]` | working directory (default: cwd); the session name is the repo basename |
| `--detach` | create the session without attaching — background / headless agents |
| `-- <args…>` | pass the rest to the agent, e.g. `-- --dangerously-skip-permissions` |

`dir` defaults to the current directory; re-running for a dir that's already open **reattaches** instead
of duplicating.

## `plexus` — registry & server

```
plexus serve      [--bind ADDR] [--db PATH] [--ttl 300s]
plexus register   [--session-id ID] [--agent claude|codex|opencode] [--inject-port N]
                    [--attach-addr HOST:PORT] [--host LABEL]
plexus heartbeat  [--session-id ID] [--state busy|idle|blocked]
plexus deregister [--session-id ID]
plexus list       [--host H] [--repo R] [--agent A] [--fresh 2m] [-o json|table]
plexus get        --repo R [--host laptop,server] [--agent A] [--fresh 2m] [-o json]
plexus watch      [-n 2]
plexus prune      [--older-than 10m]
plexus launch     <claude|codex|opencode> [dir] [--detach] [-- args…]
plexus attach     <name>
plexus kill       <name>
plexus ttyd       spawn <sid> <tmux-session> [socket] | kill <sid> | reap
plexus version
```

`plexus get` is the routing query: the freshest **injectable** session for a repo. Exit `1` + empty
output when there's no match, so it scripts cleanly.

**Server config** (`plexus serve`): `PLEXUS_BIND`, `PLEXUS_TTL`, `PLEXUS_TOKEN`. **Client config:**
`PLEXUS_URL`, `PLEXUS_TOKEN`, `PLEXUS_HOST`. Precedence: flag → env → `~/.config/plexus/env`.

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
npx skills add jjuanrivvera/plexus          # into this project
npx skills add jjuanrivvera/plexus --global # for all projects
```
