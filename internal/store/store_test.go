package store

import (
	"path/filepath"
	"testing"
	"time"
)

func openTestStore(t *testing.T) *Store {
	t.Helper()
	st, err := Open(filepath.Join(t.TempDir(), "plexus.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	return st
}

// setNow pins the store clock so tests can simulate stale rows without sleeping.
func setNow(st *Store, at time.Time) { st.now = func() time.Time { return at } }

func baseSession(id string) Session {
	return Session{
		SessionID: id, Host: "mac", Repo: "myrepo",
		RepoPath: "/path/to/myrepo", Branch: "main",
		InjectPort: 8801, PID: 4242,
	}
}

func TestUpsert(t *testing.T) {
	t0 := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		setup      func(t *testing.T, st *Store)
		want       Session
		wantStart  string // expected started_at (RFC3339)
		wantSeen   string // expected last_seen
		wantsState string
	}{
		{
			name:      "insert new row stamps started_at and last_seen",
			setup:     func(t *testing.T, st *Store) {},
			wantStart: "2026-07-07T12:00:00Z",
			wantSeen:  "2026-07-07T12:00:00Z",
		},
		{
			name: "upsert existing row keeps started_at, bumps last_seen",
			setup: func(t *testing.T, st *Store) {
				setNow(st, t0.Add(-10*time.Minute))
				if err := st.Upsert(baseSession("s1")); err != nil {
					t.Fatalf("first upsert: %v", err)
				}
			},
			wantStart: "2026-07-07T11:50:00Z",
			wantSeen:  "2026-07-07T12:00:00Z",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openTestStore(t)
			tt.setup(t, st)
			setNow(st, t0)
			if err := st.Upsert(baseSession("s1")); err != nil {
				t.Fatalf("Upsert: %v", err)
			}
			rows, err := st.List("", "", "", time.Hour)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(rows) != 1 {
				t.Fatalf("want 1 row, got %d", len(rows))
			}
			r := rows[0]
			if r.StartedAt != tt.wantStart {
				t.Errorf("started_at = %q, want %q", r.StartedAt, tt.wantStart)
			}
			if r.LastSeen != tt.wantSeen {
				t.Errorf("last_seen = %q, want %q", r.LastSeen, tt.wantSeen)
			}
			if r.State != "busy" {
				t.Errorf("state = %q, want busy", r.State)
			}
			if r.Repo != "myrepo" || r.Host != "mac" || r.InjectPort != 8801 {
				t.Errorf("row fields mismatch: %+v", r)
			}
		})
	}
}

func TestHeartbeat(t *testing.T) {
	tests := []struct {
		name      string
		exists    bool
		state     string
		wantFound bool
	}{
		{"existing session busy", true, "busy", true},
		{"existing session idle", true, "idle", true},
		{"unknown session", false, "busy", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openTestStore(t)
			t0 := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
			if tt.exists {
				setNow(st, t0)
				if err := st.Upsert(baseSession("s1")); err != nil {
					t.Fatalf("Upsert: %v", err)
				}
			}
			setNow(st, t0.Add(30*time.Second))
			found, err := st.Heartbeat("s1", tt.state)
			if err != nil {
				t.Fatalf("Heartbeat: %v", err)
			}
			if found != tt.wantFound {
				t.Fatalf("found = %v, want %v", found, tt.wantFound)
			}
			if tt.wantFound {
				rows, _ := st.List("", "", "", time.Hour)
				if rows[0].LastSeen != "2026-07-07T12:00:30Z" {
					t.Errorf("last_seen not bumped: %q", rows[0].LastSeen)
				}
				if rows[0].State != tt.state {
					t.Errorf("state = %q, want %q", rows[0].State, tt.state)
				}
			}
		})
	}
}

