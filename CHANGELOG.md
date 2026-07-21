# Changelog

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
