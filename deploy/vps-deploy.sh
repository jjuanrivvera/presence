#!/bin/bash
# Deploy plexus to a server over SSH. Run from a machine that has the release binary built
# at dist/plexus-linux-amd64 and a ~/.config/plexus/env with PLEXUS_TOKEN.
#
#   DEPLOY_HOST=user@server \
#   PLEXUS_SERVER=<private-address>:8799 \
#   bash deploy/vps-deploy.sh
#
# The same PLEXUS_TOKEN ends up on both machines. PLEXUS_SERVER is the server's private
# (e.g. Tailscale/WireGuard) address, used for both the bind and the client URL.
set -euo pipefail

DEPLOY_HOST="${DEPLOY_HOST:?set DEPLOY_HOST=user@host (SSH target of the server)}"
SERVER_ADDR="${PLEXUS_SERVER:?set PLEXUS_SERVER=<private-address>:8799}"
TOKEN=$(awk -F= '$1=="PLEXUS_TOKEN"{print $2}' ~/.config/plexus/env)
[ -n "$TOKEN" ] || { echo "PLEXUS_TOKEN not found in ~/.config/plexus/env"; exit 1; }

scp dist/plexus-linux-amd64 "$DEPLOY_HOST":/tmp/plexus
ssh "$DEPLOY_HOST" "set -e
mkdir -p ~/.local/bin ~/.config/plexus ~/.local/state/plexus ~/.config/systemd/user
mv /tmp/plexus ~/.local/bin/plexus && chmod +x ~/.local/bin/plexus && ln -sf plexus ~/.local/bin/presence
printf 'PLEXUS_URL=http://%s\nPLEXUS_TOKEN=%s\nPLEXUS_HOST=server\nPLEXUS_BIND=%s\nPLEXUS_TTL=300s\n' '$SERVER_ADDR' '$TOKEN' '$SERVER_ADDR' > ~/.config/plexus/env
chmod 600 ~/.config/plexus/env
# Retire the pre-rename unit (if any) so it doesn't fight for the bind address.
systemctl --user disable --now presence.service 2>/dev/null || true
cat > ~/.config/systemd/user/plexus.service <<'UNIT'
[Unit]
Description=plexus — Plexus session registry
After=network-online.target

[Service]
EnvironmentFile=%h/.config/plexus/env
ExecStart=%h/.local/bin/plexus serve
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
UNIT
systemctl --user daemon-reload && systemctl --user enable --now plexus.service
sleep 2 && systemctl --user is-active plexus.service
curl -s -m 3 http://$SERVER_ADDR/healthz && echo ' <- healthz OK'"
