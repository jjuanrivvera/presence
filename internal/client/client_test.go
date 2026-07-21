package client

import (
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
)

func TestParseEnvFile(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    map[string]string
	}{
		{
			name:    "basic keys",
			content: "PRESENCE_URL=http://127.0.0.1:8799\nPRESENCE_TOKEN=s3cret\n",
			want:    map[string]string{"PRESENCE_URL": "http://127.0.0.1:8799", "PRESENCE_TOKEN": "s3cret"},
		},
		{
			name:    "comments and blanks skipped",
			content: "# mesh config\n\nPRESENCE_HOST=mac\n# PRESENCE_HOST=pc\n",
			want:    map[string]string{"PRESENCE_HOST": "mac"},
		},
		{
			name:    "quoted values unquoted",
			content: "A=\"hello world\"\nB='single'\n",
			want:    map[string]string{"A": "hello world", "B": "single"},
		},
		{
			name:    "value containing equals kept whole",
			content: "URL=http://x?a=1&b=2\n",
			want:    map[string]string{"URL": "http://x?a=1&b=2"},
		},
		{
			name:    "malformed lines ignored",
			content: "JUSTAWORD\n=novalue\nGOOD=yes\n",
			want:    map[string]string{"GOOD": "yes"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "env")
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatalf("write env file: %v", err)
			}
			got := ParseEnvFile(path)
			if len(got) != len(tt.want) {
				t.Fatalf("got %v, want %v", got, tt.want)
			}
			for k, v := range tt.want {
				if got[k] != v {
					t.Errorf("%s = %q, want %q", k, got[k], v)
				}
			}
		})
	}
}

func TestResolvePrecedence(t *testing.T) {
	envFile := map[string]string{"PRESENCE_TEST_KEY": "from-file"}
	tests := []struct {
		name    string
		flagVal string
		envVal  string
		want    string
	}{
		{"flag wins over env and file", "from-flag", "from-env", "from-flag"},
		{"env wins over file", "", "from-env", "from-env"},
		{"file is the fallback", "", "", "from-file"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.envVal != "" {
				t.Setenv("PRESENCE_TEST_KEY", tt.envVal)
			} else {
				t.Setenv("PRESENCE_TEST_KEY", "")
			}
			if got := Resolve(tt.flagVal, "PRESENCE_TEST_KEY", envFile); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func mustGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

func TestDetectRepo(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(t *testing.T) string // returns dir to detect from
		wantRepo   func(dir string) string
		wantBranch string
	}{
		{
			name: "non-repo dir returns empty strings",
			setup: func(t *testing.T) string {
				return t.TempDir()
			},
			wantRepo:   func(string) string { return "" },
			wantBranch: "",
		},
		{
			name: "repo root detected with branch",
			setup: func(t *testing.T) string {
				dir := filepath.Join(t.TempDir(), "myrepo")
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatal(err)
				}
				mustGit(t, dir, "init", "-b", "feature-x")
				return dir
			},
			wantRepo:   func(string) string { return "myrepo" },
			wantBranch: "feature-x",
		},
		{
			name: "subdirectory resolves to toplevel",
			setup: func(t *testing.T) string {
				root := filepath.Join(t.TempDir(), "toprepo")
				sub := filepath.Join(root, "a", "b")
				if err := os.MkdirAll(sub, 0o755); err != nil {
					t.Fatal(err)
				}
				mustGit(t, root, "init", "-b", "main")
				return sub
			},
			wantRepo:   func(string) string { return "toprepo" },
			wantBranch: "main",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := tt.setup(t)
			repo, repoPath, branch := DetectRepo(dir)
			if repo != tt.wantRepo(dir) {
				t.Errorf("repo = %q, want %q", repo, tt.wantRepo(dir))
			}
			if branch != tt.wantBranch {
				t.Errorf("branch = %q, want %q", branch, tt.wantBranch)
			}
			if repo == "" && repoPath != "" {
				t.Errorf("repoPath = %q, want empty for non-repo", repoPath)
			}
			if repo != "" && filepath.Base(repoPath) != repo {
				t.Errorf("repoPath %q does not end in repo %q", repoPath, repo)
			}
		})
	}
}

// TestHeartbeat404ReRegisters covers the suspend-recovery flow: the server
// pruned the session, heartbeat gets a 404, the client re-registers and
// retries the heartbeat once.
func TestHeartbeat404ReRegisters(t *testing.T) {
	var registered atomic.Bool
	var registerCalls, heartbeatCalls atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/register", func(w http.ResponseWriter, r *http.Request) {
		registerCalls.Add(1)
		registered.Store(true)
		w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/heartbeat", func(w http.ResponseWriter, r *http.Request) {
		heartbeatCalls.Add(1)
		if !registered.Load() {
			w.WriteHeader(http.StatusNotFound)
			w.Write([]byte(`{"ok":false,"error":"unknown session"}`))
			return
		}
		w.Write([]byte(`{"ok":true}`))
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	c := New(ts.URL, "tok")
	reg := RegisterReq{SessionID: "s1", Host: "mac"}

	if err := c.Heartbeat("s1", "busy", reg); err != nil {
		t.Fatalf("Heartbeat: %v", err)
	}
	if got := registerCalls.Load(); got != 1 {
		t.Errorf("register calls = %d, want 1", got)
	}
	if got := heartbeatCalls.Load(); got != 2 {
		t.Errorf("heartbeat calls = %d, want 2 (404 then retry)", got)
	}

	// Second heartbeat: session known, no extra register.
	if err := c.Heartbeat("s1", "busy", reg); err != nil {
		t.Fatalf("second Heartbeat: %v", err)
	}
	if got := registerCalls.Load(); got != 1 {
		t.Errorf("register calls after 2nd hb = %d, want still 1", got)
	}
}

func TestHostLabel(t *testing.T) {
	tests := []struct {
		name     string
		resolved string
		want     string // "" = depends on hostname, skip exact check
	}{
		{"explicit label wins", "mac", "mac"},
		{"fallback is lowercase no-dots", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := HostLabel(tt.resolved)
			if tt.want != "" {
				if got != tt.want {
					t.Errorf("got %q, want %q", got, tt.want)
				}
				return
			}
			if got == "" {
				t.Fatal("fallback label is empty")
			}
			for _, ch := range got {
				if ch == '.' || (ch >= 'A' && ch <= 'Z') {
					t.Errorf("fallback %q contains uppercase or dot", got)
				}
			}
		})
	}
}
