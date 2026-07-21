# Changelog

## v0.1.0

First release. Session registry for an ambient agent mesh (Claude Code + Codex).

- HTTP service (`presence serve`) + client CLI (`register`/`heartbeat`/`deregister`/`list`/`get`/`prune`) —
  one static Go binary, pure-Go SQLite (`CGO_ENABLED=0`).
- Per-agent tracking via the `agent` field (`claude`/`codex`) with `--agent` filters on `list`/`get`;
  an idempotent migration adds the column to existing DBs (backfilling to `claude`).
- Server-side timestamps, TTL auto-prune, constant-time bearer auth, private-address bind only.
- Claude Code session hooks; Codex sessions register through edc's `.codex-plugin`.
