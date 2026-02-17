package trace

import (
	"log/slog"
	"time"

	"github.com/google/uuid"
)

const (
	// maxTraceFieldLen caps the length of transcript/response/input/output strings
	// stored in trace spans to avoid bloating the SQLite database.
	maxTraceFieldLen = 500

	// traceChannelBuffer is how many trace messages can queue before the
	// background drain goroutine writes them to the store.
	traceChannelBuffer = 64
)

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

// NewTracer creates a tracer bound to a session.
// Launches a background goroutine (drain) that writes trace messages to the
// store sequentially. Callers MUST call Close() when done to flush pending
// writes and stop the goroutine — otherwise writes are lost and goroutine leaks.
func NewTracer(store *Store, sessionID string) *Tracer {
	t := &Tracer{
		store:     store,
		sessionID: sessionID,
		ch:        make(chan traceMsg, traceChannelBuffer),
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
	err := t.dispatch(m)
	if err != nil {
		slog.Warn("trace write failed", "kind", m.kind, "error", err)
	}
}

func (t *Tracer) dispatch(m traceMsg) error {
	if m.kind == "run_create" {
		return t.store.CreateRun(m.runID, m.sessionID)
	}
	if m.kind == "run_update" {
		return t.store.UpdateRun(m.runID, m.durationMs, m.transcript, m.response, m.status)
	}
	if m.kind == "span" {
		return t.store.CreateSpan(m.span)
	}
	return nil
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
		transcript: truncate(transcript, maxTraceFieldLen),
		response:   truncate(response, maxTraceFieldLen),
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
			Input:      truncate(input, maxTraceFieldLen),
			Output:     truncate(output, maxTraceFieldLen),
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
