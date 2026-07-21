#!/bin/sh
# Claude Code SessionStart hook: register this session in presence.
# Silent and fail-soft: never block or break the session.
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
# Fallback: sesiones sin edc pero con el inject del tgctl-claude-channel
# anuncian ese puerto — mismo contrato POST /inject {source,event,text} + Bearer.
[ -n "$EDCP" ] || EDCP="${TGCTL_CHANNEL_INJECT_PORT:-}"
[ -n "$EDCP" ] && export EDC_INJECT_PORT="$EDCP"

# Web-terminal attach: if this session runs inside tmux, spawn a per-session ttyd and
# advertise its address so the cockpit can attach (view live + type + ESC). Fail-soft.
MTTYD="$HOME/.local/bin/mesh-ttyd"; [ -x "$MTTYD" ] || MTTYD="$(command -v mesh-ttyd 2>/dev/null)"
if [ -n "${TMUX:-}" ] && [ -n "$MTTYD" ] && [ -n "${CLAUDE_SESSION_ID:-}" ] && command -v tmux >/dev/null 2>&1; then
  TSESS=$(tmux display-message -p '#S' 2>/dev/null)
  if [ -n "$TSESS" ]; then
    ADDR=$("$MTTYD" spawn "$CLAUDE_SESSION_ID" "$TSESS" 2>/dev/null)
    [ -n "$ADDR" ] && export PRESENCE_ATTACH_ADDR="$ADDR"
  fi
fi

BIN="$HOME/.local/bin/presence"
[ -x "$BIN" ] || BIN="$(command -v presence 2>/dev/null)"
[ -n "$BIN" ] || exit 0
"$BIN" register >/dev/null 2>&1 || true

# Persistir sesion -> (pid de claude, cwd) para que el timer de keepalive pueda
# latir por sesiones idle (sin tool-calls no hay PostToolUse y el TTL las poda).
if [ -n "${CLAUDE_SESSION_ID:-}" ]; then
  CPID=""; P=$$
  i=0; while [ "$P" -gt 1 ] && [ $i -lt 6 ]; do
    case "$(ps -o comm= -p "$P" 2>/dev/null)" in *claude*|*node*) CPID="$P"; break;; esac
    P=$(ps -o ppid= -p "$P" 2>/dev/null | tr -d ' '); [ -n "$P" ] || break; i=$((i+1))
  done
  [ -n "$CPID" ] || CPID=$PPID
  SDIR="$HOME/.local/state/presence/sessions"
  mkdir -p "$SDIR" 2>/dev/null && printf '%s\n%s\n%s\n' "$CPID" "$(pwd)" "${EDC_INJECT_PORT:-}" > "$SDIR/$CLAUDE_SESSION_ID" 2>/dev/null
fi
exit 0
