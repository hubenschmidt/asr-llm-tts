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
	ASRClient     *pipeline.ASRRouter
	LLMClient     *pipeline.AgentLLM
	TTSClient     *pipeline.TTSRouter
	VADConfig     audio.VADConfig
	MaxConcurrent int
	RAGClient     *pipeline.RAGClient
	CallHistory   *pipeline.CallHistoryClient
	NoiseClient   *pipeline.NoiseClient
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
	Codec               string  `json:"codec"`
	SampleRate          int     `json:"sample_rate"`
	TTSEngine           string  `json:"tts_engine"`
	ASREngine           string  `json:"asr_engine"`
	SystemPrompt        string  `json:"system_prompt"`
	LLMModel            string  `json:"llm_model"`
	LLMEngine           string  `json:"llm_engine"`
	Mode                string  `json:"mode"`
	NoiseSuppression     bool    `json:"noise_suppression"`
	ASRPrompt            string  `json:"asr_prompt"`
	ConfidenceThreshold  float64 `json:"confidence_threshold"`
	ReferenceTranscript  string  `json:"reference_transcript"`
	TTSSpeed             float64 `json:"tts_speed"`
	TTSPitch             float64 `json:"tts_pitch"`
	TextNormalization    *bool   `json:"text_normalization"`
	InterSentencePauseMs int     `json:"inter_sentence_pause_ms"`
}

// wsAction is a text frame sent during a session (chat message, snippet process, etc).
type wsAction struct {
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
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
	asrEngine := meta.ASREngine
	if asrEngine == "" {
		asrEngine = "whisper.cpp"
	}

	sampleRate := meta.SampleRate
	if sampleRate <= 0 {
		sampleRate = 16000
	}

	sessionID := pipeline.GenerateUUID()
	systemPrompt := meta.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = "You are a helpful call center agent. Keep responses concise and conversational."
	}
	llmEngine := meta.LLMEngine
	if llmEngine == "" {
		llmEngine = "ollama"
	}

	mode := meta.Mode

	noiseClient := h.cfg.NoiseClient
	if !meta.NoiseSuppression {
		noiseClient = nil
	}

	confidenceThreshold := meta.ConfidenceThreshold
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.6
	}

	ttsSpeed := meta.TTSSpeed
	if ttsSpeed <= 0 {
		ttsSpeed = 1.0
	}

	textNorm := true
	if meta.TextNormalization != nil {
		textNorm = *meta.TextNormalization
	}

	slog.Info("call started", "session_id", sessionID, "codec", codec, "sample_rate", sampleRate, "tts_engine", ttsEngine, "asr_engine", asrEngine, "llm_engine", llmEngine, "mode", mode, "noise_suppression", meta.NoiseSuppression, "confidence_threshold", confidenceThreshold, "tts_speed", ttsSpeed)

	pipe := pipeline.New(pipeline.Config{
		ASRClient:            h.cfg.ASRClient,
		LLMClient:            h.cfg.LLMClient,
		TTSClient:            h.cfg.TTSClient,
		VADConfig:            h.cfg.VADConfig,
		RAGClient:            h.cfg.RAGClient,
		CallHistory:          h.cfg.CallHistory,
		NoiseClient:          noiseClient,
		NoiseSuppression:     meta.NoiseSuppression,
		SessionID:            sessionID,
		SystemPrompt:         systemPrompt,
		LLMModel:             meta.LLMModel,
		LLMEngine:            llmEngine,
		ASRPrompt:            meta.ASRPrompt,
		ConfidenceThreshold:  confidenceThreshold,
		ReferenceTranscript:  meta.ReferenceTranscript,
		TTSSpeed:             ttsSpeed,
		TTSPitch:             meta.TTSPitch,
		TextNormalization:    textNorm,
		InterSentencePauseMs: meta.InterSentencePauseMs,
	})

	sendEvent := newEventSender(conn)
	processMessages(ctx, conn, pipe, codec, sampleRate, ttsEngine, asrEngine, sendEvent, mode)
	flushIfNeeded(ctx, mode, pipe, ttsEngine, asrEngine, sendEvent)

	slog.Info("call ended")
}

// processMessages reads frames from the WebSocket in a loop.
// Text frames carry actions (chat, process) and are handled in all modes.
// Binary frames are mode-specific: talk=VAD, snippet=buffer, text=ignored.
func processMessages(ctx context.Context, conn *websocket.Conn, pipe *pipeline.Pipeline, codec audio.Codec, sampleRate int, ttsEngine, asrEngine string, sendEvent pipeline.EventCallback, mode string) {
	for {
		msgType, data, err := conn.ReadMessage()
		if err != nil {
			slog.Info("connection closed", "error", err)
			return
		}
		handleOneMessage(ctx, msgType, data, pipe, codec, sampleRate, ttsEngine, asrEngine, sendEvent, mode)
	}
}

func handleOneMessage(ctx context.Context, msgType int, data []byte, pipe *pipeline.Pipeline, codec audio.Codec, sampleRate int, ttsEngine, asrEngine string, sendEvent pipeline.EventCallback, mode string) {
	if msgType == websocket.TextMessage {
		handleTextFrame(ctx, data, pipe, ttsEngine, asrEngine, sendEvent, mode)
		return
	}
	if msgType != websocket.BinaryMessage {
		return
	}
	if mode == "text" {
		return
	}
	if mode == "snippet" {
		if err := pipe.ProcessChunkNoVAD(data, codec, sampleRate); err != nil {
			slog.Error("buffer chunk", "error", err)
			sendEvent(pipeline.Event{Type: "error", Text: err.Error()})
		}
		return
	}
	// talk mode (default): VAD processing
	if err := pipe.ProcessChunk(ctx, data, codec, sampleRate, ttsEngine, asrEngine, sendEvent); err != nil {
		slog.Error("process chunk", "error", err)
		sendEvent(pipeline.Event{Type: "error", Text: err.Error()})
	}
}

func flushIfNeeded(ctx context.Context, mode string, pipe *pipeline.Pipeline, ttsEngine, asrEngine string, sendEvent pipeline.EventCallback) {
	if mode == "snippet" || mode == "text" {
		return
	}
	if err := pipe.Flush(ctx, ttsEngine, asrEngine, sendEvent); err != nil {
		slog.Error("flush", "error", err)
	}
}

func handleTextFrame(ctx context.Context, data []byte, pipe *pipeline.Pipeline, ttsEngine, asrEngine string, sendEvent pipeline.EventCallback, mode string) {
	var act wsAction
	if err := json.Unmarshal(data, &act); err != nil {
		return
	}

	if act.Action == "chat" {
		if err := pipe.ProcessTextMessage(ctx, act.Message, sendEvent); err != nil {
			slog.Error("chat", "error", err)
			sendEvent(pipeline.Event{Type: "error", Text: err.Error()})
		}
		return
	}

	if act.Action == "process" && mode == "snippet" {
		if err := pipe.ProcessBuffered(ctx, ttsEngine, asrEngine, sendEvent); err != nil {
			slog.Error("process buffered", "error", err)
			sendEvent(pipeline.Event{Type: "error", Text: err.Error()})
		}
		return
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
