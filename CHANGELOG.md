# Changelog

## v0.4.0

**Plexus.** The project is now named **Plexus** — `presence` (this binary) plus
[`edc`](https://github.com/jjuanrivvera/edc), the two independent tools that let you see, launch,
attach, and inject coding-agent sessions across your machines. The launcher command, the tmux
socket, and the web-terminal auth all move from `mesh` to `plexus`, and the `mesh` symlink is
replaced by `plexus`.

- **`--worktree` / `-w` on the launcher**: `plexus <agent> <repo> --worktree` runs the session in a
  fresh `git worktree` (branch `plexus/<agent>-<suffix>` under `~/.local/state/plexus/worktrees/`),
  so two agents in the same repo never collide on the working tree or the index.
- **One Claude Code plugin for both tools** (`plexus@jjuanrivvera-plexus`): registration + web
  terminal via hooks (`presence`) and injection via the channel (`edc`), plus the `plexus` skill —
  installed and enabled together.
- **`plexus.sh`**: one command installs both binaries, drops the `plexus` symlink, and scaffolds
  config (generating a registry token and an inject secret) idempotently.
- **LICENSE**: MIT.

Earlier `v0.3.x` tags shipped without changelog notes; they brought the single-sign-in
master-detail cockpit, the ttyd web terminal folded into the binary as `presence ttyd`, the
`plexus kill` command, and OpenCode launch/attach support.

## v0.2.0

- New session state **`blocked`** (alongside `busy`/`idle`): the session is waiting on human
  input (a permission prompt or a question). It is the highest-signal state — it tells the mesh
  which session needs you right now. `heartbeat --state blocked` and the `/heartbeat` API accept it.
- Hooks: `notification.sh` (Claude Notification → `blocked`) and `user-prompt-submit.sh`
  (UserPromptSubmit → `busy`) to drive the state automatically.

## v0.1.0

First release. Session registry for an ambient agent mesh (Claude Code + Codex).

- HTTP service (`presence serve`) + client CLI (`register`/`heartbeat`/`deregister`/`list`/`get`/`prune`) —
  one static Go binary, pure-Go SQLite (`CGO_ENABLED=0`).
- Per-agent tracking via the `agent` field (`claude`/`codex`) with `--agent` filters on `list`/`get`;
  an idempotent migration adds the column to existing DBs (backfilling to `claude`).
- Server-side timestamps, TTL auto-prune, constant-time bearer auth, private-address bind only.
- Claude Code session hooks; Codex sessions register through edc's `.codex-plugin`.
