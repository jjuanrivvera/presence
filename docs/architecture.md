# Architecture

Plexus is two layers over one private network: **presence** (observe / control / launch) and **edc**
(inject). Each agent session sits in the middle — registered in presence, reachable by edc.

## Topology

```mermaid
flowchart TB
  subgraph net["private network — a VPN, a LAN, or one machine"]
    subgraph m1["a dev machine"]
      A1["agent session"]
    end
    subgraph m2["another machine"]
      A2["agent session"]
    end
    subgraph host["an always-on host"]
      SRV["presence serve<br/>SQLite registry + /ui cockpit"]
    end
  end
  A1 -->|register · heartbeat| SRV
  A2 -->|register · heartbeat| SRV
  SRV -->|reverse-proxy attach| A1
  SRV -->|reverse-proxy attach| A2
  You["you — any device"] --> SRV
```

- The **registry** runs on one always-on host. Every machine's agents register into it.
- The **cockpit** (`/ui`) and each session's **web terminal** are reachable from any device on the network.
- **edc** runs alongside each injectable session, exposing a local `/inject` endpoint.

None of it is fixed to a particular VPN, host type, or OS — see [Setup → Requirements](setup.md#requirements).
On a single machine the whole thing runs on `127.0.0.1`.

## How the two tools compose

| | **presence** | **edc** |
|---|---|---|
| Role | see, control, launch | feed events in as turns |
| Direction | you → session (observe/steer) | event → session (stimulus) |
| Surface | registry API, `/ui` cockpit, `plexus` launcher | `/inject` HTTP + per-agent adapters |
| Knows about the agent? | only its *kind* (a chip + a filter) | yes — one adapter per agent |

They meet at the **session**: edc injects a turn; presence lets you watch and steer that same session.

## An event's journey

```mermaid
sequenceDiagram
  participant X as external event
  participant E as edc /inject
  participant A as agent session
  participant P as presence cockpit
  X->>E: POST {source, event, text} + Bearer
  E->>A: deliver as a turn (untrusted data)
  A->>A: investigate / draft (no outward side effects)
  P-->>A: you attach, watch, type, or interrupt
```

## The registry row

Everything Plexus knows about a session is one row:

| Field | Meaning |
|---|---|
| `session_id` | stable id (the agent's own session id) |
| `host` | machine label (`laptop` / `server` / …) |
| `agent` | `claude` \| `codex` \| `opencode` |
| `repo` · `branch` · `repo_path` | what it's working on |
| `state` | `busy` \| `idle` \| `blocked` |
| `inject_port` | edc `/inject` port (`0` = visible but not injectable) |
| `attach_addr` | the session's web-terminal address, or empty |
| `started_at` · `last_seen` | timing; stale rows are pruned past the TTL |

State is deliberately small and self-cleaning: clients heartbeat, and the server prunes anything that goes
quiet past the TTL — a dead session drops off on its own.
