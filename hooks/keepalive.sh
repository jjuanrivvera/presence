#!/bin/sh
# Keepalive de presence: late por cada sesion registrada localmente cuyo proceso
# claude siga vivo. Pensado para daemons companion que pasan horas sin tool-calls
# (sin PostToolUse no hay heartbeat y el TTL del server los poda).
# Corre via systemd timer / cron cada ~2 min. Silencioso y fail-soft.
BIN="$HOME/.local/bin/presence"
[ -x "$BIN" ] || BIN="$(command -v presence 2>/dev/null)"
[ -n "$BIN" ] || exit 0

SDIR="$HOME/.local/state/presence/sessions"
[ -d "$SDIR" ] || exit 0

for f in "$SDIR"/*; do
  [ -f "$f" ] || continue
  SID=$(basename "$f")
  PID=$(sed -n 1p "$f")
  CWD=$(sed -n 2p "$f")
  case "$PID" in ''|*[!0-9]*) rm -f "$f"; continue ;; esac
  if ! kill -0 "$PID" 2>/dev/null; then
    # proceso muerto: limpiar mapping y dar de baja (por si SessionEnd nunca corrio)
    "$BIN" deregister --session-id "$SID" >/dev/null 2>&1 || true
    rm -f "$f"
    continue
  fi
  # El heartbeat re-registra si el server ya podo la sesion; correr desde el cwd
  # original para que ese re-register conserve repo/branch correctos.
  # Puerto de inject: linea 3 del mapping (edc o tgctl-channel), con el state
  # file de edc como fallback para mappings viejos de 2 lineas.
  EDCP=$(sed -n 3p "$f")
  if [ -z "$EDCP" ]; then
    ESTATE="$HOME/.local/state/edc/$SID.json"
    [ -f "$ESTATE" ] && EDCP=$(sed -n 's/.*"port":\([0-9]*\).*/\1/p' "$ESTATE")
  fi
  (
    [ -d "$CWD" ] && cd "$CWD" 2>/dev/null
    [ -n "$EDCP" ] && export EDC_INJECT_PORT="$EDCP"
    "$BIN" heartbeat --session-id "$SID" --state idle >/dev/null 2>&1 || true
  )
done
exit 0
