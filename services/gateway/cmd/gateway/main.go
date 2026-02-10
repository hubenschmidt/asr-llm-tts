package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nlpodyssey/openai-agents-go/agents"
	"github.com/openai/openai-go/v2/packages/param"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/env"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/models"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/orchestrator"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/ws"
)

// tuning holds knobs loaded from gateway.json. These are values that may
// eventually move to a database; for now a JSON file keeps them out of env vars.
type tuning struct {
	LLMSystemPrompt    string  `json:"llm_system_prompt"`
	LLMMaxTokens       int     `json:"llm_max_tokens"`
	EmbeddingModel     string  `json:"embedding_model"`
	ASRPoolSize        int     `json:"asr_pool_size"`
	LLMPoolSize        int     `json:"llm_pool_size"`
	TTSPoolSize        int     `json:"tts_pool_size"`
	QdrantPoolSize     int     `json:"qdrant_pool_size"`
	MaxConcurrentCalls int     `json:"max_concurrent_calls"`
	VectorSize         int     `json:"vector_size"`
	RAGTopK            int     `json:"rag_top_k"`
	RAGScoreThreshold  float64 `json:"rag_score_threshold"`
	VADSpeechThreshold float64 `json:"vad_speech_threshold_db"`
	OpenAIURL          string  `json:"openai_url"`
	OpenAIModel        string  `json:"openai_model"`
	AnthropicURL       string  `json:"anthropic_url"`
	AnthropicModel     string  `json:"anthropic_model"`
}

// defaultTuning returns sensible defaults matching gateway.json.
func defaultTuning() tuning {
	return tuning{
		LLMSystemPrompt:    "You are a helpful call center agent. Keep responses concise and conversational.",
		LLMMaxTokens:       2048,
		EmbeddingModel:     "nomic-embed-text",
		ASRPoolSize:        50,
		LLMPoolSize:        50,
		TTSPoolSize:        50,
		QdrantPoolSize:     10,
		MaxConcurrentCalls: 100,
		VectorSize:         768,
		RAGTopK:            3,
		RAGScoreThreshold:  0.7,
		VADSpeechThreshold: -30,
		OpenAIURL:          "https://api.openai.com",
		OpenAIModel:        "gpt-4.1-nano",
		AnthropicURL:       "https://api.anthropic.com",
		AnthropicModel:     "claude-sonnet-4-5",
	}
}

