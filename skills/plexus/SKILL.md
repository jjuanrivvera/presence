---
name: plexus
description: >-
  Operate the presence "plexus" — a cross-machine registry, launcher, and web
  cockpit for coding-agent (Claude Code / Codex) sessions. Use to see which
  agents are running and their state, launch a session in a directory, reattach
  to or kill a session, hand work to another session, or point a human at the
  live cockpit. Triggers: "plexus ls", "what sessions are running", "which agents
  are busy/idle/blocked", "launch claude/codex in <dir>", "attach to <session>",
  "kill the plexus session", "the cockpit", "presence list/get".
---

# plexus

`plexus` (a symlink to the `presence` binary) is one tool for a fleet of
coding-agent sessions spread across machines. It does three things:

1. **Registry** — every session self-registers (host, agent, repo, state,
   inject port, attach address). Query it from anywhere.
2. **Launcher** — start an agent inside a named tmux session so it survives the
   terminal closing and is reachable from any machine on the private network.
3. **Cockpit** — a web/PWA dashboard (`$PRESENCE_URL/ui`) that lists the fleet
   and embeds each session's live terminal (view + type + interrupt).

## Setup

The client reads config from flags → env vars → `~/.config/presence/env`:

```
PRESENCE_URL=http://<registry-host>:<port>   # the presence server
PRESENCE_TOKEN=<shared-secret>               # bearer + web login + terminal auth
PRESENCE_HOST=<label>                        # this machine's label (e.g. laptop)
```

Check it works: `plexus ls`. If it errors on `PRESENCE_URL`/`PRESENCE_TOKEN`,
those aren't set.

## Everyday commands

| Command | What it does |
| --- | --- |
| `plexus ls` | List live sessions (host · agent · repo · state · attach). Add `-o json` to script it, `--agent codex` / `--host <h>` to filter. |
| `plexus watch` | Full-screen live TUI of the fleet (blocked-first, auto-refresh). |
| `plexus claude [dir]` | Launch Claude Code inside a tmux session named after the repo, and drop you into it. Attachable everywhere. |
| `plexus codex [dir]` | Same, for Codex. |
| `plexus attach <name>` | Reattach to a running session (also `tmux -L plexus attach -t <name>`). |
| `plexus kill <name>` | End a session — kills the agent and its terminal. |

Launcher details:

- **`dir` defaults to the current directory.** The session name is the git repo
  basename (or the dir basename).
- **`--detach`** creates the session without attaching — for background /
  headless agents: `plexus claude --detach <dir> -- <agent-args>`.
- **`--`** passes the rest through to the agent, e.g. skip an interactive
  first-run trust prompt in a fresh directory:
  `plexus claude --detach <dir> -- --dangerously-skip-permissions`.
- **Re-running `plexus claude <same-dir>` reattaches** instead of starting a
  second agent.

## Sessions persist

Agents launched with `plexus` run in a detached-capable tmux server (its own
socket), so **closing the terminal only detaches you — the session keeps
running.** Reattach with `plexus attach <name>`, from another machine, or via the
cockpit. Only exiting the agent from inside (or `plexus kill`) ends it.

## The cockpit

Open `$PRESENCE_URL/ui` in a browser (installable as a PWA). Log in once with
`PRESENCE_TOKEN`. Click a session in the sidebar to see its live terminal on the
right — you can type turns and send an interrupt. States are color-coded:
`busy` (working), `idle`, `blocked` (waiting on a human — surfaced first).

## Scripting the registry

- `plexus ls -o json` — the full fleet as JSON.
- `presence get --repo <R> [--host a,b] [--agent codex] -o json` — the freshest
  *injectable* session for a repo (exit 1 + empty when none). Use this to find
  where a repo is already open before starting a new session, or to discover a
  session's inject port for handing it an event.
- `register` / `heartbeat` / `deregister` exist but are normally driven by the
  session-start/end hooks, not called by hand.

## When to use

- **"What's running / who's stuck?"** → `plexus ls` (or `plexus watch`, or the cockpit).
- **Start or resume work in a repo** → `plexus claude <dir>` (reattaches if it's
  already open there).
- **Hand off / delegate** → `presence get --repo <R>` to find the target
  session, then reach it via its inject port.
- **Clean up** → `plexus kill <name>`.

## Notes

- A brand-new directory triggers the agent's first-run trust prompt; a `--detach`
  launch would hang there — pass the agent's skip-permissions flag or pre-trust
  the directory once interactively.
- The registry, terminals, and cockpit are meant to live on a private network
  (e.g. a VPN/tailnet); the token is the only credential, so keep it secret.
