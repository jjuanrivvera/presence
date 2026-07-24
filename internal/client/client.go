// Package client implements the plexus HTTP client used by the CLI
// subcommands and the Claude Code hooks.
package client

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/jjuanrivvera/plexus/internal/store"
)

// Timeout is deliberately short: the hooks must never hang a Claude session.
const Timeout = 2 * time.Second

type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
}

func New(baseURL, token string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		Token:   token,
		HTTP:    &http.Client{Timeout: Timeout},
	}
}

// RegisterReq mirrors the /register body. The client never sends timestamps —
// the server stamps all times (avoids clock skew across machines).
type RegisterReq struct {
	SessionID  string `json:"session_id"`
	Host       string `json:"host"`
	Repo       string `json:"repo"`
	RepoPath   string `json:"repo_path"`
	Branch     string `json:"branch"`
	InjectPort int    `json:"inject_port"`
	PID        int    `json:"pid"`
	Agent      string `json:"agent,omitempty"`       // omit when empty so old servers ignore it
	AttachAddr string `json:"attach_addr,omitempty"` // ttyd host:port for this session, or empty
}

func (c *Client) post(path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, c.BaseURL+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	return c.HTTP.Do(req)
}

func (c *Client) get(path string, q url.Values) (*http.Response, error) {
	u := c.BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequest(http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	return c.HTTP.Do(req)
}

func drainCheck(resp *http.Response) error {
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusOK {
		io.Copy(io.Discard, resp.Body)
		return nil
	}
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	return fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
}

func (c *Client) Register(req RegisterReq) error {
	resp, err := c.post("/register", req)
	if err != nil {
		return err
	}
	return drainCheck(resp)
}

// Heartbeat posts /heartbeat. On 404 (the server already pruned the row —
// e.g. the Mac slept past the TTL) it re-registers with reg and retries the
// heartbeat once, so long-lived sessions survive suspends unattended.
func (c *Client) Heartbeat(sessionID, state string, reg RegisterReq) error {
	body := map[string]string{"session_id": sessionID}
	if state != "" {
		body["state"] = state
	}
	resp, err := c.post("/heartbeat", body)
	if err != nil {
		return err
	}
	if resp.StatusCode == http.StatusNotFound {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if err := c.Register(reg); err != nil {
			return fmt.Errorf("re-register after 404: %w", err)
		}
		resp, err = c.post("/heartbeat", body)
		if err != nil {
			return err
		}
	}
	return drainCheck(resp)
}

func (c *Client) Deregister(sessionID string) error {
	resp, err := c.post("/deregister", map[string]string{"session_id": sessionID})
	if err != nil {
		return err
	}
	return drainCheck(resp)
}

func (c *Client) List(host, repo, agent, fresh string) ([]store.Session, error) {
	q := url.Values{}
	if host != "" {
		q.Set("host", host)
	}
	if repo != "" {
		q.Set("repo", repo)
	}
	if agent != "" {
		q.Set("agent", agent)
	}
	if fresh != "" {
		q.Set("fresh", fresh)
	}
	resp, err := c.get("/list", q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var rows []store.Session
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return nil, err
	}
	return rows, nil
}

// Get runs the delegation query. Returns (nil, nil) on 204 (no match).
func (c *Client) Get(repo, host, agent, fresh string) (*store.Session, error) {
	q := url.Values{"repo": {repo}}
	if host != "" {
		q.Set("host", host)
	}
	if agent != "" {
		q.Set("agent", agent)
	}
	if fresh != "" {
		q.Set("fresh", fresh)
	}
	resp, err := c.get("/get", q)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch resp.StatusCode {
	case http.StatusNoContent:
		return nil, nil
	case http.StatusOK:
		var row store.Session
		if err := json.NewDecoder(resp.Body).Decode(&row); err != nil {
			return nil, err
		}
		return &row, nil
	default:
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
}

func (c *Client) Prune(olderThan string) (int64, error) {
	body := map[string]string{}
	if olderThan != "" {
		body["older_than"] = olderThan
	}
	resp, err := c.post("/prune", body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return 0, fmt.Errorf("server returned %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	var out struct {
		Pruned int64 `json:"pruned"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, err
	}
	return out.Pruned, nil
}

// SessionID resolves the session id: explicit flag, then CLAUDE_SESSION_ID,
// then a per-machine generated id persisted under the state dir so that
// heartbeat/deregister from separate hook processes reuse the same one.
func SessionID(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if v := os.Getenv("CLAUDE_SESSION_ID"); v != "" {
		return v, nil
	}
	dir := StateDir()
	path := filepath.Join(dir, "session-id")
	if b, err := os.ReadFile(path); err == nil {
		if id := strings.TrimSpace(string(b)); id != "" {
			return id, nil
		}
	}
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	id := "gen-" + hex.EncodeToString(buf)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path, []byte(id+"\n"), 0o644); err != nil {
		return "", err
	}
	return id, nil
}

// HostLabel resolves the machine label: PLEXUS_HOST (flag/env/env-file) or,
// as an out-of-Plexus fallback only, the hostname lowercased and truncated at
// the first dot (MacBook-Pro.local -> macbook-pro).
func HostLabel(resolved string) string {
	if resolved != "" {
		return resolved
	}
	hn, err := os.Hostname()
	if err != nil || hn == "" {
		return "unknown"
	}
	hn = strings.ToLower(hn)
	if i := strings.Index(hn, "."); i > 0 {
		hn = hn[:i]
	}
	return hn
}
