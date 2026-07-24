#!/bin/sh
# plexus.sh — install and configure both Plexus binaries in one shot.
#
#   curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/presence/main/plexus.sh | sh
#
# Installs `presence` (registry + cockpit + launcher, also symlinked `plexus`) and `edc` (event
# injection) — two independent binaries — then scaffolds their config so both are ready to use.
set -eu

INSTALL_DIR="${PLEXUS_INSTALL_DIR:-$HOME/.local/bin}"
RAW="https://raw.githubusercontent.com/jjuanrivvera"

say() { printf '%s\n' "$*"; }
gensecret() { head -c32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

say "Installing Plexus → $INSTALL_DIR"
say "  • presence (registry + cockpit + launcher)"
PRESENCE_INSTALL_DIR="$INSTALL_DIR" sh -c "$(curl -fsSL "$RAW/presence/main/install.sh")"
say "  • edc (event injection)"
EDC_INSTALL_DIR="$INSTALL_DIR" sh -c "$(curl -fsSL "$RAW/edc/main/install.sh")"

# --- config scaffolding (idempotent: never overwrites existing config) ---------------------------
PCONF="${XDG_CONFIG_HOME:-$HOME/.config}/presence/env"
ECONF="${XDG_CONFIG_HOME:-$HOME/.config}/edc/config.json"

if [ ! -f "$PCONF" ]; then
  mkdir -p "$(dirname "$PCONF")"
  TOKEN="$(gensecret)"
  {
    echo "PRESENCE_URL=http://127.0.0.1:8799   # the registry; a private/tailnet IP:port for multi-machine"
    echo "PRESENCE_TOKEN=$TOKEN"
    echo "PRESENCE_HOST=$(hostname 2>/dev/null | cut -d. -f1)"
  } > "$PCONF"
  say "  ✓ wrote $PCONF (generated PRESENCE_TOKEN)"
else
  say "  • $PCONF exists — left untouched"
fi

if [ ! -f "$ECONF" ]; then
  mkdir -p "$(dirname "$ECONF")"
  printf '{ "inject_secret": "%s", "inject_port": "auto" }\n' "$(gensecret)" > "$ECONF"
  say "  ✓ wrote $ECONF (generated inject_secret)"
else
  say "  • $ECONF exists — left untouched"
fi

# --- prereq check (attach/launcher needs these; injection alone does not) -------------------------
for t in tmux ttyd git; do
  command -v "$t" >/dev/null 2>&1 || say "  ⚠ $t not found — needed for the launcher/attach (not for edc injection alone)"
done

say ""
say "Installed:"
say "  $("$INSTALL_DIR/presence" version)   ($INSTALL_DIR/presence, also 'plexus')"
say "  edc                                  ($INSTALL_DIR/edc)"
say ""
say "Next — wire it into your agents with the single Plexus plugin:"
say "  Claude Code:  claude plugin marketplace add jjuanrivvera/presence"
say "                claude plugin install plexus@jjuanrivvera-plexus"
say "  Codex:        copy .codex-plugin from the edc repo (see docs)"
say "  OpenCode:     copy edc/.opencode-plugin/plexus.ts → ~/.config/opencode/plugins/plexus.ts"
say ""
say "Run the registry on your always-on host:  presence serve"
say "Docs: https://jjuanrivvera.github.io/presence/"
