#!/bin/sh
# Claude Code SessionEnd hook: remove this session from presence.
# Silent and fail-soft: never block or break session shutdown.
# Claude Code manda session_id en el JSON por stdin (no como env var).
SID=$(python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null)
[ -n "$SID" ] && export CLAUDE_SESSION_ID="$SID"
BIN="$HOME/.local/bin/presence"
[ -x "$BIN" ] || BIN="$(command -v presence 2>/dev/null)"
[ -n "$BIN" ] || exit 0
"$BIN" deregister >/dev/null 2>&1 || true
[ -n "${CLAUDE_SESSION_ID:-}" ] && rm -f "$HOME/.local/state/presence/sessions/$CLAUDE_SESSION_ID" 2>/dev/null
exit 0
