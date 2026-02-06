package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
)

var upgrader = websocket.Upgrader{
	ReadBufferSize:  16384,
	WriteBufferSize: 16384,
	CheckOrigin:     func(r *http.Request) bool { return true },
}

// HandlerConfig holds the shared backend clients for all call sessions.
type HandlerConfig struct {
	ASRClient      *pipeline.ASRClient
	LLMClient      *pipeline.LLMClient
	TTSClient      *pipeline.TTSClient
	VADConfig      audio.VADConfig
	MaxConcurrent  int
}

// Handler manages WebSocket call sessions with admission control.
type Handler struct {
	cfg HandlerConfig
	sem chan struct{}
}

// NewHandler creates a WebSocket handler with shared backend clients and concurrency limit.
func NewHandler(cfg HandlerConfig) *Handler {
	maxConc := cfg.MaxConcurrent
	if maxConc <= 0 {
		maxConc = 100
	}
	return &Handler{
		cfg: cfg,
		sem: make(chan struct{}, maxConc),
	}
}

// callMetadata is the first text frame sent by the client.
type callMetadata struct {
	Codec      string `json:"codec"`
	SampleRate int    `json:"sample_rate"`
	TTSEngine  string `json:"tts_engine"`
	Mode       string `json:"mode"`
}

// ServeHTTP upgrades the connection and runs the call session.
// Returns 503 if at max concurrent call capacity.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	select {
	case h.sem <- struct{}{}:
		defer func() { <-h.sem }()
	default:
		http.Error(w, "at capacity", http.StatusServiceUnavailable)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	metrics.CallsActive.Inc()
	metrics.CallsTotal.Inc()
	defer metrics.CallsActive.Dec()

	h.runSession(conn)
}

func (h *Handler) runSession(conn *websocket.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meta, err := readMetadata(conn)
	if err != nil {
		slog.Error("read metadata", "error", err)
		return
	}

	codec := audio.Codec(meta.Codec)
	ttsEngine := meta.TTSEngine
	if ttsEngine == "" {
		ttsEngine = "fast"
	}

	sampleRate := meta.SampleRate
	if sampleRate <= 0 {
		sampleRate = 16000
	}

	slog.Info("call started", "codec", codec, "sample_rate", sampleRate, "tts_engine", ttsEngine, "mode", meta.Mode)

	pipe := pipeline.New(pipeline.Config{
		ASRClient: h.cfg.ASRClient,
		LLMClient: h.cfg.LLMClient,
		TTSClient: h.cfg.TTSClient,
		VADConfig: h.cfg.VADConfig,
	})

	sendEvent := newEventSender(conn)
	processMessages(ctx, conn, pipe, codec, sampleRate, ttsEngine, sendEvent)

	if err = pipe.Flush(ctx, ttsEngine, sendEvent); err != nil {
		slog.Error("flush", "error", err)
	}

	slog.Info("call ended")
}

func processMessages(ctx context.Context, conn *websocket.Conn, pipe *pipeline.Pipeline, codec audio.Codec, sampleRate int, ttsEngine string, sendEvent pipeline.EventCallback) {
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			slog.Info("connection closed", "error", err)
			return
		}

		if msgType != websocket.BinaryMessage {
			return
		}

		if err = pipe.ProcessChunk(ctx, data, codec, sampleRate, ttsEngine, sendEvent); err != nil {
			slog.Error("process chunk", "error", err)
			sendEvent(pipeline.Event{Type: "error", Text: err.Error()})
		}
	}
}

func newEventSender(conn *websocket.Conn) pipeline.EventCallback {
	var mu sync.Mutex
	return func(ev pipeline.Event) {
		mu.Lock()
		defer mu.Unlock()

		if ev.Audio != nil {
			if err := conn.WriteMessage(websocket.BinaryMessage, ev.Audio); err != nil {
				slog.Error("write audio", "error", err)
			}
		}

		jsonBytes, err := json.Marshal(ev)
		if err != nil {
			return
		}
		if err = conn.WriteMessage(websocket.TextMessage, jsonBytes); err != nil {
			slog.Error("write event", "error", err)
		}
	}
}

func readMetadata(conn *websocket.Conn) (*callMetadata, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	var meta callMetadata
	if err = json.Unmarshal(data, &meta); err != nil {
		return nil, err
	}
	return &meta, nil
}
