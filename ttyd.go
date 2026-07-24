// ttyd.go — per-session web-terminal (ttyd) lifecycle, folded into the plexus
// binary so there is a single versioned artifact for all of Plexus (no bash
// script to drift between machines — the exact failure mode that once 404'd a
// session whose standalone ttyd wrapper had gone stale).
//
//	plexus ttyd spawn <session-id> <tmux-session> [socket]
//	plexus ttyd kill  <session-id>
//	plexus ttyd reap
//
// ttyd binds the Tailscale IP only (the tailnet is the perimeter) and requires
// basic auth (plexus:$PLEXUS_TOKEN); it also runs with base-path
// /attach/<session-id> so the plexus server can reverse-proxy it without
// rewriting asset/WebSocket paths. Fail-soft everywhere.
package main

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jjuanrivvera/plexus/internal/client"
)

const (
	ttydPortLo = 17600
	ttydPortHi = 17699
	// xterm theme + size so the embedded terminal blends into the cockpit viewport.
	ttydTheme    = `theme={"background":"#0b0d10","foreground":"#d8dde4","cursor":"#e0a44a","selectionBackground":"#2b3140"}`
	ttydFontSize = "fontSize=13"
)

func ttydStateDir() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "state", "plexus", "ttyd")
}

// lookTool resolves an executable, falling back to the dirs Plexus installs
// into when a Claude Code hook's PATH is minimal.
func lookTool(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, d := range []string{filepath.Join(home, ".local", "bin"), "/opt/homebrew/bin", "/usr/local/bin", "/usr/bin", "/bin"} {
		p := filepath.Join(d, name)
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return p
		}
	}
	return ""
}

// tsIP returns this machine's Tailscale IPv4, via the CLI or the macOS app bundle.
func tsIP() string {
	cands := []string{lookTool("tailscale"), "/Applications/Tailscale.app/Contents/MacOS/Tailscale"}
	for _, ts := range cands {
		if ts == "" {
			continue
		}
		if out, err := exec.Command(ts, "ip", "-4").Output(); err == nil {
			if line := strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]); line != "" {
				return line
			}
		}
	}
	return ""
}

func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	return err == nil || err == syscall.EPERM
}

func tmuxHasSession(sock, sess string) bool {
	tm := lookTool("tmux")
	if tm == "" {
		return false
	}
	return exec.Command(tm, "-L", sock, "has-session", "-t", sess).Run() == nil
}

func portInUse(p int) bool {
	c, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%d", p), 200*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// readTtydState parses a "pid port tsess [sock]" state file.
func readTtydState(path string) (pid, port int, tsess, sock string, ok bool) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	f := strings.Fields(string(b))
	if len(f) < 3 {
		return
	}
	pid, _ = strconv.Atoi(f[0])
	port, _ = strconv.Atoi(f[1])
	tsess = f[2]
	sock = "default"
	if len(f) >= 4 {
		sock = f[3]
	}
	ok = pid != 0 && port != 0 && tsess != ""
	return
}

// attachAddrFromState reconstructs a session's web-terminal address from its ttyd
// state file (tailscale-ip:port). This is the durable local truth: a re-register
// that lost $PLEXUS_ATTACH_ADDR (e.g. the keepalive or a 404-recovery heartbeat,
// which don't carry that env) can recover it here instead of wiping attach_addr.
func attachAddrFromState(sid string) string {
	if sid == "" {
		return ""
	}
	_, port, _, _, ok := readTtydState(filepath.Join(ttydStateDir(), sid))
	if !ok || port == 0 {
		return ""
	}
	if ip := tsIP(); ip != "" {
		return fmt.Sprintf("%s:%d", ip, port)
	}
	return ""
}

func freePort() int {
	recorded := map[int]bool{}
	if entries, err := os.ReadDir(ttydStateDir()); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			if _, port, _, _, ok := readTtydState(filepath.Join(ttydStateDir(), e.Name())); ok {
				recorded[port] = true
			}
		}
	}
	for p := ttydPortLo; p <= ttydPortHi; p++ {
		if recorded[p] {
			continue
		}
		if !portInUse(p) {
			return p
		}
	}
	return 0
}

