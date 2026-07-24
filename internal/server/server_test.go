package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jjuanrivvera/plexus/internal/store"
)

const testToken = "test-token"

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "plexus.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ts := httptest.NewServer(New(st, testToken, 5*time.Minute).Handler())
	t.Cleanup(ts.Close)
	return ts
}

func doReq(t *testing.T, ts *httptest.Server, method, path, authHeader, body string) *http.Response {
	t.Helper()
	req, err := http.NewRequest(method, ts.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("do request: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	return resp
}

func TestAuthMiddleware(t *testing.T) {
	tests := []struct {
		name       string
		method     string
		path       string
		authHeader string
		wantStatus int
	}{
		{"no token on POST route", http.MethodPost, "/register", "", http.StatusUnauthorized},
		{"no token on GET route", http.MethodGet, "/list", "", http.StatusUnauthorized},
		{"wrong token", http.MethodGet, "/list", "Bearer wrong-token", http.StatusUnauthorized},
		{"wrong scheme", http.MethodGet, "/list", "Basic " + testToken, http.StatusUnauthorized},
		{"token as prefix of real token", http.MethodGet, "/list", "Bearer test-tok", http.StatusUnauthorized},
		{"real token with suffix", http.MethodGet, "/list", "Bearer test-token-extra", http.StatusUnauthorized},
		{"valid token", http.MethodGet, "/list", "Bearer " + testToken, http.StatusOK},
		{"healthz needs no auth", http.MethodGet, "/healthz", "", http.StatusOK},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			resp := doReq(t, ts, tt.method, tt.path, tt.authHeader, "")
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
			if tt.wantStatus == http.StatusUnauthorized {
				var body struct {
					OK    bool   `json:"ok"`
					Error string `json:"error"`
				}
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatalf("401 body not JSON: %v", err)
				}
				if body.OK || body.Error == "" {
					t.Errorf("401 body = %+v, want ok:false with error", body)
				}
			}
		})
	}
}

func TestUIServedWithoutAuth(t *testing.T) {
	ts := newTestServer(t)

	resp := doReq(t, ts, http.MethodGet, "/ui", "", "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET /ui without auth: status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !strings.Contains(string(body), "plexus") {
		t.Errorf("body does not contain %q", "plexus")
	}

	resp = doReq(t, ts, http.MethodPost, "/ui", "", "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("POST /ui status = %d, want 405", resp.StatusCode)
	}
}

func TestRegisterValidation(t *testing.T) {
	auth := "Bearer " + testToken
	tests := []struct {
		name       string
		body       string
		wantStatus int
	}{
		{"valid minimal", `{"session_id":"abc-123","host":"mac"}`, http.StatusOK},
		{"valid full", `{"session_id":"abc-123","host":"mac","repo":"myrepo","repo_path":"/x","branch":"main","inject_port":8801,"pid":42}`, http.StatusOK},
		{"missing session_id", `{"host":"mac"}`, http.StatusBadRequest},
		{"session_id bad chars", `{"session_id":"a b","host":"mac"}`, http.StatusBadRequest},
		{"session_id too long", `{"session_id":"` + strings.Repeat("a", 129) + `","host":"mac"}`, http.StatusBadRequest},
		{"missing host", `{"session_id":"abc"}`, http.StatusBadRequest},
		{"host uppercase rejected", `{"session_id":"abc","host":"Mac"}`, http.StatusBadRequest},
		{"host not in mac/pc/vps still ok", `{"session_id":"abc","host":"laptop-2"}`, http.StatusOK},
		{"inject_port negative", `{"session_id":"abc","host":"mac","inject_port":-1}`, http.StatusBadRequest},
		{"inject_port too big", `{"session_id":"abc","host":"mac","inject_port":70000}`, http.StatusBadRequest},
		{"pid negative", `{"session_id":"abc","host":"mac","pid":-1}`, http.StatusBadRequest},
		{"repo over 1KiB", `{"session_id":"abc","host":"mac","repo":"` + strings.Repeat("r", 1025) + `"}`, http.StatusBadRequest},
		{"not JSON", `nope`, http.StatusBadRequest},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := newTestServer(t)
			resp := doReq(t, ts, http.MethodPost, "/register", auth, tt.body)
			if resp.StatusCode != tt.wantStatus {
				t.Errorf("status = %d, want %d", resp.StatusCode, tt.wantStatus)
			}
		})
	}
}

func TestHeartbeatUnknownSession404(t *testing.T) {
	ts := newTestServer(t)
	auth := "Bearer " + testToken
	resp := doReq(t, ts, http.MethodPost, "/heartbeat", auth, `{"session_id":"ghost"}`)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestGetDelegationFlow(t *testing.T) {
	ts := newTestServer(t)
	auth := "Bearer " + testToken

	// No sessions yet: 204.
	resp := doReq(t, ts, http.MethodGet, "/get?repo=myrepo", auth, "")
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("empty get status = %d, want 204", resp.StatusCode)
	}

	// Register an injectable session; /get must return it.
	resp = doReq(t, ts, http.MethodPost, "/register", auth,
		`{"session_id":"abc-123","host":"mac","repo":"myrepo","inject_port":8801}`)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("register status = %d, want 200", resp.StatusCode)
	}
	resp = doReq(t, ts, http.MethodGet, "/get?repo=myrepo&host=mac,pc", auth, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status = %d, want 200", resp.StatusCode)
	}
	var row store.Session
	if err := json.NewDecoder(resp.Body).Decode(&row); err != nil {
		t.Fatalf("decode row: %v", err)
	}
	if row.SessionID != "abc-123" || row.InjectPort != 8801 {
		t.Errorf("row = %+v", row)
	}

	// Wrong method on a POST route.
	resp = doReq(t, ts, http.MethodGet, "/register", auth, "")
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("GET /register status = %d, want 405", resp.StatusCode)
	}
}

func TestBodyTooLargeRejected(t *testing.T) {
	ts := newTestServer(t)
	auth := "Bearer " + testToken
	big := `{"session_id":"abc","host":"mac","repo":"` + strings.Repeat("x", 20<<10) + `"}`
	resp := doReq(t, ts, http.MethodPost, "/register", auth, big)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}
