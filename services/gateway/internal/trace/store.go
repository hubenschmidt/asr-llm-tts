package trace

import (
	"database/sql"
	"fmt"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

// migrations is an ordered list of SQL statements. Each entry's index is its
// version number (0-based). New migrations are appended — never reorder or edit
// existing entries.
var migrations = []string{
	// 0: initial schema
	`CREATE TABLE IF NOT EXISTS sessions (
		id         TEXT PRIMARY KEY,
		metadata   TEXT NOT NULL DEFAULT '{}',
		started_at TEXT NOT NULL,
		ended_at   TEXT
	);
	CREATE TABLE IF NOT EXISTS runs (
		id          TEXT PRIMARY KEY,
		session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
		started_at  TEXT NOT NULL,
		duration_ms REAL DEFAULT 0,
		transcript  TEXT DEFAULT '',
		response    TEXT DEFAULT '',
		status      TEXT NOT NULL DEFAULT 'running'
	);
	CREATE TABLE IF NOT EXISTS spans (
		id          TEXT PRIMARY KEY,
		run_id      TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
		name        TEXT NOT NULL,
		started_at  TEXT NOT NULL,
		duration_ms REAL NOT NULL,
		input       TEXT DEFAULT '',
		output      TEXT DEFAULT '',
		status      TEXT NOT NULL DEFAULT 'ok',
		error_msg   TEXT DEFAULT ''
	);
	CREATE INDEX IF NOT EXISTS idx_runs_session ON runs(session_id);
	CREATE INDEX IF NOT EXISTS idx_spans_run ON spans(run_id);`,
}

const maxSessions = 100

// SQLiteStore persists trace data to SQLite.
type SQLiteStore struct {
	db *sql.DB
}

// Open creates or opens a SQLite trace database at path.
func Open(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite3", path+"?_journal=WAL&_fk=1")
	if err != nil {
		return nil, fmt.Errorf("trace open: %w", err)
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("trace migrate: %w", err)
	}
	return &SQLiteStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`)
	if err != nil {
		return err
	}

	var current int
	row := db.QueryRow(`SELECT COALESCE(MAX(version), -1) FROM schema_version`)
	if err = row.Scan(&current); err != nil {
		return err
	}

	for i := current + 1; i < len(migrations); i++ {
		if _, err = db.Exec(migrations[i]); err != nil {
			return fmt.Errorf("migration %d: %w", i, err)
		}
		if _, err = db.Exec(`INSERT INTO schema_version (version) VALUES (?)`, i); err != nil {
			return fmt.Errorf("migration %d record: %w", i, err)
		}
	}
	return nil
}

// Close closes the database.
func (s *SQLiteStore) Close() error {
	return s.db.Close()
}

// CreateSession inserts a new session and prunes old ones.
func (s *SQLiteStore) CreateSession(id, metadata string) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, metadata, started_at) VALUES (?, ?, ?)`,
		id, metadata, time.Now().UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`DELETE FROM sessions WHERE id NOT IN (SELECT id FROM sessions ORDER BY started_at DESC LIMIT ?)`,
		maxSessions,
	)
	return err
}

// EndSession sets the ended_at timestamp.
func (s *SQLiteStore) EndSession(id string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET ended_at = ? WHERE id = ?`,
		time.Now().UTC().Format(time.RFC3339Nano), id,
	)
	return err
}

// CreateRun inserts a new run.
func (s *SQLiteStore) CreateRun(id, sessionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO runs (id, session_id, started_at, status) VALUES (?, ?, ?, 'running')`,
		id, sessionID, time.Now().UTC().Format(time.RFC3339Nano),
	)
	return err
}

// UpdateRun sets the run's final fields.
func (s *SQLiteStore) UpdateRun(id string, durationMs float64, transcript, response, status string) error {
	_, err := s.db.Exec(
		`UPDATE runs SET duration_ms = ?, transcript = ?, response = ?, status = ? WHERE id = ?`,
		durationMs, transcript, response, status, id,
	)
	return err
}

