# Agents

Plexus is agent-agnostic by design: every agent goes through the same two presence calls and the same
`/inject` contract. What differs is *where* registration is wired and *how* the injected turn is received.

## Support matrix

| Capability | Claude Code | Codex | OpenCode |
|---|:---:|:---:|:---:|
| Register in Plexus | ✅ hook | ✅ plugin | ✅ plugin |
| Live terminal (attach) | ✅ | ✅ | ✅ |
| Launch via `plexus` | ✅ | ✅ | ✅ decoupled |
| External turn injection | ✅ channel | ✅ app-server | ✅ HTTP |
| Injected turn is **visible** | ✅ native | ✅ | ✅ TUI mode |
| Native `source=system` marker | ✅ | framed¹ | framed¹ |
| Interrupt a running turn | ✅ | ✅ | ✅ `/abort` |

¹ **framed** — the trust boundary is reconstructed as text (`SYSTEM EVENT (untrusted data)`) since the
agent has no native system marker.

## How each is wired

### Claude Code

- **Registration:** hooks in `presence/hooks/` — `session-start.sh` runs `presence ttyd spawn` +
  `presence register` (`agent=claude`); `session-end.sh` deregisters; a keepalive heartbeats idle sessions.
- **Injection:** the `edc` **channel** — an MCP stdio server declaring `claude/channel`. Events arrive as
  native `notifications/claude/channel` turns with `meta.source="system"`.
- **Install:** the `event-driven-claude` plugin (`claude plugin install …`) and the presence hooks in
  `settings.json`.

### Codex

- **Registration:** the `.codex-plugin` shipped in `edc` — its `SessionStart` hook registers interactive
  Codex sessions (`agent=codex`, `inject_port=0`) and spawns the web terminal. The always-on injectable
  session is the `edc codex serve` daemon, which self-registers with a real inject port.
- **Injection:** `edc codex serve` drives a `codex app-server` thread over JSON-RPC; each event becomes a
  `turn/start`. Pinned to approval-never + read-only sandbox so an injected turn can't act on its own.
- **Trust:** no native system marker → framed as text.

### OpenCode

- **Registration:** the `.opencode-plugin` shipped in `edc` (`plexus.ts` → `~/.config/opencode/plugins/`).
  On `session.created` it runs `presence ttyd spawn` + `presence register` (`agent=opencode`), reading the
  inject port from `$EDC_INJECT_PORT`.
- **Injection:** `edc opencode serve`. OpenCode is client-server, so `plexus opencode` launches a **decoupled
  stack** — an addressable `opencode serve`, a TUI-mode `edc` sidecar on a fixed inject port, and the
  `opencode attach` you interact with. Injected turns are typed into the attached session via `/tui/*` so
  they render (working around
  [sst/opencode#8564](https://github.com/sst/opencode/issues/8564)).
- **Trust:** no native system marker → framed as text.

## Adding another agent

The pattern to support a new agent:

1. **Register** it — a session-start hook/plugin that calls `presence ttyd spawn` + `presence register
   --agent <name>` (and a session-end that deregisters). `presence` accepts any lowercase agent name.
2. **Receive** injection — an `edc <name> serve` adapter that runs the `/inject` listener and translates
   each event into that agent's "start a turn" primitive, reconstructing the trust boundary if the agent
   has no native system marker.

`presence` needs no change to add an agent — only a hook and an adapter.
