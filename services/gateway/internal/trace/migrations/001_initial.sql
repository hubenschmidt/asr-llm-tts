CREATE TABLE IF NOT EXISTS sessions (
    id         TEXT PRIMARY KEY,
    metadata   TEXT NOT NULL DEFAULT '{}',
    started_at TIMESTAMPTZ NOT NULL,
    ended_at   TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS runs (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    started_at  TIMESTAMPTZ NOT NULL,
    duration_ms DOUBLE PRECISION DEFAULT 0,
    transcript  TEXT DEFAULT '',
    response    TEXT DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'running'
);

CREATE TABLE IF NOT EXISTS spans (
    id          TEXT PRIMARY KEY,
    run_id      TEXT NOT NULL REFERENCES runs(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    started_at  TIMESTAMPTZ NOT NULL,
    duration_ms DOUBLE PRECISION NOT NULL,
    input       TEXT DEFAULT '',
    output      TEXT DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'ok',
    error_msg   TEXT DEFAULT ''
);

CREATE INDEX IF NOT EXISTS idx_runs_session ON runs(session_id);
CREATE INDEX IF NOT EXISTS idx_spans_run ON spans(run_id);
