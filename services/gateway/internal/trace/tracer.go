package trace

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const maxIOLen = 500

type traceMsg struct {
	kind string // "run_create", "run_update", "span"
	// run fields
	runID      string
	sessionID  string
	durationMs float64
	transcript string
	response   string
	status     string
	// span fields
	span Span
}

// Tracer writes trace data asynchronously via a buffered channel.
// All methods are nil-safe (no-op on nil receiver).
type Tracer struct {
	store     *Store
	sessionID string
	ch        chan traceMsg
	done      chan struct{}
}

// NewTracer creates a tracer bound to a session. Must call Close when done.
func NewTracer(store *Store, sessionID string) *Tracer {
	t := &Tracer{
		store:     store,
		sessionID: sessionID,
		ch:        make(chan traceMsg, 64),
		done:      make(chan struct{}),
	}
	go t.drain()
	return t
}

func (t *Tracer) drain() {
	defer close(t.done)
	for msg := range t.ch {
		t.handle(msg)
	}
}

func (t *Tracer) handle(m traceMsg) {
	handlers := map[string]func() error{
		"run_create": func() error { return t.store.CreateRun(m.runID, m.sessionID) },
		"run_update": func() error { return t.store.UpdateRun(m.runID, m.durationMs, m.transcript, m.response, m.status) },
		"span":       func() error { return t.store.CreateSpan(m.span) },
	}
	fn, ok := handlers[m.kind]
	if !ok {
		return
	}
	if err := fn(); err != nil {
		slog.Warn("trace write failed", "kind", m.kind, "error", err)
	}
}

// StartRun begins a new run and returns its ID.
func (t *Tracer) StartRun() string {
	if t == nil {
		return ""
	}
	id := uuid.NewString()
	t.ch <- traceMsg{kind: "run_create", runID: id, sessionID: t.sessionID}
	return id
}

// EndRun finalizes a run.
func (t *Tracer) EndRun(runID string, durationMs float64, transcript, response, status string) {
	if t == nil {
		return
	}
	t.ch <- traceMsg{
		kind:       "run_update",
		runID:      runID,
		durationMs: durationMs,
		transcript: truncate(transcript, maxIOLen),
		response:   truncate(response, maxIOLen),
		status:     status,
	}
}

// RecordSpan records a completed span.
func (t *Tracer) RecordSpan(runID, name string, startedAt time.Time, durationMs float64, input, output, status, errMsg string) {
	if t == nil {
		return
	}
	t.ch <- traceMsg{
		kind: "span",
		span: Span{
			ID:         uuid.NewString(),
			RunID:      runID,
			Name:       name,
			StartedAt:  startedAt,
			DurationMs: durationMs,
			Input:      truncate(input, maxIOLen),
			Output:     truncate(output, maxIOLen),
			Status:     status,
			Error:      errMsg,
		},
	}
}

// Close drains pending writes and shuts down the background goroutine.
func (t *Tracer) Close() {
	if t == nil {
		return
	}
	close(t.ch)
	<-t.done
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