func TestGet(t *testing.T) {
	t0 := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	// Fixture: rows registered at different ages / hosts / ports.
	seed := []struct {
		sess Session
		age  time.Duration
	}{
		{Session{SessionID: "old-mac", Host: "mac", Repo: "myrepo", InjectPort: 8801}, 10 * time.Minute},
		{Session{SessionID: "fresh-mac", Host: "mac", Repo: "myrepo", InjectPort: 8802}, 30 * time.Second},
		{Session{SessionID: "fresh-pc", Host: "pc", Repo: "myrepo", InjectPort: 8803}, 10 * time.Second},
		{Session{SessionID: "no-port", Host: "mac", Repo: "myrepo", InjectPort: 0}, 5 * time.Second},
		{Session{SessionID: "fresh-vps", Host: "vps", Repo: "myrepo", InjectPort: 8804}, 5 * time.Second},
		{Session{SessionID: "other-repo", Host: "mac", Repo: "acue-api", InjectPort: 8805}, 5 * time.Second},
	}

	tests := []struct {
		name  string
		repo  string
		hosts []string
		fresh time.Duration
		want  string // expected session_id; "" = no match
	}{
		{"freshest injectable wins", "myrepo", nil, 2 * time.Minute, "fresh-vps"},
		{"host CSV OR filter excludes vps", "myrepo", []string{"mac", "pc"}, 2 * time.Minute, "fresh-pc"},
		{"single host filter", "myrepo", []string{"mac"}, 2 * time.Minute, "fresh-mac"},
		{"stale rows excluded by fresh window", "myrepo", []string{"mac"}, 15 * time.Second, ""},
		{"inject_port=0 rows never match", "myrepo", []string{"mac"}, 7 * time.Second, ""},
		{"unknown repo no match", "nope", nil, 2 * time.Minute, ""},
		{"other repo matches its own row", "acue-api", nil, 2 * time.Minute, "other-repo"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openTestStore(t)
			for _, s := range seed {
				setNow(st, t0.Add(-s.age))
				if err := st.Upsert(s.sess); err != nil {
					t.Fatalf("seed %s: %v", s.sess.SessionID, err)
				}
			}
			setNow(st, t0)
			got, err := st.Get(tt.repo, tt.hosts, "", tt.fresh)
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if tt.want == "" {
				if got != nil {
					t.Fatalf("want no match, got %+v", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("want %q, got no match", tt.want)
			}
			if got.SessionID != tt.want {
				t.Errorf("got %q, want %q", got.SessionID, tt.want)
			}
		})
	}
}

func TestGetTieBreakDeterministic(t *testing.T) {
	// Two rows with identical last_seen: session_id ASC must win.
	st := openTestStore(t)
	t0 := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	setNow(st, t0)
	for _, id := range []string{"bbb", "aaa"} {
		if err := st.Upsert(Session{SessionID: id, Host: "mac", Repo: "myrepo", InjectPort: 8801}); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}
	got, err := st.Get("myrepo", nil, "", time.Minute)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got == nil || got.SessionID != "aaa" {
		t.Fatalf("tie-break: got %+v, want session aaa", got)
	}
}

func TestPrune(t *testing.T) {
	t0 := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name       string
		ages       map[string]time.Duration
		olderThan  time.Duration
		wantPruned int64
		wantLeft   int
	}{
		{
			name:       "crashed session (stale) is swept, fresh survives",
			ages:       map[string]time.Duration{"stale": 10 * time.Minute, "fresh": 10 * time.Second},
			olderThan:  5 * time.Minute,
			wantPruned: 1,
			wantLeft:   1,
		},
		{
			name:       "nothing stale prunes nothing",
			ages:       map[string]time.Duration{"a": time.Second, "b": 2 * time.Second},
			olderThan:  5 * time.Minute,
			wantPruned: 0,
			wantLeft:   2,
		},
		{
			name:       "everything stale prunes all",
			ages:       map[string]time.Duration{"a": time.Hour, "b": 2 * time.Hour},
			olderThan:  5 * time.Minute,
			wantPruned: 2,
			wantLeft:   0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := openTestStore(t)
			for id, age := range tt.ages {
				setNow(st, t0.Add(-age))
				if err := st.Upsert(Session{SessionID: id, Host: "mac"}); err != nil {
					t.Fatalf("Upsert: %v", err)
				}
			}
			setNow(st, t0)
			n, err := st.Prune(tt.olderThan)
			if err != nil {
				t.Fatalf("Prune: %v", err)
			}
			if n != tt.wantPruned {
				t.Errorf("pruned = %d, want %d", n, tt.wantPruned)
			}
			rows, err := st.List("", "", "", 24*time.Hour)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(rows) != tt.wantLeft {
				t.Errorf("rows left = %d, want %d", len(rows), tt.wantLeft)
			}
		})
	}
}