// ttydReap kills ttyds whose owning process died or whose tmux session is gone.
func ttydReap() {
	dir := ttydStateDir()
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		path := filepath.Join(dir, e.Name())
		pid, _, tsess, sock, ok := readTtydState(path)
		if !ok {
			_ = os.Remove(path)
			continue
		}
		if !pidAlive(pid) || !tmuxHasSession(sock, tsess) {
			if pid > 0 {
				_ = syscall.Kill(pid, syscall.SIGTERM)
			}
			_ = os.Remove(path)
		}
	}
}

// envWithout returns env with any KEY=… entries removed.
func envWithout(env []string, key string) []string {
	out := make([]string, 0, len(env))
	pre := key + "="
	for _, e := range env {
		if !strings.HasPrefix(e, pre) {
			out = append(out, e)
		}
	}
	return out
}

// ttydSpawn starts (or re-reports) a ttyd attaching to the given tmux session and
// prints its attach address (tailscale-ip:port). The socket is taken as an arg, or
// derived from $TMUX (the hook runs inside the session), else "default".
func ttydSpawn(sid, tsess, sock string) {
	if sid == "" || tsess == "" {
		return
	}
	if sock == "" {
		if t := os.Getenv("TMUX"); t != "" {
			sock = filepath.Base(strings.SplitN(t, ",", 2)[0])
		}
	}
	if sock == "" {
		sock = "default"
	}
	ttydReap()
	dir := ttydStateDir()
	_ = os.MkdirAll(dir, 0o755)
	statePath := filepath.Join(dir, sid)
	if pid, port, _, _, ok := readTtydState(statePath); ok && pidAlive(pid) {
		fmt.Printf("%s:%d\n", tsIP(), port) // already serving
		return
	}
	_ = os.Remove(statePath)

	ttyd := lookTool("ttyd")
	tm := lookTool("tmux")
	if ttyd == "" || tm == "" {
		return
	}
	ip := tsIP()
	if ip == "" || !tmuxHasSession(sock, tsess) {
		return
	}
	port := freePort()
	if port == 0 {
		return
	}
	tok := client.Resolve("", "PLEXUS_TOKEN", client.ParseEnvFile(client.EnvFilePath()))
	if tok == "" {
		tok = "plexus"
	}
	args := []string{
		"-p", strconv.Itoa(port), "-i", ip, "-W",
		"-c", "plexus:" + tok,
		"-b", "/attach/" + sid,
		"-t", "disableLeaveAlert=true",
		"-t", ttydFontSize,
		"-t", ttydTheme,
		tm, "-L", sock, "attach", "-t", tsess,
	}
	cmd := exec.Command(ttyd, args...)
	// Drop $TMUX so the child `tmux attach` is not refused as a nested client; detach
	// into its own session so it outlives this short-lived process.
	cmd.Env = envWithout(os.Environ(), "TMUX")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if devnull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0); err == nil {
		cmd.Stdin, cmd.Stdout, cmd.Stderr = devnull, devnull, devnull
	}
	if err := cmd.Start(); err != nil {
		return
	}
	_ = os.WriteFile(statePath, []byte(fmt.Sprintf("%d %d %s %s\n", cmd.Process.Pid, port, tsess, sock)), 0o644)
	fmt.Printf("%s:%d\n", ip, port)
}

func ttydKill(sid string) {
	if sid == "" {
		return
	}
	statePath := filepath.Join(ttydStateDir(), sid)
	if pid, _, _, _, ok := readTtydState(statePath); ok && pid > 0 {
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	_ = os.Remove(statePath)
}

func argAt(a []string, i int) string {
	if i < len(a) {
		return a[i]
	}
	return ""
}

func cmdTtyd(args []string) {
	switch argAt(args, 0) {
	case "spawn":
		sid, tsess := argAt(args, 1), argAt(args, 2)
		if sid == "" || tsess == "" {
			fatal("ttyd spawn: need <session-id> <tmux-session> [socket]")
		}
		ttydSpawn(sid, tsess, argAt(args, 3))
	case "kill":
		ttydKill(argAt(args, 1))
	case "reap":
		ttydReap()
	default:
		fatal("ttyd: need spawn|kill|reap")
	}
}