// loadTuning reads gateway.json if present, otherwise returns defaults.
func loadTuning(path string) tuning {
	t := defaultTuning()
	data, err := os.ReadFile(path)
	if err != nil {
		slog.Info("no config file, using defaults", "path", path)
		return t
	}
	if err = json.Unmarshal(data, &t); err != nil {
		slog.Warn("bad config file, using defaults", "path", path, "error", err)
		return defaultTuning()
	}
	slog.Info("loaded config", "path", path)
	return t
}

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	t := loadTuning("gateway.json")

	// Deployment env vars — URLs, ports, keys
	port := env.Str("GATEWAY_PORT", "8000")
	ollamaURL := env.Str("OLLAMA_URL", "http://localhost:11434")
	ollamaModel := env.Str("OLLAMA_MODEL", "llama3.2:3b")
	piperURL := env.Str("PIPER_URL", "http://localhost:5100")
	kokoroURL := env.Str("KOKORO_URL", "")
	melottsURL := env.Str("MELOTTS_URL", "")
	whisperServerURL := env.Str("WHISPER_SERVER_URL", "")
	whisperControlURL := env.Str("WHISPER_CONTROL_URL", "")
	qdrantURL := env.Str("QDRANT_URL", "")
	elevenlabsAPIKey := env.Str("ELEVENLABS_API_KEY", "")
	elevenlabsVoiceID := env.Str("ELEVENLABS_VOICE_ID", "21m00Tcm4TlvDq8ikWAM")
	elevenlabsModelID := env.Str("ELEVENLABS_MODEL_ID", "eleven_turbo_v2_5")
	openaiAPIKey := env.Str("OPENAI_API_KEY", "")
	anthropicAPIKey := env.Str("ANTHROPIC_API_KEY", "")

	// Service orchestrator
	svcRegistry := orchestrator.NewRegistry(map[string]orchestrator.ServiceMeta{
		"whisper-server": {
			Category:   "stt",
			HealthURL:  whisperServerURL,
			ControlURL: whisperControlURL,
		},
	})
	svcMgr := orchestrator.NewHTTPControlManager(svcRegistry)

	// ASR backends
	asrBackends := map[string]pipeline.ASRTranscriber{}
	if whisperServerURL != "" {
		asrBackends["whisper-server"] = pipeline.NewASRClient(whisperServerURL, t.ASRPoolSize)
	}
	asrRouter := pipeline.NewASRRouter(asrBackends, "whisper-server")

	// LLM backends (openai-agents-go SDK)
	llmRouter := pipeline.NewAgentLLM("ollama", t.LLMMaxTokens)
	llmRouter.Register("ollama", agents.NewOpenAIProvider(agents.OpenAIProviderParams{
		BaseURL:      param.NewOpt(ollamaURL + "/v1/"),
		APIKey:       param.NewOpt("ollama"),
		UseResponses: param.NewOpt(false),
	}), ollamaModel)
	if openaiAPIKey != "" {
		llmRouter.Register("openai", agents.NewOpenAIProvider(agents.OpenAIProviderParams{
			BaseURL:      param.NewOpt(t.OpenAIURL + "/v1/"),
			APIKey:       param.NewOpt(openaiAPIKey),
			UseResponses: param.NewOpt(true),
		}), t.OpenAIModel)
	}
	if anthropicAPIKey != "" {
		llmRouter.Register("anthropic", agents.NewOpenAIProvider(agents.OpenAIProviderParams{
			BaseURL:      param.NewOpt(t.AnthropicURL + "/v1/"),
			APIKey:       param.NewOpt(anthropicAPIKey),
			UseResponses: param.NewOpt(false),
		}), t.AnthropicModel)
	}

	// TTS backends
	ttsHTTP := pipeline.NewPooledHTTPClient(t.TTSPoolSize, 30*time.Second)
	ttsBackends := map[string]pipeline.TTSSynthesizer{
		"fast":    pipeline.NewPiperSynthesizer(piperURL, "en_US-lessac-low", ttsHTTP),
		"quality": pipeline.NewPiperSynthesizer(piperURL, "en_US-lessac-medium", ttsHTTP),
		"high":    pipeline.NewPiperSynthesizer(piperURL, "en_US-lessac-high", ttsHTTP),
	}
	if kokoroURL != "" {
		ttsBackends["kokoro"] = pipeline.NewOpenAISynthesizer(kokoroURL, "kokoro", "af_heart", ttsHTTP)
	}
	if melottsURL != "" {
		ttsBackends["melotts"] = pipeline.NewMeloSynthesizer(melottsURL, ttsHTTP)
	}
	if elevenlabsAPIKey != "" {
		ttsBackends["elevenlabs"] = pipeline.NewElevenLabsSynthesizer(elevenlabsAPIKey, elevenlabsVoiceID, elevenlabsModelID, ttsHTTP)
	}
	ttsClient := pipeline.NewTTSRouter(ttsBackends, "fast")

	// RAG + call history (nil when Qdrant not configured)
	ragClient, callHistory := initRAG(ollamaURL, qdrantURL, t)

	// VAD config
	vad := audio.DefaultVADConfig()
	vad.SpeechThresholdDB = t.VADSpeechThreshold

	handler := ws.NewHandler(ws.HandlerConfig{
		ASRClient:     asrRouter,
		LLMClient:     llmRouter,
		TTSClient:     ttsClient,
		VADConfig:     vad,
		MaxConcurrent: t.MaxConcurrentCalls,
		RAGClient:     ragClient,
		CallHistory:   callHistory,
	})

	gpu := newGPUHub(ollamaURL, whisperControlURL)

	mux := http.NewServeMux()
	registerRoutes(mux, deps{
		ollamaURL:         ollamaURL,
		ollamaModel:       ollamaModel,
		whisperControlURL: whisperControlURL,
		asrRouter:         asrRouter,
		llmRouter:         llmRouter,
		ttsClient:         ttsClient,
		svcMgr:            svcMgr,
		gpu:               gpu,
		wsHandler:         handler,
	})

	addr := ":" + port
	srv := &http.Server{Addr: addr, Handler: mux}

	go awaitShutdown(srv, ollamaURL, svcMgr)

	slog.Info("gateway starting", "addr", addr, "max_concurrent", t.MaxConcurrentCalls)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

	slog.Info("gateway stopped")
}

// awaitShutdown blocks until SIGINT/SIGTERM, then gracefully unloads models and stops services.
func awaitShutdown(srv *http.Server, ollamaURL string, svcMgr *orchestrator.HTTPControlManager) {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigCh
	slog.Info("shutting down", "signal", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	slog.Info("unloading ollama models")
	if err := models.UnloadAllLLMs(ctx, ollamaURL); err != nil {
		slog.Warn("ollama unload", "error", err)
	}

	slog.Info("stopping ML services")
	stopRunningServices(ctx, svcMgr, "shutdown")

	srv.Shutdown(ctx)
}

// initRAG sets up Qdrant-backed RAG and call history clients.
// Returns nil for both when Qdrant is not configured.
func initRAG(ollamaURL, qdrantURL string, t tuning) (*pipeline.RAGClient, *pipeline.CallHistoryClient) {
	if qdrantURL == "" {
		return nil, nil
	}

	embedClient := pipeline.NewEmbeddingClient(ollamaURL, t.EmbeddingModel, t.LLMPoolSize)
	qdrantClient := pipeline.NewQdrantClient(qdrantURL, t.QdrantPoolSize)

	initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer initCancel()

	if err := qdrantClient.EnsureCollection(initCtx, "knowledge_base", t.VectorSize); err != nil {
		slog.Warn("qdrant knowledge_base collection", "error", err)
	}
	if err := qdrantClient.EnsureCollection(initCtx, "call_history", t.VectorSize); err != nil {
		slog.Warn("qdrant call_history collection", "error", err)
	}

	slog.Info("rag enabled", "qdrant", qdrantURL, "embedding_model", t.EmbeddingModel)

	ragClient := pipeline.NewRAGClient(pipeline.RAGConfig{
		Embedder:       embedClient,
		Qdrant:         qdrantClient,
		Collection:     "knowledge_base",
		TopK:           t.RAGTopK,
		ScoreThreshold: t.RAGScoreThreshold,
	})
	callHistory := pipeline.NewCallHistoryClient(embedClient, qdrantClient, "call_history")

	return ragClient, callHistory
}
