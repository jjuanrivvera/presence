// launch.go — start an agent inside a named tmux session so it is attachable
// from the cockpit, and reattach to one. This is the anti-drift piece: instead
// of sessions being started ad-hoc (some in tmux, some not, on random sockets),
// `plexus launch` always creates a session on the shared `plexus` socket, which
// the SessionStart hook then wires for attach automatically.
//
//	plexus launch <claude|codex|opencode> [dir] [--detach] [--worktree] [-- args…]
//	plexus attach <name>
//
// Ergonomic aliases (also via the `plexus` symlink): `plexus claude [dir]`.
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

const plexusSocket = "plexus"

func freeTCPPort() int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func shQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

// opencodeWrapCmd builds the tmux session command for a decoupled OpenCode stack, so an
// interactive session is both attachable AND injectable: an addressable `opencode serve`, a
// TUI-mode `edc opencode serve` sidecar on a fixed inject port EP (injects visibly via /tui/*),
// and the `opencode attach` the human sees. The OpenCode plugin registers the session in plexus
// with inject_port=EP (it reads $EDC_INJECT_PORT), so `edc /inject` events land in the live TUI.
// Without edc on PATH it degrades to plain attach (attachable, not injectable).
func opencodeWrapCmd(ocBin, dir string, extra []string) []string {
	edcBin := lookTool("edc")
	p, ep := freeTCPPort(), freeTCPPort()
	var sb strings.Builder
	// Export the inject port BEFORE `opencode serve` so the server — which runs the Plexus plugin —
	// inherits it and the plugin registers the session with inject_port=EP (not 0). Order matters:
	// the plugin lives in the server process, not in the later `opencode attach`.
	if edcBin != "" {
		fmt.Fprintf(&sb, "export EDC_INJECT_PORT=%d\n", ep)
	}
	fmt.Fprintf(&sb, "%s serve --port %d --hostname 127.0.0.1 >/dev/null 2>&1 &\nSRV=$!\n", shQuote(ocBin), p)
	// wait for the server's port to open before attaching / injecting (bash /dev/tcp probe).
	fmt.Fprintf(&sb, "for _ in $(seq 1 60); do (exec 3<>/dev/tcp/127.0.0.1/%d) 2>/dev/null && { exec 3>&-; break; }; sleep 0.3; done\n", p)
	if edcBin != "" {
		fmt.Fprintf(&sb, "EDC_OPENCODE_URL=http://127.0.0.1:%d EDC_OPENCODE_TUI=1 %s opencode serve >/dev/null 2>&1 &\nEDCP=$!\n", p, shQuote(edcBin))
		sb.WriteString("trap 'kill $SRV $EDCP 2>/dev/null' EXIT\n")
	} else {
		sb.WriteString("trap 'kill $SRV 2>/dev/null' EXIT\n")
	}
	fmt.Fprintf(&sb, "exec %s attach http://127.0.0.1:%d", shQuote(ocBin), p)
	for _, a := range extra {
		fmt.Fprintf(&sb, " %s", shQuote(a))
	}
	sb.WriteString("\n")
	return []string{"bash", "-lc", sb.String()}
}

var nameStrip = regexp.MustCompile(`[^a-z0-9-]+`)

// sessionName derives a tmux session name from the git toplevel (or dir) basename.
func sessionName(dir string) string {
	base := dir
	if git := lookTool("git"); git != "" {
		c := exec.Command(git, "rev-parse", "--show-toplevel")
		c.Dir = dir
		if out, err := c.Output(); err == nil {
			if t := strings.TrimSpace(string(out)); t != "" {
				base = t
			}
		}
	}
	name := strings.ToLower(filepath.Base(base))
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.Trim(nameStrip.ReplaceAllString(name, ""), "-")
	if name == "" {
		name = "session"
	}
	return name
}

// makeWorktree creates a fresh git worktree + branch for dir's repo so a session gets its own
// isolated checkout — no cross-agent filesystem/branch collisions. The worktree lives under
// ~/.local/state/plexus/worktrees/ (outside the repo, so it never pollutes the working tree).
// Requires dir to be inside a git repo. Returns the worktree path.
func makeWorktree(dir, agent string) (string, error) {
	git := lookTool("git")
	if git == "" {
		return "", fmt.Errorf("git not found")
	}
	c := exec.Command(git, "rev-parse", "--show-toplevel")
	c.Dir = dir
	out, err := c.Output()
	if err != nil {
		return "", fmt.Errorf("%q is not inside a git repo (--worktree needs one)", dir)
	}
	top := strings.TrimSpace(string(out))
	home, _ := os.UserHomeDir()
	suffix := time.Now().Format("0102-150405")
	name := filepath.Base(top) + "-" + agent + "-" + suffix
	wt := filepath.Join(home, ".local", "state", "plexus", "worktrees", name)
	branch := "plexus/" + agent + "-" + suffix
	if err := os.MkdirAll(filepath.Dir(wt), 0o755); err != nil {
		return "", err
	}
	add := exec.Command(git, "-C", top, "worktree", "add", "-b", branch, wt)
	add.Stdout, add.Stderr = os.Stderr, os.Stderr
	if err := add.Run(); err != nil {
		return "", fmt.Errorf("git worktree add: %w", err)
	}
	return wt, nil
}

