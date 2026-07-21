#!/bin/sh
# Claude Code UserPromptSubmit hook: you answered, so the session is working again — clear the
# `blocked` state immediately (don't wait for the next tool call). Silent and fail-soft.

SID=$(python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null)
[ -n "$SID" ] || exit 0
if command -v presence >/dev/null 2>&1; then
  presence heartbeat --session-id "$SID" --state busy >/dev/null 2>&1 || true
fi
exit 0
