// presence — session registry for the ambient mesh.
//
// One binary: `presence serve` runs the HTTP service (VPS); the other
// subcommands (register/heartbeat/deregister/list/get/prune) are the client,
// used from any machine (typically via Claude Code hooks).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/jjuanrivvera/presence/internal/client"
	"github.com/jjuanrivvera/presence/internal/server"
	"github.com/jjuanrivvera/presence/internal/store"
	"github.com/jjuanrivvera/presence/internal/version"
)

// Exit codes: 0 success; 1 no-match (get only); 2 network/auth/server error.
const (
	exitOK      = 0
	exitNoMatch = 1
	exitErr     = 2
)

const (
	defaultBind = "127.0.0.1:8799"
	defaultTTL  = 300 * time.Second
)

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(exitErr)
	}
	cmd, args := os.Args[1], os.Args[2:]
	switch cmd {
	case "serve":
		cmdServe(args)
	case "register":
		cmdRegister(args)
	case "heartbeat":
		cmdHeartbeat(args)
	case "deregister":
		cmdDeregister(args)
	case "list", "ls":
		cmdList(args)
	case "watch":
		cmdWatch(args)
	case "get":
		cmdGet(args)
	case "prune":
		cmdPrune(args)
	case "launch":
		cmdLaunch(args)
	case "claude", "codex", "opencode":
		// ergonomic alias: `mesh claude [dir]` == `presence launch claude [dir]`
		cmdLaunch(append([]string{cmd}, args...))
	case "attach":
		cmdAttach(args)
	case "kill":
		cmdKill(args)
	case "ttyd":
		cmdTtyd(args)
	case "version":
		fmt.Println("presence " + version.String())
	case "-h", "--help", "help":
		usage()
	default:
		fmt.Fprintf(os.Stderr, "presence: unknown command %q\n\n", cmd)
		usage()
		os.Exit(exitErr)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `presence — session registry for the ambient mesh

Usage:
  presence serve      [--bind ADDR] [--db PATH] [--ttl 300s]
  presence register   [--session-id ID] [--inject-port N] [--host LABEL]
  presence heartbeat  [--session-id ID] [--state busy|idle]
  presence deregister [--session-id ID]
  presence list       [--host H] [--repo R] [--agent A] [--fresh 2m] [-o json|table]
  presence watch      [-n 2]     # live full-screen mesh cockpit (blocked-first, colored)
  presence get        --repo R [--host mac,pc] [--fresh 2m] [-o json]
  presence prune      [--older-than 10m]
  presence launch     <claude|codex|opencode> [dir] [--detach] [-- args…]   # start agent in tmux, attachable
  presence attach     <name>     # reattach to a mesh session (also: mesh claude [dir])
  presence kill       <name>     # end a mesh session (kills the agent + its terminal)
  presence ttyd       spawn <sid> <tmux-session> [socket] | kill <sid> | reap
  presence version

Installed as "mesh" too: mesh claude [dir], mesh ls, mesh attach NAME.

Config precedence: flag > env var > ~/.config/presence/env
Keys: PRESENCE_URL, PRESENCE_TOKEN, PRESENCE_HOST (client); PRESENCE_BIND, PRESENCE_TTL (serve)
`)
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "presence: "+format+"\n", a...)
	os.Exit(exitErr)
}

// ---- serve ----