// execTmuxAttach replaces this process with `tmux attach`, dropping the user into
// the session. $TMUX is stripped so a launch from inside another tmux is not refused.
func execTmuxAttach(sock, name string) {
	tm := lookTool("tmux")
	if tm == "" {
		fatal("tmux not found")
	}
	if err := syscall.Exec(tm, []string{"tmux", "-L", sock, "attach", "-t", name}, envWithout(os.Environ(), "TMUX")); err != nil {
		fatal("attach: %v", err)
	}
}

func cmdLaunch(args []string) {
	// args[0] is the agent; then optional [dir], [--detach], [--worktree], and [-- extra…].
	var agent, dir string
	var detach, worktree bool
	var extra []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			extra = append(extra, args[i+1:]...)
			i = len(args)
		case a == "--detach" || a == "-d":
			detach = true
		case a == "--worktree" || a == "-w":
			worktree = true
		case agent == "":
			agent = a
		case dir == "":
			dir = a
		default:
			fatal("launch: unexpected argument %q", a)
		}
	}
	if agent != "claude" && agent != "codex" && agent != "opencode" {
		fatal("launch: agent must be claude, codex, or opencode (got %q)", agent)
	}
	agentBin := lookTool(agent)
	if agentBin == "" {
		fatal("launch: %s not found in PATH", agent)
	}
	if dir == "" {
		wd, err := os.Getwd()
		if err != nil {
			fatal("cwd: %v", err)
		}
		dir = wd
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		fatal("dir: %v", err)
	}
	if fi, err := os.Stat(abs); err != nil || !fi.IsDir() {
		fatal("launch: %q is not a directory", abs)
	}

	// --worktree: give this session its own isolated git worktree + branch, so concurrent
	// agents on the same repo can't collide. The session then runs in that worktree.
	if worktree {
		wt, err := makeWorktree(abs, agent)
		if err != nil {
			fatal("launch: %v", err)
		}
		abs = wt
		fmt.Fprintf(os.Stderr, "worktree: %s\n", wt)
	}

	name := sessionName(abs)
	// Already alive for this repo? Reattach (interactive) instead of duplicating.
	if tmuxHasSession(plexusSocket, name) {
		if detach {
			fmt.Printf("already running: %s (socket %s)\n", name, plexusSocket)
			return
		}
		execTmuxAttach(plexusSocket, name)
		return
	}

	tm := lookTool("tmux")
	if tm == "" {
		fatal("tmux not found")
	}
	// The session command: the agent directly for claude/codex, or the decoupled OpenCode stack
	// (serve + edc sidecar + attach) so an interactive OpenCode session is also injectable.
	sessCmd := append([]string{agentBin}, extra...)
	if agent == "opencode" {
		sessCmd = opencodeWrapCmd(agentBin, abs, extra)
	}
	// -e PLEXUS_AGENT so the session registers with the right kind (claude|codex|opencode).
	tmArgs := append([]string{"-L", plexusSocket, "new-session", "-d", "-s", name,
		"-e", "PLEXUS_AGENT=" + agent, "-c", abs}, sessCmd...)
	create := exec.Command(tm, tmArgs...)
	create.Stdout, create.Stderr = os.Stderr, os.Stderr
	if err := create.Run(); err != nil {
		fatal("launch: tmux new-session: %v", err)
	}

	if detach {
		fmt.Printf("▸ %s · %s in tmux -L %s · attachable from the cockpit in ~2s\n", name, agent, plexusSocket)
		return
	}
	execTmuxAttach(plexusSocket, name)
}

func cmdAttach(args []string) {
	name := argAt(args, 0)
	if name == "" {
		fatal("attach: need a session name (see `plexus ls`)")
	}
	if !tmuxHasSession(plexusSocket, name) {
		fatal("attach: no live plexus session %q", name)
	}
	execTmuxAttach(plexusSocket, name)
}

// cmdKill ends a plexus session by name: kills the tmux session (which terminates
// the agent) and reaps its now-orphaned web terminal. The plexus row clears on
// the session's own SessionEnd, or is pruned once it goes stale.
func cmdKill(args []string) {
	name := argAt(args, 0)
	if name == "" {
		fatal("kill: need a session name (see `plexus ls`)")
	}
	if !tmuxHasSession(plexusSocket, name) {
		fatal("kill: no live plexus session %q", name)
	}
	tm := lookTool("tmux")
	if tm == "" {
		fatal("tmux not found")
	}
	if err := exec.Command(tm, "-L", plexusSocket, "kill-session", "-t", name).Run(); err != nil {
		fatal("kill: %v", err)
	}
	ttydReap() // drop the ttyd whose tmux session just went away
	fmt.Printf("killed %s\n", name)
}
