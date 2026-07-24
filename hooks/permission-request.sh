#!/bin/sh
# Claude Code PermissionRequest hook: a permission prompt is up — the session is genuinely
# waiting on a human decision. Mark it `blocked` so the cockpit shows which session needs you.
#
# This deliberately uses PermissionRequest, NOT Notification: Notification also fires on idle
# input-waiting (a session that just finished a turn), which is `idle`, not `blocked`. Using the
# permission event avoids that false positive. The state clears on the next UserPromptSubmit
# (-> busy), PostToolUse (-> busy), or keepalive (-> idle).

SID=$(python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null)
[ -n "$SID" ] || exit 0
if command -v presence >/dev/null 2>&1; then
  plexus heartbeat --session-id "$SID" --state blocked >/dev/null 2>&1 || true
fi
exit 0
