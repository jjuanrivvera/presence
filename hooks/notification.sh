#!/bin/sh
# Claude Code Notification hook: the session is waiting on you — a permission prompt or an
# idle input wait. Mark it `blocked` in presence so the mesh cockpit (the tmux status bar)
# shows which session needs you, across every machine. Silent and fail-soft.

SID=$(python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null)
[ -n "$SID" ] || exit 0
if command -v presence >/dev/null 2>&1; then
  presence heartbeat --session-id "$SID" --state blocked >/dev/null 2>&1 || true
fi
exit 0
