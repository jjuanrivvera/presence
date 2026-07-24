# Setup & requirements

## Requirements

The honest dependency picture — separating what's **required** from a particular setup:

| | Required? | Notes |
|---|---|---|
| A machine to run the agent(s) | **Yes** | macOS or Linux |
| Somewhere to run `presence serve` | **Yes** | any host the agents can reach — including the same machine |
| A private network | Only for **multi-machine** | a VPN (Tailscale, WireGuard, Headscale, ZeroTier), a LAN, or `127.0.0.1` for one machine |
| `tmux` + `ttyd` | Only for **attach / launcher** | the registry + injection work without them; you just lose the clickable terminal |
| A specific VPS / cloud | **No** | any always-on reachable host: a Pi, a home server, a VM, or one of your machines |

**Single machine = zero extra infra.** Run `presence serve` on `127.0.0.1`, agents register to loopback,
open the cockpit locally. No VPN, no second box.

!!! info "The one networking rule"
    `presence serve` refuses to listen on `0.0.0.0` — it needs an explicit private address. On one machine
    that's `127.0.0.1`; across machines it's whatever your VPN or LAN assigns. That's why a VPN slots in
    so naturally (a stable private IP per machine), but it's interchangeable.

## Install

Both tools are single static Go binaries with checksum-verified releases.

```sh
# presence (also installs the `mesh` symlink)
curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/presence/main/install.sh | sh

# edc
curl -fsSL https://raw.githubusercontent.com/jjuanrivvera/edc/main/install.sh | sh
```

`ttyd` (for the web terminal) is a single static binary from its
[releases](https://github.com/tsl0922/ttyd/releases); `tmux` from your package manager.

## Configure

Config resolves **flag → env var → `~/.config/presence/env`** (and `~/.config/edc/config.json` for edc).

```sh
# ~/.config/presence/env — every machine
PRESENCE_URL=http://<registry-host>:<port>   # the registry (e.g. http://127.0.0.1:8799 for one machine)
PRESENCE_TOKEN=<shared-secret>             # bearer + cockpit login + terminal proxy auth
PRESENCE_HOST=<label>                      # this machine's label, e.g. laptop
```

```json
// ~/.config/edc/config.json — where injection is served
{ "inject_secret": "<secret>", "inject_port": "auto" }
```

Generate secrets with `openssl rand -hex 32`. Keep them out of version control.

## Run the registry

On the always-on host, run `presence serve` as a service. A systemd user unit:

```ini
[Service]
Environment=PRESENCE_BIND=127.0.0.1:8799     # or a private IP:port for multi-machine
Environment=PRESENCE_TOKEN=<secret>
ExecStart=%h/.local/bin/presence serve
Restart=always
```

Then, on any dev machine, launch an agent and it appears in the cockpit at `PRESENCE_URL/ui`:

```sh
mesh claude ~/code/api
```

## Portability

`mesh` is designed for **one developer with a handful of long-lived sessions across machines they own**.
The design generalizes; the implementation is a solo-to-small-team reference.

| Audience | Fit | What to change |
|---|---|---|
| **Another solo dev** | High | install, set `PRESENCE_URL`/`PRESENCE_TOKEN`, a private net — works as-is |
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
