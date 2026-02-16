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
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/denoise"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/env"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/models"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/orchestrator"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/trace"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/ws"
)

// tuning holds knobs loaded from gateway.json. These are values that may
// eventually move to a database; for now a JSON file keeps them out of env vars.
type tuning struct {
	LLMSystemPrompt    string  `json:"llm_system_prompt"`
	LLMMaxTokens       int     `json:"llm_max_tokens"`
	ASRPoolSize        int     `json:"asr_pool_size"`
	LLMPoolSize        int     `json:"llm_pool_size"`
	TTSPoolSize        int     `json:"tts_pool_size"`
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
		ASRPoolSize:        50,
		LLMPoolSize:        50,
		TTSPoolSize:        50,
		VADSpeechThreshold: -25,
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
	piperModelDir := env.Str("PIPER_MODEL_DIR", "/models")
	whisperServerURL := env.Str("WHISPER_SERVER_URL", "")
	whisperControlURL := env.Str("WHISPER_CONTROL_URL", "")
	openaiAPIKey := env.Str("OPENAI_API_KEY", "")
	anthropicAPIKey := env.Str("ANTHROPIC_API_KEY", "")
	audioclassifyURL := env.Str("AUDIOCLASSIFY_URL", "")

	// Service orchestrator
	svcRegistry := orchestrator.NewRegistry(map[string]orchestrator.ServiceMeta{
		"whisper-server": {
			Category:   "asr",
			HealthURL:  whisperServerURL,
			ControlURL: whisperControlURL,
		},
	})
	svcMgr := orchestrator.NewHTTPControlManager(svcRegistry)

	whisperPrompt := env.Str("WHISPER_PROMPT", "Customer service call transcript:")
	asrRouter := initASR(whisperServerURL, t.ASRPoolSize, whisperPrompt)
	llmRouter := initLLM(ollamaURL, ollamaModel, openaiAPIKey, anthropicAPIKey, t)
	ttsClient := initTTS(piperModelDir)

	// VAD config
	vad := audio.DefaultVADConfig()
	vad.SpeechThresholdDB = t.VADSpeechThreshold

	denoiser := denoise.New()

	var classifyClient *pipeline.ClassifyClient
	if audioclassifyURL != "" {
		classifyClient = pipeline.NewClassifyClient(audioclassifyURL)
	}

	postgresURL := env.Str("POSTGRES_URL", "")
	var traceStore *trace.Store
	if postgresURL != "" {
		var traceErr error
		traceStore, traceErr = trace.Open(postgresURL)
		if traceErr != nil {
			slog.Error("trace store open failed", "error", traceErr)
		}
		if traceStore != nil {
			slog.Info("tracing enabled", "postgres", postgresURL)
		}
	}

	handler := ws.NewHandler(ws.HandlerConfig{
		ASRClient:     asrRouter,
		LLMClient:     llmRouter,
		TTSClient:     ttsClient,
		VADConfig:     vad,
		Denoiser:       denoiser,
		ClassifyClient: classifyClient,
		TraceStore:     traceStore,
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
		traceStore:        traceStore,
	})

	addr := ":" + port
	srv := &http.Server{Addr: addr, Handler: mux}

	go awaitShutdown(srv, ollamaURL, svcMgr)

	slog.Info("gateway starting", "addr", addr)

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

func initASR(whisperServerURL string, poolSize int, prompt string) *pipeline.ASRRouter {
	backends := map[string]pipeline.ASRTranscriber{}
	if whisperServerURL != "" {
		backends["whisper-server"] = pipeline.NewASRClient(whisperServerURL, poolSize, prompt)
	}
	return pipeline.NewASRRouter(backends, "whisper-server")
}

func initLLM(ollamaURL, ollamaModel, openaiAPIKey, anthropicAPIKey string, t tuning) *pipeline.AgentLLM {
	router := pipeline.NewAgentLLM("ollama", t.LLMMaxTokens)
	router.Register("ollama", agents.NewOpenAIProvider(agents.OpenAIProviderParams{
		BaseURL:      param.NewOpt(ollamaURL + "/v1/"),
		APIKey:       param.NewOpt("ollama"),
		UseResponses: param.NewOpt(false),
	}), ollamaModel)
	if openaiAPIKey != "" {
		router.Register("openai", agents.NewOpenAIProvider(agents.OpenAIProviderParams{
			BaseURL:      param.NewOpt(t.OpenAIURL + "/v1/"),
			APIKey:       param.NewOpt(openaiAPIKey),
			UseResponses: param.NewOpt(true),
		}), t.OpenAIModel)
	}
	if anthropicAPIKey != "" {
		router.Register("anthropic", agents.NewOpenAIProvider(agents.OpenAIProviderParams{
			BaseURL:      param.NewOpt(t.AnthropicURL + "/v1/"),
			APIKey:       param.NewOpt(anthropicAPIKey),
			UseResponses: param.NewOpt(false),
		}), t.AnthropicModel)
	}
	return router
}

func initTTS(piperModelDir string) *pipeline.TTSRouter {
	backends := map[string]pipeline.TTSSynthesizer{
		"fast":    pipeline.NewPiperSynthesizer(piperModelDir, "en_US-lessac-low"),
		"quality": pipeline.NewPiperSynthesizer(piperModelDir, "en_US-lessac-medium"),
		"high":    pipeline.NewPiperSynthesizer(piperModelDir, "en_US-lessac-high"),
	}
	return pipeline.NewTTSRouter(backends, "fast")
}
