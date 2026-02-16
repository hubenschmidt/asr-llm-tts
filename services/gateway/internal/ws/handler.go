package ws

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/denoise"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/trace"
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
	Denoiser       *denoise.Denoiser
	ClassifyClient *pipeline.ClassifyClient
	TraceStore     *trace.Store
}

// Handler manages WebSocket call sessions.
type Handler struct {
	cfg HandlerConfig
}

// NewHandler creates a WebSocket handler with shared backend clients.
func NewHandler(cfg HandlerConfig) *Handler {
	return &Handler{cfg: cfg}
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
	VADSilenceTimeoutMs  int     `json:"vad_silence_timeout_ms"`
	VADMinSpeechMs       int     `json:"vad_min_speech_ms"`
	AudioClassification  bool    `json:"audio_classification"`
}

// wsAction is a text frame sent during a session (chat message, snippet process, etc).
type wsAction struct {
	Action  string `json:"action"`
	Message string `json:"message,omitempty"`
}

// ServeHTTP upgrades the connection and runs the call session.
// Returns 503 if at max concurrent call capacity.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		slog.Error("websocket upgrade failed", "error", err)
		return
	}
	defer conn.Close()

	h.runSession(conn)
}

// sessionParams holds resolved metadata with defaults applied.
type sessionParams struct {
	codec               audio.Codec
	ttsEngine           string
	asrEngine           string
	sampleRate          int
	systemPrompt        string
	llmEngine           string
	mode                string
	confidenceThreshold float64
	ttsSpeed            float64
	textNorm            bool
	vadCfg              audio.VADConfig
}

var metaDefaults = map[string]string{
	"tts_engine":    "fast",
	"asr_engine":    "whisper.cpp",
	"llm_engine":    "ollama",
	"system_prompt": "You are a helpful call center agent. Keep responses concise and conversational.",
}

func resolveParams(meta *callMetadata, baseCfg audio.VADConfig) sessionParams {
	ttsEngine := orDefault(meta.TTSEngine, metaDefaults["tts_engine"])
	asrEngine := orDefault(meta.ASREngine, metaDefaults["asr_engine"])
	llmEngine := orDefault(meta.LLMEngine, metaDefaults["llm_engine"])
	systemPrompt := orDefault(meta.SystemPrompt, metaDefaults["system_prompt"])

	sampleRate := meta.SampleRate
	if sampleRate <= 0 {
		sampleRate = 16000
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

	vadCfg := baseCfg
	if meta.VADSilenceTimeoutMs > 0 {
		vadCfg.SilenceTimeout = time.Duration(meta.VADSilenceTimeoutMs) * time.Millisecond
	}
	if meta.VADMinSpeechMs > 0 {
		vadCfg.MinSpeechDuration = time.Duration(meta.VADMinSpeechMs) * time.Millisecond
	}

	return sessionParams{
		codec:               audio.Codec(meta.Codec),
		ttsEngine:           ttsEngine,
		asrEngine:           asrEngine,
		sampleRate:          sampleRate,
		systemPrompt:        systemPrompt,
		llmEngine:           llmEngine,
		mode:                meta.Mode,
		confidenceThreshold: confidenceThreshold,
		ttsSpeed:            ttsSpeed,
		textNorm:            textNorm,
		vadCfg:              vadCfg,
	}
}

func orDefault(val, fallback string) string {
	if val != "" {
		return val
	}
	return fallback
}

func (h *Handler) runSession(conn *websocket.Conn) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	meta, err := readMetadata(conn)
	if err != nil {
		slog.Error("read metadata", "error", err)
		return
	}

	sp := resolveParams(meta, h.cfg.VADConfig)
	sessionID := uuid.NewString()

	var denoiser *denoise.Denoiser
	if meta.NoiseSuppression {
		denoiser = h.cfg.Denoiser
	}

	classifyClient := h.cfg.ClassifyClient
	if !meta.AudioClassification {
		classifyClient = nil
	}

	slog.Info("call started", "session_id", sessionID, "codec", sp.codec, "sample_rate", sp.sampleRate, "tts_engine", sp.ttsEngine, "asr_engine", sp.asrEngine, "llm_engine", sp.llmEngine, "mode", sp.mode, "noise_suppression", meta.NoiseSuppression, "confidence_threshold", sp.confidenceThreshold, "tts_speed", sp.ttsSpeed)

	tracer := h.startTracer(sessionID, meta)
	if tracer != nil {
		defer func() {
			tracer.Close()
			_ = h.cfg.TraceStore.EndSession(sessionID)
		}()
	}

	pipe := pipeline.New(pipeline.Config{
		ASRClient:            h.cfg.ASRClient,
		LLMClient:            h.cfg.LLMClient,
		TTSClient:            h.cfg.TTSClient,
		VADConfig:            sp.vadCfg,
		Denoiser:             denoiser,
		NoiseSuppression:     meta.NoiseSuppression,
		SessionID:            sessionID,
		SystemPrompt:         sp.systemPrompt,
		LLMModel:             meta.LLMModel,
		LLMEngine:            sp.llmEngine,
		ASRPrompt:            meta.ASRPrompt,
		ConfidenceThreshold:  sp.confidenceThreshold,
		ReferenceTranscript:  meta.ReferenceTranscript,
		TTSSpeed:             sp.ttsSpeed,
		TTSPitch:             meta.TTSPitch,
		TextNormalization:    sp.textNorm,
		InterSentencePauseMs: meta.InterSentencePauseMs,
		ClassifyClient:       classifyClient,
		AudioClassification:  meta.AudioClassification,
		Tracer:               tracer,
	})

	sendEvent := newEventSender(conn)
	processMessages(ctx, conn, pipe, sp.codec, sp.sampleRate, sp.ttsEngine, sp.asrEngine, sendEvent, sp.mode)
	flushIfNeeded(ctx, sp.mode, pipe, sp.ttsEngine, sp.asrEngine, sendEvent)

	slog.Info("call ended")
}

func (h *Handler) startTracer(sessionID string, meta *callMetadata) *trace.Tracer {
	if h.cfg.TraceStore == nil {
		return nil
	}
	metaJSON, _ := json.Marshal(meta)
	_ = h.cfg.TraceStore.CreateSession(sessionID, string(metaJSON))
	return trace.NewTracer(h.cfg.TraceStore, sessionID)
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
