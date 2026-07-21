#!/bin/bash
# Deploy presence to a server over SSH. Run from a machine that has the release binary built
# at dist/presence-linux-amd64 and a ~/.config/presence/env with PRESENCE_TOKEN.
#
#   DEPLOY_HOST=user@server \
#   PRESENCE_SERVER=<private-address>:8799 \
#   bash deploy/vps-deploy.sh
#
# The same PRESENCE_TOKEN ends up on both machines. PRESENCE_SERVER is the server's private
# (e.g. Tailscale/WireGuard) address, used for both the bind and the client URL.
set -euo pipefail

DEPLOY_HOST="${DEPLOY_HOST:?set DEPLOY_HOST=user@host (SSH target of the server)}"
SERVER_ADDR="${PRESENCE_SERVER:?set PRESENCE_SERVER=<private-address>:8799}"
TOKEN=$(awk -F= '$1=="PRESENCE_TOKEN"{print $2}' ~/.config/presence/env)
[ -n "$TOKEN" ] || { echo "PRESENCE_TOKEN not found in ~/.config/presence/env"; exit 1; }

scp dist/presence-linux-amd64 "$DEPLOY_HOST":/tmp/presence
ssh "$DEPLOY_HOST" "set -e
mkdir -p ~/.local/bin ~/.config/presence ~/.local/state/presence ~/.config/systemd/user
mv /tmp/presence ~/.local/bin/presence && chmod +x ~/.local/bin/presence
printf 'PRESENCE_URL=http://%s\nPRESENCE_TOKEN=%s\nPRESENCE_HOST=server\nPRESENCE_BIND=%s\nPRESENCE_TTL=300s\n' '$SERVER_ADDR' '$TOKEN' '$SERVER_ADDR' > ~/.config/presence/env
chmod 600 ~/.config/presence/env
cat > ~/.config/systemd/user/presence.service <<'UNIT'
[Unit]
Description=presence — ambient mesh session registry
After=network-online.target

[Service]
EnvironmentFile=%h/.config/presence/env
ExecStart=%h/.local/bin/presence serve
Restart=always
RestartSec=3

[Install]
WantedBy=default.target
UNIT
systemctl --user daemon-reload && systemctl --user enable --now presence.service
sleep 2 && systemctl --user is-active presence.service
curl -s -m 3 http://$SERVER_ADDR/healthz && echo ' <- healthz OK'"
