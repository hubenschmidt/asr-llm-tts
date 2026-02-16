package trace

import "time"

// Session represents one WebSocket connection.
type Session struct {
	ID        string    `json:"id"`
	Metadata  string    `json:"metadata"`
	StartedAt time.Time `json:"started_at"`
	EndedAt   *time.Time `json:"ended_at,omitempty"`
	RunCount  int       `json:"run_count,omitempty"`
}

// Run represents one pipeline execution (one speech segment through ASR→LLM→TTS).
type Run struct {
	ID         string     `json:"id"`
	SessionID  string     `json:"session_id"`
	StartedAt  time.Time  `json:"started_at"`
	DurationMs float64    `json:"duration_ms,omitempty"`
	Transcript string     `json:"transcript,omitempty"`
	Response   string     `json:"response,omitempty"`
	Status     string     `json:"status"`
	SpanCount  int        `json:"span_count,omitempty"`
}

// Span represents an individual pipeline stage execution.
type Span struct {
	ID         string    `json:"id"`
	RunID      string    `json:"run_id"`
	Name       string    `json:"name"`
	StartedAt  time.Time `json:"started_at"`
	DurationMs float64   `json:"duration_ms"`
	Input      string    `json:"input,omitempty"`
	Output     string    `json:"output,omitempty"`
	Status     string    `json:"status"`
	Error      string    `json:"error,omitempty"`
}