func cmdServe(args []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	bindFlag := fs.String("bind", "", "address to bind (default $PRESENCE_BIND or "+defaultBind+")")
	dbFlag := fs.String("db", "", "SQLite path (default ~/.local/state/presence/presence.db)")
	ttlFlag := fs.String("ttl", "", "auto-prune TTL (default $PRESENCE_TTL or 300s)")
	fs.Parse(args)

	envFile := client.ParseEnvFile(client.EnvFilePath())

	token := client.Resolve("", "PRESENCE_TOKEN", envFile)
	if token == "" {
		// Fail closed: without a token every request would be unauthenticated.
		fatal("PRESENCE_TOKEN is required to serve")
	}

	bind := client.Resolve(*bindFlag, "PRESENCE_BIND", envFile)
	if bind == "" {
		bind = defaultBind
	}
	if host, _, err := net.SplitHostPort(bind); err != nil || host == "0.0.0.0" || host == "" || host == "::" {
		// Tailscale-only by policy: never listen on all interfaces.
		fatal("invalid bind %q: must be an explicit host:port, never 0.0.0.0", bind)
	}

	ttl := defaultTTL
	if raw := client.Resolve(*ttlFlag, "PRESENCE_TTL", envFile); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			fatal("invalid ttl %q", raw)
		}
		ttl = d
	}

	dbPath := *dbFlag
	if dbPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fatal("cannot resolve home dir: %v", err)
		}
		dbPath = filepath.Join(home, ".local", "state", "presence", "presence.db")
	}
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		fatal("cannot create db dir: %v", err)
	}

	st, err := store.Open(dbPath)
	if err != nil {
		fatal("open store: %v", err)
	}
	defer func() { _ = st.Close() }()

	srv := server.New(st, token, ttl)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.RunAutoPrune(ctx, 60*time.Second)

	httpSrv := &http.Server{
		Addr:         bind,
		Handler:      srv.Handler(),
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	fmt.Fprintf(os.Stderr, "presence %s serving on %s (db=%s ttl=%s)\n", version.String(), bind, dbPath, ttl)
	if err := httpSrv.ListenAndServe(); err != nil {
		fatal("serve: %v", err)
	}
}

// ---- client plumbing ----

type clientCtx struct {
	cli       *client.Client
	sessionID string
	host      string
}

func newClientCtx(sessionIDFlag, hostFlag string, needSession bool) clientCtx {
	envFile := client.ParseEnvFile(client.EnvFilePath())
	url := client.Resolve("", "PRESENCE_URL", envFile)
	if url == "" {
		fatal("PRESENCE_URL is required (flag/env/~/.config/presence/env)")
	}
	token := client.Resolve("", "PRESENCE_TOKEN", envFile)
	if token == "" {
		fatal("PRESENCE_TOKEN is required (env/~/.config/presence/env)")
	}
	var id string
	if needSession {
		var err error
		// Resolving may generate + persist a fallback id, so only do it for
		// commands that actually operate on "the current session".
		id, err = client.SessionID(sessionIDFlag)
		if err != nil {
			fatal("resolve session id: %v", err)
		}
	}
	host := client.HostLabel(client.Resolve(hostFlag, "PRESENCE_HOST", envFile))
	return clientCtx{cli: client.New(url, token), sessionID: id, host: host}
}

// buildRegisterReq captures the launch cwd once: repo/branch/repo_path are
// deliberately NOT refreshed mid-session (a session belongs to the dir it
// opened in).
func buildRegisterReq(cc clientCtx, injectPort int, agent, attachAddr string) client.RegisterReq {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "."
	}
	repo, repoPath, branch := client.DetectRepo(cwd)
	if repoPath == "" {
		repoPath = cwd
	}
	if injectPort == 0 {
		if v := os.Getenv("EDC_INJECT_PORT"); v != "" {
			if p, err := strconv.Atoi(v); err == nil {
				injectPort = p
			}
		}
	}
	// Agent resolves flag -> $PRESENCE_AGENT -> "" (server defaults empty to "claude"). The env
	// fallback lets heartbeat's 404 re-register preserve the agent without re-passing the flag.
	if agent == "" {
		agent = os.Getenv("PRESENCE_AGENT")
	}
	if attachAddr == "" {
		attachAddr = os.Getenv("PRESENCE_ATTACH_ADDR")
	}
	// Last resort: recover it from this session's live ttyd state file, so a
	// re-register that lost the env (keepalive / 404-recovery heartbeat) doesn't
	// wipe the attach address of a session whose web terminal is still running.
	if attachAddr == "" {
		attachAddr = attachAddrFromState(cc.sessionID)
	}
	return client.RegisterReq{
		SessionID:  cc.sessionID,
		Host:       cc.host,
		Repo:       repo,
		RepoPath:   repoPath,
		Branch:     branch,
		InjectPort: injectPort,
		PID:        os.Getppid(),
		Agent:      agent,
		AttachAddr: attachAddr,
	}
}

// ---- subcommands ----

