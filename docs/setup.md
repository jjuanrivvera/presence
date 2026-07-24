# Setup & requirements

## Requirements

The honest dependency picture — separating what's **required** from a particular setup:

| | Required? | Notes |
|---|---|---|
| A machine to run the agent(s) | **Yes** | macOS or Linux |
| Somewhere to run `plexus serve` | **Yes** | any host the agents can reach — including the same machine |
| A private network | Only for **multi-machine** | a VPN (Tailscale, WireGuard, Headscale, ZeroTier), a LAN, or `127.0.0.1` for one machine |
| `tmux` + `ttyd` | Only for **attach / launcher** | the registry + injection work without them; you just lose the clickable terminal |
| A specific VPS / cloud | **No** | any always-on reachable host: a Pi, a home server, a VM, or one of your machines |

**Single machine = zero extra infra.** Run `plexus serve` on `127.0.0.1`, agents register to loopback,
open the cockpit locally. No VPN, no second box.

!!! info "The one networking rule"
    `plexus serve` refuses to listen on `0.0.0.0` — it needs an explicit private address. On one machine
    that's `127.0.0.1`; across machines it's whatever your VPN or LAN assigns. That's why a VPN slots in
    so naturally (a stable private IP per machine), but it's interchangeable.

## Install

Both tools are single static Go binaries with checksum-verified releases.

### Both at once (recommended)

`plexus.sh` installs `plexus` and `edc`, drops the `plexus` symlink, and scaffolds
config for both (generating a registry token and an inject secret the first time):

```sh
curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/plexus/main/plexus.sh | sh
```

It never overwrites an existing config, so it's safe to re-run to upgrade.

### One at a time

The two binaries are independent — install only what you need:

```sh
# plexus (registry + cockpit + launcher; also installs the `plexus` symlink)
curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/plexus/main/install.sh | sh

# edc (event injection — usable entirely on its own, no plexus required)
curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/edc/main/install.sh | sh
```

`ttyd` (for the web terminal) is a single static binary from its
[releases](https://github.com/tsl0922/ttyd/releases); `tmux` from your package manager.
Neither is needed for `edc` injection on its own.

## One plugin, both tools

Plexus ships **a single Claude Code plugin** that wires both binaries into a session at once:

- **registration** (hooks → `plexus`): the session publishes itself into the registry and
  spawns its web terminal on start, keeps its state fresh, and deregisters on exit;
- **injection** (channel → `edc`): the session gets a local `/inject` endpoint so external
  events arrive as turns.

It also bundles the `plexus` skill. Install and enable it:

```sh
claude plugin marketplace add jjuanrivvera/plexus
claude plugin install plexus@jjuanrivvera-plexus
```

Then allow the injection channel by adding it to `allowedChannelPlugins` in your Claude
settings (`~/.claude/settings.json`) — `plexus.sh` prints the exact snippet:

```json
{ "allowedChannelPlugins": [ { "marketplace": "jjuanrivvera-plexus", "plugin": "plexus" } ] }
```

!!! note "Codex & OpenCode"
    **Codex** has its own equivalent plugin in the `edc` repo (`.codex-plugin/` — registration
    hooks + the injection adapter). **OpenCode** isn't a plugin: drop
    `edc/.opencode-plugin/plexus.ts` into `~/.config/opencode/plugins/` and launch decoupled
    (see [OpenCode](agents.md#opencode)).

## Configure

Config resolves **flag → env var → `~/.config/plexus/env`** (and `~/.config/edc/config.json` for edc).

```sh
# ~/.config/plexus/env — every machine
PLEXUS_URL=http://<registry-host>:<port>   # the registry (e.g. http://127.0.0.1:8799 for one machine)
PLEXUS_TOKEN=<shared-secret>             # bearer + cockpit login + terminal proxy auth
PLEXUS_HOST=<label>                      # this machine's label, e.g. laptop
```

```json
// ~/.config/edc/config.json — where injection is served
{ "inject_secret": "<secret>", "inject_port": "auto" }
```

Generate secrets with `openssl rand -hex 32`. Keep them out of version control.

## Run the registry

On the always-on host, run `plexus serve` as a service. A systemd user unit:

```ini
[Service]
Environment=PLEXUS_BIND=127.0.0.1:8799     # or a private IP:port for multi-machine
Environment=PLEXUS_TOKEN=<secret>
ExecStart=%h/.local/bin/plexus serve
Restart=always
```

Then, on any dev machine, launch an agent and it appears in the cockpit at `PLEXUS_URL/ui`:

```sh
plexus claude ~/code/api
```

## Portability

`plexus` is designed for **one developer with a handful of long-lived sessions across machines they own**.
The design generalizes; the implementation is a solo-to-small-team reference.

| Audience | Fit | What to change |
|---|---|---|
| **Another solo dev** | High | install, set `PLEXUS_URL`/`PLEXUS_TOKEN`, a private net — works as-is |
| **A small team** | Medium | a shared token means everyone sees/controls everything; no per-user identity or audit |
| **A company / product** | Blueprint | the *patterns* transfer; the *code* needs hardening (below) |

**The four things to change to scale up:**

1. **Auth** — replace the shared token with OIDC/SSO, per-user tokens, RBAC, and an audit log.
2. **Registry** — swap single-node SQLite for Postgres + HA + multi-tenancy.
3. **Attach** — `tmux`+`ttyd` assume persistent sessions on machines you own; ephemeral CI/cloud agents
   need a different attach path (or none).
4. **Config** — centralize hook/plugin/policy management instead of per-host installs.

**What transfers at any scale** — and is the real reusable value: the **agent-agnostic registry**, the
**uniform `/inject` contract with one adapter per agent**, and the **trust boundary**. That's the shape an
agent platform needs.
