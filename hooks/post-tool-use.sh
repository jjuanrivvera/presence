#!/bin/sh
# Claude Code PostToolUse hook: heartbeat, throttled to <=1/min so we don't
# POST on every tool call. Silent and fail-soft.
# Claude Code manda session_id en el JSON por stdin (no como env var).
SID=$(python3 -c "import sys,json;print(json.load(sys.stdin).get('session_id',''))" 2>/dev/null)
[ -n "$SID" ] && export CLAUDE_SESSION_ID="$SID"
# Descubrir el puerto de edc de ESTA sesión: el state file puede ser pid-<pid>.json
# (Claude Code no exporta CLAUDE_SESSION_ID al plugin MCP), así que matcheamos por
# parentesco: el edc dueño del state file debe colgar del mismo proceso claude que este hook.
discover_edc_port() {
  [ -d "$HOME/.local/state/edc" ] || return 0
  # ancestros de este hook (hasta 6 niveles)
  ANC=""; P=$$
  i=0; while [ "$P" -gt 1 ] && [ $i -lt 6 ]; do ANC="$ANC $P"; P=$(ps -o ppid= -p "$P" 2>/dev/null | tr -d ' '); [ -n "$P" ] || break; i=$((i+1)); done
  for f in "$HOME/.local/state/edc/"*.json; do
    [ -f "$f" ] || continue
    EPID=$(sed -n 's/.*"pid":\([0-9]*\).*/\1/p' "$f")
    EPORT=$(sed -n 's/.*"port":\([0-9]*\).*/\1/p' "$f")
    [ -n "$EPID" ] && kill -0 "$EPID" 2>/dev/null || continue
    EPPID=$(ps -o ppid= -p "$EPID" 2>/dev/null | tr -d ' ')
    case " $ANC " in *" $EPPID "*) echo "$EPORT"; return 0;; esac
  done
}

EDCP=$(discover_edc_port)
# Fallback: sesiones sin edc pero con el inject del tgctl-claude-channel.
[ -n "$EDCP" ] || EDCP="${TGCTL_CHANNEL_INJECT_PORT:-}"
[ -n "$EDCP" ] && export EDC_INJECT_PORT="$EDCP"

BIN="$HOME/.local/bin/presence"
[ -x "$BIN" ] || BIN="$(command -v presence 2>/dev/null)"
[ -n "$BIN" ] || exit 0

STATE_DIR="$HOME/.local/state/presence"
mkdir -p "$STATE_DIR" 2>/dev/null || exit 0
LAST_FILE="$STATE_DIR/last-hb-${CLAUDE_SESSION_ID:-default}"

now=$(date +%s)
last=$(cat "$LAST_FILE" 2>/dev/null || echo 0)
case "$last" in ''|*[!0-9]*) last=0 ;; esac
[ $((now - last)) -lt 60 ] && exit 0

# El re-register tras un 404 del server toma el puerto de EDC_INJECT_PORT (env);
# lo exportamos desde el state file por sesión de edc si su pid sigue vivo.
if [ -z "${EDC_INJECT_PORT:-}" ] && [ -n "${CLAUDE_SESSION_ID:-}" ]; then
  STATE="$HOME/.local/state/edc/${CLAUDE_SESSION_ID}.json"
  if [ -f "$STATE" ]; then
    SET=$(python3 -c "import json;d=json.load(open('$STATE'));print(str(d.get('port',''))+' '+str(d.get('pid','')))" 2>/dev/null)
    P=${SET%% *}; EPID=${SET##* }
    if [ -n "$P" ] && [ -n "$EPID" ] && kill -0 "$EPID" 2>/dev/null; then
      export EDC_INJECT_PORT="$P"
    fi
  fi
fi

echo "$now" > "$LAST_FILE" 2>/dev/null
"$BIN" heartbeat >/dev/null 2>&1 || true
exit 0