// CreateSpan inserts a span.
func (s *SQLiteStore) CreateSpan(sp Span) error {
	_, err := s.db.Exec(
		`INSERT INTO spans (id, run_id, name, started_at, duration_ms, input, output, status, error_msg)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		sp.ID, sp.RunID, sp.Name, sp.StartedAt.UTC().Format(time.RFC3339Nano),
		sp.DurationMs, sp.Input, sp.Output, sp.Status, sp.Error,
	)
	return err
}

// ListSessions returns sessions ordered newest first, with run counts.
func (s *SQLiteStore) ListSessions(limit, offset int) ([]Session, int, error) {
	var total int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM sessions`).Scan(&total); err != nil {
		return nil, 0, err
	}

	rows, err := s.db.Query(`
		SELECT s.id, s.metadata, s.started_at, s.ended_at, COUNT(r.id) as run_count
		FROM sessions s
		LEFT JOIN runs r ON r.session_id = s.id
		GROUP BY s.id
		ORDER BY s.started_at DESC
		LIMIT ? OFFSET ?
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var startStr string
		var endStr sql.NullString
		if err = rows.Scan(&sess.ID, &sess.Metadata, &startStr, &endStr, &sess.RunCount); err != nil {
			return nil, 0, err
		}
		sess.StartedAt, _ = time.Parse(time.RFC3339Nano, startStr)
		if endStr.Valid {
			t, _ := time.Parse(time.RFC3339Nano, endStr.String)
			sess.EndedAt = &t
		}
		sessions = append(sessions, sess)
	}
	return sessions, total, rows.Err()
}

// GetSession returns a single session with its runs.
func (s *SQLiteStore) GetSession(id string) (*Session, []Run, error) {
	var sess Session
	var startStr string
	var endStr sql.NullString
	err := s.db.QueryRow(
		`SELECT id, metadata, started_at, ended_at FROM sessions WHERE id = ?`, id,
	).Scan(&sess.ID, &sess.Metadata, &startStr, &endStr)
	if err != nil {
		return nil, nil, err
	}
	sess.StartedAt, _ = time.Parse(time.RFC3339Nano, startStr)
	if endStr.Valid {
		t, _ := time.Parse(time.RFC3339Nano, endStr.String)
		sess.EndedAt = &t
	}

	rows, err := s.db.Query(`
		SELECT r.id, r.session_id, r.started_at, r.duration_ms, r.transcript, r.response, r.status,
		       COUNT(sp.id) as span_count
		FROM runs r
		LEFT JOIN spans sp ON sp.run_id = r.id
		WHERE r.session_id = ?
		GROUP BY r.id
		ORDER BY r.started_at ASC
	`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var runs []Run
	for rows.Next() {
		var r Run
		var rStart string
		if err = rows.Scan(&r.ID, &r.SessionID, &rStart, &r.DurationMs, &r.Transcript, &r.Response, &r.Status, &r.SpanCount); err != nil {
			return nil, nil, err
		}
		r.StartedAt, _ = time.Parse(time.RFC3339Nano, rStart)
		runs = append(runs, r)
	}
	return &sess, runs, rows.Err()
}

// GetRun returns a single run with its spans.
func (s *SQLiteStore) GetRun(sessionID, runID string) (*Run, []Span, error) {
	var r Run
	var rStart string
	err := s.db.QueryRow(
		`SELECT id, session_id, started_at, duration_ms, transcript, response, status FROM runs WHERE id = ? AND session_id = ?`,
		runID, sessionID,
	).Scan(&r.ID, &r.SessionID, &rStart, &r.DurationMs, &r.Transcript, &r.Response, &r.Status)
	if err != nil {
		return nil, nil, err
	}
	r.StartedAt, _ = time.Parse(time.RFC3339Nano, rStart)

	rows, err := s.db.Query(
		`SELECT id, run_id, name, started_at, duration_ms, input, output, status, error_msg FROM spans WHERE run_id = ? ORDER BY started_at ASC`,
		runID,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var spans []Span
	for rows.Next() {
		var sp Span
		var spStart string
		if err = rows.Scan(&sp.ID, &sp.RunID, &sp.Name, &spStart, &sp.DurationMs, &sp.Input, &sp.Output, &sp.Status, &sp.Error); err != nil {
			return nil, nil, err
		}
		sp.StartedAt, _ = time.Parse(time.RFC3339Nano, spStart)
		spans = append(spans, sp)
	}
	return &r, spans, rows.Err()
}
