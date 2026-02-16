package trace

import (
	"database/sql"
	"embed"
	"fmt"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // registers "pgx" driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

const maxSessions = 100

// Store persists trace data to PostgreSQL.
type Store struct {
	db *sql.DB
}

// Open connects to a PostgreSQL trace database at connStr.
func Open(connStr string) (*Store, error) {
	db, err := sql.Open("pgx", connStr)
	if err != nil {
		return nil, fmt.Errorf("trace open: %w", err)
	}
	if err = db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("trace ping: %w", err)
	}
	if err = migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("trace migrate: %w", err)
	}
	return &Store{db: db}, nil
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

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("read migrations dir: %w", err)
	}

	for i := current + 1; i < len(entries); i++ {
		data, readErr := migrationFS.ReadFile("migrations/" + entries[i].Name())
		if readErr != nil {
			return fmt.Errorf("read migration %d: %w", i, readErr)
		}
		if _, execErr := db.Exec(string(data)); execErr != nil {
			return fmt.Errorf("migration %d: %w", i, execErr)
		}
		if _, execErr := db.Exec(`INSERT INTO schema_version (version) VALUES ($1)`, i); execErr != nil {
			return fmt.Errorf("migration %d record: %w", i, execErr)
		}
	}
	return nil
}

// Close closes the database.
func (s *Store) Close() error {
	return s.db.Close()
}

// CreateSession inserts a new session and prunes old ones.
func (s *Store) CreateSession(id, metadata string) error {
	_, err := s.db.Exec(
		`INSERT INTO sessions (id, metadata, started_at) VALUES ($1, $2, $3)`,
		id, metadata, time.Now().UTC(),
	)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(
		`DELETE FROM sessions WHERE id NOT IN (SELECT id FROM sessions ORDER BY started_at DESC LIMIT $1)`,
		maxSessions,
	)
	return err
}

// EndSession sets the ended_at timestamp.
func (s *Store) EndSession(id string) error {
	_, err := s.db.Exec(
		`UPDATE sessions SET ended_at = $1 WHERE id = $2`,
		time.Now().UTC(), id,
	)
	return err
}

// CreateRun inserts a new run.
func (s *Store) CreateRun(id, sessionID string) error {
	_, err := s.db.Exec(
		`INSERT INTO runs (id, session_id, started_at, status) VALUES ($1, $2, $3, 'running')`,
		id, sessionID, time.Now().UTC(),
	)
	return err
}

// UpdateRun sets the run's final fields.
func (s *Store) UpdateRun(id string, durationMs float64, transcript, response, status string) error {
	_, err := s.db.Exec(
		`UPDATE runs SET duration_ms = $1, transcript = $2, response = $3, status = $4 WHERE id = $5`,
		durationMs, transcript, response, status, id,
	)
	return err
}

// CreateSpan inserts a span.
func (s *Store) CreateSpan(sp Span) error {
	_, err := s.db.Exec(
		`INSERT INTO spans (id, run_id, name, started_at, duration_ms, input, output, status, error_msg)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		sp.ID, sp.RunID, sp.Name, sp.StartedAt.UTC(),
		sp.DurationMs, sp.Input, sp.Output, sp.Status, sp.Error,
	)
	return err
}

// ListSessions returns sessions ordered newest first, with run counts.
func (s *Store) ListSessions(limit, offset int) ([]Session, int, error) {
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
		LIMIT $1 OFFSET $2
	`, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var endedAt sql.NullTime
		if err = rows.Scan(&sess.ID, &sess.Metadata, &sess.StartedAt, &endedAt, &sess.RunCount); err != nil {
			return nil, 0, err
		}
		if endedAt.Valid {
			sess.EndedAt = &endedAt.Time
		}
		sessions = append(sessions, sess)
	}
	return sessions, total, rows.Err()
}

// GetSession returns a single session with its runs.
func (s *Store) GetSession(id string) (*Session, []Run, error) {
	var sess Session
	var endedAt sql.NullTime
	err := s.db.QueryRow(
		`SELECT id, metadata, started_at, ended_at FROM sessions WHERE id = $1`, id,
	).Scan(&sess.ID, &sess.Metadata, &sess.StartedAt, &endedAt)
	if err != nil {
		return nil, nil, err
	}
	if endedAt.Valid {
		sess.EndedAt = &endedAt.Time
	}

	rows, err := s.db.Query(`
		SELECT r.id, r.session_id, r.started_at, r.duration_ms, r.transcript, r.response, r.status,
		       COUNT(sp.id) as span_count
		FROM runs r
		LEFT JOIN spans sp ON sp.run_id = r.id
		WHERE r.session_id = $1
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
		if err = rows.Scan(&r.ID, &r.SessionID, &r.StartedAt, &r.DurationMs, &r.Transcript, &r.Response, &r.Status, &r.SpanCount); err != nil {
			return nil, nil, err
		}
		runs = append(runs, r)
	}
	return &sess, runs, rows.Err()
}

// GetRun returns a single run with its spans.
func (s *Store) GetRun(sessionID, runID string) (*Run, []Span, error) {
	var r Run
	err := s.db.QueryRow(
		`SELECT id, session_id, started_at, duration_ms, transcript, response, status FROM runs WHERE id = $1 AND session_id = $2`,
		runID, sessionID,
	).Scan(&r.ID, &r.SessionID, &r.StartedAt, &r.DurationMs, &r.Transcript, &r.Response, &r.Status)
	if err != nil {
		return nil, nil, err
	}

	rows, err := s.db.Query(
		`SELECT id, run_id, name, started_at, duration_ms, input, output, status, error_msg FROM spans WHERE run_id = $1 ORDER BY started_at ASC`,
		runID,
	)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()

	var spans []Span
	for rows.Next() {
		var sp Span
		if err = rows.Scan(&sp.ID, &sp.RunID, &sp.Name, &sp.StartedAt, &sp.DurationMs, &sp.Input, &sp.Output, &sp.Status, &sp.Error); err != nil {
			return nil, nil, err
		}
		spans = append(spans, sp)
	}
	return &r, spans, rows.Err()
}
