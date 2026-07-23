// launch.go — start an agent inside a named tmux session so it is attachable
// from the cockpit, and reattach to one. This is the anti-drift piece: instead
// of sessions being started ad-hoc (some in tmux, some not, on random sockets),
// `presence launch` always creates a session on the shared `mesh` socket, which
// the SessionStart hook then wires for attach automatically.
//
//	presence launch <claude|codex> [dir] [--detach] [-- args…]
//	presence attach <name>
//
// Ergonomic aliases (also via the `mesh` symlink): `mesh claude [dir]`.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
)

const meshSocket = "mesh"

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
	// args[0] is the agent; then optional [dir], [--detach], and [-- extra…].
	var agent, dir string
	var detach bool
	var extra []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			extra = append(extra, args[i+1:]...)
			i = len(args)
		case a == "--detach" || a == "-d":
			detach = true
		case agent == "":
			agent = a
		case dir == "":
			dir = a
		default:
			fatal("launch: unexpected argument %q", a)
		}
	}
	if agent != "claude" && agent != "codex" {
		fatal("launch: agent must be claude or codex (got %q)", agent)
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

	name := sessionName(abs)
	// Already alive for this repo? Reattach (interactive) instead of duplicating.
	if tmuxHasSession(meshSocket, name) {
		if detach {
			fmt.Printf("already running: %s (socket %s)\n", name, meshSocket)
			return
		}
		execTmuxAttach(meshSocket, name)
		return
	}

	tm := lookTool("tmux")
	if tm == "" {
		fatal("tmux not found")
	}
	// -e PRESENCE_AGENT so the session registers with the right kind (claude|codex).
	tmArgs := []string{"-L", meshSocket, "new-session", "-d", "-s", name,
		"-e", "PRESENCE_AGENT=" + agent, "-c", abs, agentBin}
	tmArgs = append(tmArgs, extra...)
	create := exec.Command(tm, tmArgs...)
	create.Stdout, create.Stderr = os.Stderr, os.Stderr
	if err := create.Run(); err != nil {
		fatal("launch: tmux new-session: %v", err)
	}

	if detach {
		fmt.Printf("▸ %s · %s in tmux -L %s · attachable from the cockpit in ~2s\n", name, agent, meshSocket)
		return
	}
	execTmuxAttach(meshSocket, name)
}

func cmdAttach(args []string) {
	name := argAt(args, 0)
	if name == "" {
		fatal("attach: need a session name (see `presence ls`)")
	}
	if !tmuxHasSession(meshSocket, name) {
		fatal("attach: no live mesh session %q", name)
	}
	execTmuxAttach(meshSocket, name)
}

// cmdKill ends a mesh session by name: kills the tmux session (which terminates
// the agent) and reaps its now-orphaned web terminal. The presence row clears on
// the session's own SessionEnd, or is pruned once it goes stale.
func cmdKill(args []string) {
	name := argAt(args, 0)
	if name == "" {
		fatal("kill: need a session name (see `mesh ls`)")
	}
	if !tmuxHasSession(meshSocket, name) {
		fatal("kill: no live mesh session %q", name)
	}
	tm := lookTool("tmux")
	if tm == "" {
		fatal("tmux not found")
	}
	if err := exec.Command(tm, "-L", meshSocket, "kill-session", "-t", name).Run(); err != nil {
		fatal("kill: %v", err)
	}
	ttydReap() // drop the ttyd whose tmux session just went away
	fmt.Printf("killed %s\n", name)
}
