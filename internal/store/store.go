// Package store persists session plexus rows in SQLite (pure Go, no cgo).
package store

import (
	"database/sql"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sessions (
  session_id   TEXT PRIMARY KEY,
  host         TEXT NOT NULL,
  repo         TEXT NOT NULL DEFAULT '',
  repo_path    TEXT NOT NULL DEFAULT '',
  branch       TEXT NOT NULL DEFAULT '',
  inject_port  INTEGER NOT NULL DEFAULT 0,
  pid          INTEGER NOT NULL DEFAULT 0,
  state        TEXT NOT NULL DEFAULT 'idle',
  agent        TEXT NOT NULL DEFAULT 'claude',
  attach_addr  TEXT NOT NULL DEFAULT '',
  started_at   TEXT NOT NULL,
  last_seen    TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_sessions_repo      ON sessions(repo);
CREATE INDEX IF NOT EXISTS idx_sessions_host      ON sessions(host);
CREATE INDEX IF NOT EXISTS idx_sessions_last_seen ON sessions(last_seen);
`

// Session is one plexus row. JSON field names match the column names.
type Session struct {
	SessionID  string `json:"session_id"`
	Host       string `json:"host"`
	Repo       string `json:"repo"`
	RepoPath   string `json:"repo_path"`
	Branch     string `json:"branch"`
	InjectPort int    `json:"inject_port"`
	PID        int    `json:"pid"`
	State      string `json:"state"`
	Agent      string `json:"agent"`
	AttachAddr string `json:"attach_addr"`
	StartedAt  string `json:"started_at"`
	LastSeen   string `json:"last_seen"`
}

type Store struct {
	db *sql.DB
	// now is swappable in tests to simulate stale rows without sleeping.
	now func() time.Time
}

// Open opens (or creates) the SQLite DB with WAL + busy_timeout and the schema applied.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, err
	}
	// Additive migration for DBs created before the agent column existed (the live VPS DB).
	// The column is also in CREATE TABLE for fresh DBs, so this ALTER duplicates on new DBs —
	// that specific error is expected and ignored; anything else is fatal.
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN agent TEXT NOT NULL DEFAULT 'claude'`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, err
		}
	}
	// attach_addr: host:port of this session's web terminal (ttyd), so a UI can deep-link to
	// "attach" and stream/steer it. Empty when the session has no attach endpoint.
	if _, err := db.Exec(`ALTER TABLE sessions ADD COLUMN attach_addr TEXT NOT NULL DEFAULT ''`); err != nil {
		if !strings.Contains(err.Error(), "duplicate column name") {
			db.Close()
			return nil, err
		}
	}
	return &Store{db: db, now: time.Now}, nil
}

func (s *Store) Close() error { return s.db.Close() }

func (s *Store) stamp() string {
	return s.now().UTC().Format(time.RFC3339)
}

// Upsert inserts or updates a session. The server stamps the times: started_at
// only on first insert, last_seen always. State is forced to "busy" (a session
// that just registered is active by definition).
func (s *Store) Upsert(sess Session) error {
	now := s.stamp()
	agent := sess.Agent
	if agent == "" {
		agent = "claude" // registrations from the pre-agent client default to claude
	}
	_, err := s.db.Exec(`
		INSERT INTO sessions (session_id, host, repo, repo_path, branch, inject_port, pid, state, agent, attach_addr, started_at, last_seen)
		VALUES (?, ?, ?, ?, ?, ?, ?, 'busy', ?, ?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
		  host=excluded.host, repo=excluded.repo, repo_path=excluded.repo_path,
		  branch=excluded.branch, inject_port=excluded.inject_port, pid=excluded.pid,
		  state='busy', agent=excluded.agent, attach_addr=excluded.attach_addr, last_seen=excluded.last_seen`,
		sess.SessionID, sess.Host, sess.Repo, sess.RepoPath, sess.Branch,
		sess.InjectPort, sess.PID, agent, sess.AttachAddr, now, now)
	return err
}

// Heartbeat bumps last_seen and state. Returns false if the session does not exist.
func (s *Store) Heartbeat(sessionID, state string) (bool, error) {
	res, err := s.db.Exec(`UPDATE sessions SET last_seen=?, state=? WHERE session_id=?`,
		s.stamp(), state, sessionID)
	if err != nil {
		return false, err
	}
	n, err := res.RowsAffected()
	return n > 0, err
}

// Delete removes a session (idempotent).
func (s *Store) Delete(sessionID string) error {
	_, err := s.db.Exec(`DELETE FROM sessions WHERE session_id=?`, sessionID)
	return err
}

const sessionCols = `session_id, host, repo, repo_path, branch, inject_port, pid, state, agent, attach_addr, started_at, last_seen`

func scanSession(sc interface{ Scan(...any) error }) (Session, error) {
	var r Session
	err := sc.Scan(&r.SessionID, &r.Host, &r.Repo, &r.RepoPath, &r.Branch,
		&r.InjectPort, &r.PID, &r.State, &r.Agent, &r.AttachAddr, &r.StartedAt, &r.LastSeen)
	return r, err
}

// List returns rows with last_seen within the fresh window, optionally
// filtered by exact host and repo.
func (s *Store) List(host, repo, agent string, fresh time.Duration) ([]Session, error) {
	cutoff := s.now().UTC().Add(-fresh).Format(time.RFC3339)
	q := `SELECT ` + sessionCols + ` FROM sessions WHERE last_seen >= ?`
	args := []any{cutoff}
	if host != "" {
		q += ` AND host = ?`
		args = append(args, host)
	}
	if repo != "" {
		q += ` AND repo = ?`
		args = append(args, repo)
	}
	if agent != "" {
		q += ` AND agent = ?`
		args = append(args, agent)
	}
	q += ` ORDER BY last_seen DESC, session_id ASC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		r, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Get is the delegation query: the freshest injectable (inject_port > 0)
// session matching repo (required) and any of hosts (optional, OR). Tie-break
// is last_seen DESC then session_id ASC, so results are deterministic.
// Returns (nil, nil) when there is no match.
func (s *Store) Get(repo string, hosts []string, agent string, fresh time.Duration) (*Session, error) {
	cutoff := s.now().UTC().Add(-fresh).Format(time.RFC3339)
	q := `SELECT ` + sessionCols + ` FROM sessions
	      WHERE repo = ? AND inject_port > 0 AND last_seen >= ?`
	args := []any{repo, cutoff}
	if len(hosts) > 0 {
		q += ` AND host IN (?` + strings.Repeat(",?", len(hosts)-1) + `)`
		for _, h := range hosts {
			args = append(args, h)
		}
	}
	if agent != "" {
		q += ` AND agent = ?`
		args = append(args, agent)
	}
	q += ` ORDER BY last_seen DESC, session_id ASC LIMIT 1`
	row := s.db.QueryRow(q, args...)
	r, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// GetByID returns the session with the given id, or (nil, nil) if absent.
// Used by the attach reverse-proxy to resolve a session's web-terminal address.
func (s *Store) GetByID(sessionID string) (*Session, error) {
	row := s.db.QueryRow(`SELECT `+sessionCols+` FROM sessions WHERE session_id = ?`, sessionID)
	r, err := scanSession(row)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &r, nil
}

// Prune deletes rows whose last_seen is older than olderThan; returns the count.
func (s *Store) Prune(olderThan time.Duration) (int64, error) {
	cutoff := s.now().UTC().Add(-olderThan).Format(time.RFC3339)
	res, err := s.db.Exec(`DELETE FROM sessions WHERE last_seen < ?`, cutoff)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}
