package client

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// DetectRepo inspects dir with git and returns (repo basename, repo root path,
// branch). All three are "" when dir is not inside a git repo. This captures
// the LAUNCH cwd of a session once, at register — by design it is never
// refreshed mid-session.
func DetectRepo(dir string) (repo, repoPath, branch string) {
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", "", ""
	}
	repoPath = strings.TrimSpace(string(out))
	if repoPath == "" {
		return "", "", ""
	}
	repo = filepath.Base(repoPath)
	if out, err := exec.Command("git", "-C", dir, "branch", "--show-current").Output(); err == nil {
		branch = strings.TrimSpace(string(out))
	}
	return repo, repoPath, branch
}
