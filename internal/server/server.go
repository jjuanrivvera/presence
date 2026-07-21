// Package server implements the presence HTTP service: auth middleware,
// the six API routes plus /healthz and the /ui dashboard, and the auto-prune
// timer.
package server

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/http/httputil"
	"regexp"
	"strings"
	"time"

	"github.com/jjuanrivvera/presence/internal/store"
)

// cookieName holds the bearer token for browser clients, which cannot set an
// Authorization header on link navigations or WebSocket handshakes (both needed
// to reach a session's web terminal through /attach). Set by POST /login.
const cookieName = "presence_auth"

// uiHTML is the live dashboard. It is static markup with no data baked in —
// the page's own JS fetches /list with the bearer token, so serving it
// unauthenticated leaks nothing.
//
//go:embed ui.html
var uiHTML []byte

// PWA assets served alongside /ui, all unauthenticated (they carry no data — the page's JS
// still needs the bearer token to call /list).
//
//go:embed manifest.json
var manifestJSON []byte

//go:embed sw.js
var swJS []byte

//go:embed icon.svg
var iconSVG []byte

const maxBodyBytes = 16 << 10 // 16 KiB

var (
	sessionIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,128}$`)
	hostRe      = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)
)

type Server struct {
	store *store.Store
	token string
	ttl   time.Duration
}

func New(st *store.Store, token string, ttl time.Duration) *Server {
	return &Server{store: st, token: token, ttl: ttl}
}

// Handler returns the routed mux with auth applied to everything but /healthz.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("ok"))
	})
	mux.Handle("/ui", s.method(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(uiHTML)
	}))
	mux.Handle("/manifest.json", s.method(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/manifest+json")
		w.Write(manifestJSON)
	}))
	mux.Handle("/sw.js", s.method(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/javascript")
		w.Write(swJS)
	}))
	mux.Handle("/icon.svg", s.method(http.MethodGet, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Write(iconSVG)
	}))
	// /login is the auth itself (validates the token in the body), so it is unauthenticated.
	mux.Handle("/login", s.method(http.MethodPost, s.handleLogin))
	mux.Handle("/register", s.auth(s.method(http.MethodPost, s.handleRegister)))
	mux.Handle("/heartbeat", s.auth(s.method(http.MethodPost, s.handleHeartbeat)))
	mux.Handle("/deregister", s.auth(s.method(http.MethodPost, s.handleDeregister)))
	mux.Handle("/list", s.auth(s.method(http.MethodGet, s.handleList)))
	mux.Handle("/get", s.auth(s.method(http.MethodGet, s.handleGet)))
	mux.Handle("/prune", s.auth(s.method(http.MethodPost, s.handlePrune)))
	// /attach/<session_id>/* reverse-proxies to that session's ttyd (subtree match).
	// No method wrapper: it must pass GETs, static assets, and the WebSocket upgrade.
	mux.Handle("/attach/", s.auth(s.handleAttach))
	return mux
}

// RunAutoPrune deletes rows older than the TTL every interval, until ctx is done.
func (s *Server) RunAutoPrune(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if n, err := s.store.Prune(s.ttl); err != nil {
				log.Printf("auto-prune error: %v", err)
			} else if n > 0 {
				log.Printf("auto-prune: removed %d stale session(s)", n)
			}
		}
	}
}

// auth accepts the token from either "Authorization: Bearer <token>" (CLI clients)
// or the presence_auth cookie (browsers, which can't set headers on link/WS
// navigations). Compared constant-time, both sides hashed so length never leaks.
func (s *Server) auth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok {
			if c, err := r.Cookie(cookieName); err == nil {
				got, ok = c.Value, true
			}
		}
		gh := sha256.Sum256([]byte(got))
		wh := sha256.Sum256([]byte(s.token))
		if !ok || subtle.ConstantTimeCompare(gh[:], wh[:]) != 1 {
			writeErr(w, http.StatusUnauthorized, "unauthorized")
			return
		}
		next(w, r)
	}
}

// handleLogin validates the token and, on success, sets it as an HttpOnly cookie
// so the browser carries auth on subsequent /list polls and /attach navigations
// without a second prompt. This is the single sign-in for the whole cockpit.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	gh := sha256.Sum256([]byte(req.Token))
	wh := sha256.Sum256([]byte(s.token))
	if subtle.ConstantTimeCompare(gh[:], wh[:]) != 1 {
		writeErr(w, http.StatusUnauthorized, "bad token")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieName,
		Value:    req.Token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   365 * 24 * 3600,
	})
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleAttach reverse-proxies /attach/<session_id>/* to that session's ttyd web
// terminal. It injects ttyd's basic-auth so the user authenticates once (the
// presence login) and never sees ttyd's own prompt. The ttyd runs with base-path
// /attach/<session_id>, so request paths pass through unrewritten (assets + WS).
func (s *Server) handleAttach(w http.ResponseWriter, r *http.Request) {
	rest := strings.TrimPrefix(r.URL.Path, "/attach/")
	sid := rest
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		sid = rest[:i]
	}
	if !sessionIDRe.MatchString(sid) {
		writeErr(w, http.StatusBadRequest, "bad session_id")
		return
	}
	row, err := s.store.GetByID(sid)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	if row == nil || row.AttachAddr == "" {
		writeErr(w, http.StatusNotFound, "no attachable session")
		return
	}
	host := row.AttachAddr
	basic := "Basic " + base64.StdEncoding.EncodeToString([]byte("mesh:"+s.token))
	proxy := &httputil.ReverseProxy{
		FlushInterval: -1, // stream terminal output as it arrives
		Director: func(req *http.Request) {
			req.URL.Scheme = "http"
			req.URL.Host = host
			req.Host = host
			req.Header.Set("Authorization", basic)
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, _ error) {
			writeErr(w, http.StatusBadGateway, "attach upstream unreachable")
		},
	}
	proxy.ServeHTTP(w, r)
}

func (s *Server) method(m string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != m {
			writeErr(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		next(w, r)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"ok": false, "error": msg})
}

func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return false
	}
	return true
}

type registerReq struct {
	SessionID  string `json:"session_id"`
	Host       string `json:"host"`
	Repo       string `json:"repo"`
	RepoPath   string `json:"repo_path"`
	Branch     string `json:"branch"`
	InjectPort int    `json:"inject_port"`
	PID        int    `json:"pid"`
	Agent      string `json:"agent"`       // "claude" (default) | "codex" | future; empty defaults server-side
	AttachAddr string `json:"attach_addr"` // host:port of this session's web terminal (ttyd), or empty
}

func validateRegister(req registerReq) string {
	switch {
	case !sessionIDRe.MatchString(req.SessionID):
		return "session_id: required, 1-128 chars [A-Za-z0-9._-]"
	case !hostRe.MatchString(req.Host):
		return "host: required, lowercase, 1-32 chars [a-z0-9-]"
	case req.InjectPort < 0 || req.InjectPort > 65535:
		return "inject_port: must be 0-65535"
	case req.PID < 0:
		return "pid: must be >= 0"
	case len(req.Repo) > 1024 || len(req.RepoPath) > 1024 || len(req.Branch) > 1024:
		return "repo/repo_path/branch: max 1 KiB each"
	// agent shares host's charset (lowercase [a-z0-9-], 1-32); empty is allowed and
	// defaults to "claude" in the store, so old clients that never send it still validate.
	case req.Agent != "" && !hostRe.MatchString(req.Agent):
		return "agent: lowercase, 1-32 chars [a-z0-9-]"
	}
	return ""
}

func (s *Server) handleRegister(w http.ResponseWriter, r *http.Request) {
	var req registerReq
	if !decodeBody(w, r, &req) {
		return
	}
	if msg := validateRegister(req); msg != "" {
		writeErr(w, http.StatusBadRequest, msg)
		return
	}
	err := s.store.Upsert(store.Session{
		SessionID: req.SessionID, Host: req.Host, Repo: req.Repo,
		RepoPath: req.RepoPath, Branch: req.Branch,
		InjectPort: req.InjectPort, PID: req.PID, Agent: req.Agent,
		AttachAddr: req.AttachAddr,
	})
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
		State     string `json:"state"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !sessionIDRe.MatchString(req.SessionID) {
		writeErr(w, http.StatusBadRequest, "session_id: required, 1-128 chars [A-Za-z0-9._-]")
		return
	}
	if req.State == "" {
		req.State = "busy"
	}
	// "blocked" = the session is waiting on human input (a permission prompt / a question).
	// It is the highest-signal state: it tells the mesh which session needs you right now.
	if req.State != "busy" && req.State != "idle" && req.State != "blocked" {
		writeErr(w, http.StatusBadRequest, "state: must be busy, idle, or blocked")
		return
	}
	found, err := s.store.Heartbeat(req.SessionID, req.State)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	if !found {
		writeErr(w, http.StatusNotFound, "unknown session")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleDeregister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		SessionID string `json:"session_id"`
	}
	if !decodeBody(w, r, &req) {
		return
	}
	if !sessionIDRe.MatchString(req.SessionID) {
		writeErr(w, http.StatusBadRequest, "session_id: required, 1-128 chars [A-Za-z0-9._-]")
		return
	}
	if err := s.store.Delete(req.SessionID); err != nil {
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) parseFresh(w http.ResponseWriter, raw string) (time.Duration, bool) {
	if raw == "" {
		return 120 * time.Second, true
	}
	d, err := time.ParseDuration(raw)
	if err != nil || d <= 0 {
		writeErr(w, http.StatusBadRequest, "fresh: invalid Go duration")
		return 0, false
	}
	return d, true
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	fresh, ok := s.parseFresh(w, q.Get("fresh"))
	if !ok {
		return
	}
	rows, err := s.store.List(q.Get("host"), q.Get("repo"), q.Get("agent"), fresh)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	writeJSON(w, http.StatusOK, rows)
}

func (s *Server) handleGet(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	repo := q.Get("repo")
	if repo == "" {
		writeErr(w, http.StatusBadRequest, "repo: required")
		return
	}
	fresh, ok := s.parseFresh(w, q.Get("fresh"))
	if !ok {
		return
	}
	var hosts []string
	if h := q.Get("host"); h != "" {
		for _, part := range strings.Split(h, ",") {
			if part = strings.TrimSpace(part); part != "" {
				hosts = append(hosts, part)
			}
		}
	}
	row, err := s.store.Get(repo, hosts, q.Get("agent"), fresh)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	if row == nil {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	writeJSON(w, http.StatusOK, row)
}

func (s *Server) handlePrune(w http.ResponseWriter, r *http.Request) {
	var req struct {
		OlderThan string `json:"older_than"`
	}
	// Empty body means "use the server TTL" — prune is callable with no args.
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil && err != io.EOF {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	olderThan := s.ttl
	if req.OlderThan != "" {
		d, err := time.ParseDuration(req.OlderThan)
		if err != nil || d <= 0 {
			writeErr(w, http.StatusBadRequest, "older_than: invalid Go duration")
			return
		}
		olderThan = d
	}
	n, err := s.store.Prune(olderThan)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "store error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "pruned": n})
}