func TestListFilters(t *testing.T) {
	t0 := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	st := openTestStore(t)
	seed := []Session{
		{SessionID: "m1", Host: "mac", Repo: "myrepo"},
		{SessionID: "m2", Host: "mac", Repo: "acue-api"},
		{SessionID: "p1", Host: "pc", Repo: "myrepo"},
	}
	setNow(st, t0)
	for _, s := range seed {
		if err := st.Upsert(s); err != nil {
			t.Fatalf("Upsert: %v", err)
		}
	}

	tests := []struct {
		name string
		host string
		repo string
		want int
	}{
		{"no filters", "", "", 3},
		{"host filter", "mac", "", 2},
		{"repo filter", "", "myrepo", 2},
		{"both filters", "mac", "myrepo", 1},
		{"no matches", "vps", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows, err := st.List(tt.host, tt.repo, "", time.Minute)
			if err != nil {
				t.Fatalf("List: %v", err)
			}
			if len(rows) != tt.want {
				t.Errorf("got %d rows, want %d", len(rows), tt.want)
			}
		})
	}
}

func agentSession(id, agent string) Session {
	s := baseSession(id)
	s.Agent = agent
	return s
}

func mustUpsert(t *testing.T, st *Store, s Session) {
	t.Helper()
	if err := st.Upsert(s); err != nil {
		t.Fatalf("Upsert(%s): %v", s.SessionID, err)
	}
}

func sessionIDs(rows []Session) map[string]bool {
	m := map[string]bool{}
	for _, r := range rows {
		m[r.SessionID] = true
	}
	return m
}

func TestUpsertAgentDefault(t *testing.T) {
	st := openTestStore(t)
	mustUpsert(t, st, baseSession("s-empty"))           // no agent set
	mustUpsert(t, st, agentSession("s-codex", "codex")) // explicit
	rows, err := st.List("", "", "", time.Hour)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	got := map[string]string{}
	for _, r := range rows {
		got[r.SessionID] = r.Agent
	}
	if got["s-empty"] != "claude" {
		t.Errorf("empty agent = %q, want claude (server default)", got["s-empty"])
	}
	if got["s-codex"] != "codex" {
		t.Errorf("explicit agent = %q, want codex", got["s-codex"])
	}
}

func TestListAgentFilter(t *testing.T) {
	st := openTestStore(t)
	mustUpsert(t, st, agentSession("claude-1", "claude"))
	mustUpsert(t, st, agentSession("codex-1", "codex"))

	for _, tt := range []struct {
		agent string
		want  []string
	}{
		{"", []string{"claude-1", "codex-1"}},
		{"codex", []string{"codex-1"}},
		{"claude", []string{"claude-1"}},
		{"nope", nil},
	} {
		rows, err := st.List("", "", tt.agent, time.Hour)
		if err != nil {
			t.Fatalf("List(agent=%q): %v", tt.agent, err)
		}
		got := sessionIDs(rows)
		if len(got) != len(tt.want) {
			t.Errorf("List(agent=%q): got %d rows, want %d", tt.agent, len(got), len(tt.want))
		}
		for _, id := range tt.want {
			if !got[id] {
				t.Errorf("List(agent=%q): missing %q", tt.agent, id)
			}
		}
	}
}

func TestGetAgentFilter(t *testing.T) {
	st := openTestStore(t)
	t0 := time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)
	// Both injectable on the same repo (baseSession repo/port); codex registers later, so it is
	// the freshest match when no agent filter is applied.
	setNow(st, t0.Add(-time.Minute))
	mustUpsert(t, st, agentSession("claude-1", "claude"))
	setNow(st, t0)
	mustUpsert(t, st, agentSession("codex-1", "codex"))

	for _, c := range []struct {
		agent string
		want  string // "" = expect no match
	}{
		{"claude", "claude-1"},
		{"codex", "codex-1"},
		{"", "codex-1"}, // no filter → freshest injectable
		{"nope", ""},
	} {
		got, err := st.Get("myrepo", nil, c.agent, time.Hour)
		if err != nil {
			t.Fatalf("Get(agent=%q): %v", c.agent, err)
		}
		if c.want == "" {
			if got != nil {
				t.Errorf("Get(agent=%q) = %q, want no match", c.agent, got.SessionID)
			}
			continue
		}
		if got == nil {
			t.Fatalf("Get(agent=%q) = nil, want %q", c.agent, c.want)
		}
		if got.SessionID != c.want {
			t.Errorf("Get(agent=%q) = %q, want %q", c.agent, got.SessionID, c.want)
		}
	}
}
