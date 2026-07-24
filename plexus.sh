#!/bin/sh
# plexus.sh — install and configure both Plexus binaries in one shot.
#
#   curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/plexus/main/plexus.sh | sh
#
# Installs `plexus` (registry + cockpit + launcher) and `edc` (event injection) — two independent
# binaries — then scaffolds their config so both are ready to use.
set -eu

INSTALL_DIR="${PLEXUS_INSTALL_DIR:-$HOME/.local/bin}"
RAW="https://raw.githubusercontent.com/jjuanrivvera"

say() { printf '%s\n' "$*"; }
gensecret() { head -c32 /dev/urandom | od -An -tx1 | tr -d ' \n'; }

say "Installing Plexus → $INSTALL_DIR"
say "  • plexus (registry + cockpit + launcher)"
PLEXUS_INSTALL_DIR="$INSTALL_DIR" sh -c "$(curl -fsSL "$RAW/plexus/main/install.sh")"
say "  • edc (event injection)"
EDC_INSTALL_DIR="$INSTALL_DIR" sh -c "$(curl -fsSL "$RAW/edc/main/install.sh")"

# --- config scaffolding (idempotent: never overwrites existing config) ---------------------------
PCONF="${XDG_CONFIG_HOME:-$HOME/.config}/plexus/env"
OLDCONF="${XDG_CONFIG_HOME:-$HOME/.config}/presence/env"
ECONF="${XDG_CONFIG_HOME:-$HOME/.config}/edc/config.json"

# Migrate a pre-rename config: rename the PRESENCE_* keys and move the file so the
# same token keeps talking to a registry that hasn't been upgraded yet.
if [ ! -f "$PCONF" ] && [ -f "$OLDCONF" ]; then
  mkdir -p "$(dirname "$PCONF")"
  sed 's/^PRESENCE_/PLEXUS_/' "$OLDCONF" > "$PCONF"
  say "  ✓ migrated $OLDCONF → $PCONF (PRESENCE_* → PLEXUS_*)"
fi

if [ ! -f "$PCONF" ]; then
  mkdir -p "$(dirname "$PCONF")"
  TOKEN="$(gensecret)"
  {
    echo "PLEXUS_URL=http://127.0.0.1:8799   # the registry; a private/tailnet IP:port for multi-machine"
    echo "PLEXUS_TOKEN=$TOKEN"
    echo "PLEXUS_HOST=$(hostname 2>/dev/null | cut -d. -f1)"
  } > "$PCONF"
  say "  ✓ wrote $PCONF (generated PLEXUS_TOKEN)"
else
  say "  • $PCONF exists — left untouched"
fi

if [ ! -f "$ECONF" ]; then
  mkdir -p "$(dirname "$ECONF")"
  printf '{ "inject_secret": "%s", "inject_port": "auto", "inject_bind": "0.0.0.0" }\n' "$(gensecret)" > "$ECONF"
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
say "  $("$INSTALL_DIR/plexus" version)   ($INSTALL_DIR/plexus, + 'presence' back-compat symlink)"
say "  edc                                ($INSTALL_DIR/edc)"
say ""
say "Next — wire it into your agents with the single Plexus plugin:"
say "  Claude Code:  claude plugin marketplace add jjuanrivvera/plexus"
say "                claude plugin install plexus@jjuanrivvera-plexus"
say "  Codex:        copy .codex-plugin from the edc repo (see docs)"
say "  OpenCode:     copy edc/.opencode-plugin/plexus.ts → ~/.config/opencode/plugins/plexus.ts"
say ""
say "Run the registry on your always-on host:  plexus serve"
say "Docs: https://jjuanrivvera.github.io/plexus/"