func cmdRegister(args []string) {
	fs := flag.NewFlagSet("register", flag.ExitOnError)
	sessionID := fs.String("session-id", "", "session id (default $CLAUDE_SESSION_ID)")
	injectPort := fs.Int("inject-port", 0, "edc /inject port (default $EDC_INJECT_PORT, 0 = not injectable)")
	hostFlag := fs.String("host", "", "machine label (default $PRESENCE_HOST)")
	agent := fs.String("agent", "", "agent kind: claude|codex (default $PRESENCE_AGENT, else claude)")
	attachAddr := fs.String("attach-addr", "", "web-terminal host:port for attach (default $PRESENCE_ATTACH_ADDR)")
	fs.Parse(args)

	cc := newClientCtx(*sessionID, *hostFlag, true)
	if err := cc.cli.Register(buildRegisterReq(cc, *injectPort, *agent, *attachAddr)); err != nil {
		fatal("register: %v", err)
	}
	fmt.Println("ok")
}

func cmdHeartbeat(args []string) {
	fs := flag.NewFlagSet("heartbeat", flag.ExitOnError)
	sessionID := fs.String("session-id", "", "session id (default $CLAUDE_SESSION_ID)")
	state := fs.String("state", "", "busy|idle (default busy)")
	hostFlag := fs.String("host", "", "machine label (default $PRESENCE_HOST)")
	fs.Parse(args)

	if *state != "" && *state != "busy" && *state != "idle" && *state != "blocked" {
		fatal("--state must be busy, idle, or blocked")
	}
	cc := newClientCtx(*sessionID, *hostFlag, true)
	// The register payload doubles as the 404 recovery path (server pruned us); agent comes
	// from $PRESENCE_AGENT so a recovered codex row keeps its kind.
	if err := cc.cli.Heartbeat(cc.sessionID, *state, buildRegisterReq(cc, 0, "", "")); err != nil {
		fatal("heartbeat: %v", err)
	}
	fmt.Println("ok")
}

func cmdDeregister(args []string) {
	fs := flag.NewFlagSet("deregister", flag.ExitOnError)
	sessionID := fs.String("session-id", "", "session id (default $CLAUDE_SESSION_ID)")
	fs.Parse(args)

	cc := newClientCtx(*sessionID, "", true)
	if err := cc.cli.Deregister(cc.sessionID); err != nil {
		fatal("deregister: %v", err)
	}
	fmt.Println("ok")
}

func cmdList(args []string) {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	host := fs.String("host", "", "filter by exact host")
	repo := fs.String("repo", "", "filter by exact repo")
	agent := fs.String("agent", "", "filter by agent: claude|codex")
	fresh := fs.String("fresh", "", "freshness window, Go duration (server default 120s)")
	output := fs.String("o", "table", "output: table|json")
	fs.Parse(args)

	cc := newClientCtx("", "", false)
	rows, err := cc.cli.List(*host, *repo, *agent, *fresh)
	if err != nil {
		fatal("list: %v", err)
	}
	if *output == "json" {
		json.NewEncoder(os.Stdout).Encode(rows)
		return
	}
	fmt.Print(renderTable(rows, stdoutIsTTY()))
}

func cmdGet(args []string) {
	fs := flag.NewFlagSet("get", flag.ExitOnError)
	repo := fs.String("repo", "", "repo to match (required)")
	host := fs.String("host", "", "host filter, CSV = OR (e.g. mac,pc)")
	agent := fs.String("agent", "", "agent filter: claude|codex")
	fresh := fs.String("fresh", "", "freshness window, Go duration (server default 120s)")
	output := fs.String("o", "json", "output: json")
	fs.Parse(args)
	_ = output

	if strings.TrimSpace(*repo) == "" {
		fatal("get: --repo is required")
	}
	cc := newClientCtx("", "", false)
	row, err := cc.cli.Get(*repo, *host, *agent, *fresh)
	if err != nil {
		fatal("get: %v", err)
	}
	if row == nil {
		os.Exit(exitNoMatch) // empty output + exit 1: scriptable "no match"
	}
	json.NewEncoder(os.Stdout).Encode(row)
}

func cmdPrune(args []string) {
	fs := flag.NewFlagSet("prune", flag.ExitOnError)
	olderThan := fs.String("older-than", "", "prune rows older than this Go duration (default: server TTL)")
	fs.Parse(args)

	cc := newClientCtx("", "", false)
	n, err := cc.cli.Prune(*olderThan)
	if err != nil {
		fatal("prune: %v", err)
	}
	fmt.Printf("pruned %d\n", n)
}
