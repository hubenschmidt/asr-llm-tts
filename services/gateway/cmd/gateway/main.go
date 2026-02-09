package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/models"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/orchestrator"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/ws"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := loadConfig()

	// Service orchestrator
	svcRegistry := orchestrator.NewRegistry(map[string]orchestrator.ServiceMeta{
		"whisper-server": {
			Category:   "stt",
			HealthURL:  cfg.whisperServerURL,
			ControlURL: cfg.whisperControlURL,
		},
	})
	svcMgr := orchestrator.NewHTTPControlManager(svcRegistry)

	// ASR backends
	asrBackends := map[string]pipeline.ASRTranscriber{}
	if cfg.whisperServerURL != "" {
		asrBackends["whisper-server"] = pipeline.NewASRClient(cfg.whisperServerURL, cfg.asrPoolSize)
	}
	asrRouter := pipeline.NewASRRouter(asrBackends, "whisper-server")

	llmClient := pipeline.NewLLMClient(cfg.ollamaURL, cfg.ollamaModel, cfg.llmSystemPrompt, cfg.llmMaxTokens, cfg.llmPoolSize)
	ttsHTTP := pipeline.NewPooledHTTPClient(cfg.ttsPoolSize, 30*time.Second)
	ttsBackends := map[string]pipeline.TTSSynthesizer{
		"fast":    pipeline.NewPiperSynthesizer(cfg.piperURL, "en_US-lessac-low", ttsHTTP),
		"quality": pipeline.NewPiperSynthesizer(cfg.piperURL, "en_US-lessac-medium", ttsHTTP),
		"high":    pipeline.NewPiperSynthesizer(cfg.piperURL, "en_US-lessac-high", ttsHTTP),
	}
	if cfg.kokoroURL != "" {
		ttsBackends["kokoro"] = pipeline.NewOpenAISynthesizer(cfg.kokoroURL, "kokoro", "af_heart", ttsHTTP)
	}
	if cfg.melottsURL != "" {
		ttsBackends["melotts"] = pipeline.NewMeloSynthesizer(cfg.melottsURL, ttsHTTP)
	}
	if cfg.elevenlabsAPIKey != "" {
		ttsBackends["elevenlabs"] = pipeline.NewElevenLabsSynthesizer(cfg.elevenlabsAPIKey, cfg.elevenlabsVoiceID, cfg.elevenlabsModelID, ttsHTTP)
	}
	ttsClient := pipeline.NewTTSRouter(ttsBackends, "fast")

	var ragClient *pipeline.RAGClient
	var callHistory *pipeline.CallHistoryClient

	if cfg.qdrantURL != "" {
		embedClient := pipeline.NewEmbeddingClient(cfg.ollamaURL, cfg.embeddingModel, cfg.llmPoolSize)
		qdrantClient := pipeline.NewQdrantClient(cfg.qdrantURL, cfg.qdrantPoolSize)

		initCtx, initCancel := context.WithTimeout(context.Background(), 10*time.Second)
		if err := qdrantClient.EnsureCollection(initCtx, "knowledge_base", cfg.vectorSize); err != nil {
			slog.Warn("qdrant knowledge_base collection", "error", err)
		}
		if err := qdrantClient.EnsureCollection(initCtx, "call_history", cfg.vectorSize); err != nil {
			slog.Warn("qdrant call_history collection", "error", err)
		}
		initCancel()

		ragClient = pipeline.NewRAGClient(pipeline.RAGConfig{
			Embedder:       embedClient,
			Qdrant:         qdrantClient,
			Collection:     "knowledge_base",
			TopK:           cfg.ragTopK,
			ScoreThreshold: cfg.ragScoreThreshold,
		})
		callHistory = pipeline.NewCallHistoryClient(embedClient, qdrantClient, "call_history")
		slog.Info("rag enabled", "qdrant", cfg.qdrantURL, "embedding_model", cfg.embeddingModel)
	}

	handler := ws.NewHandler(ws.HandlerConfig{
		ASRClient:     asrRouter,
		LLMClient:     llmClient,
		TTSClient:     ttsClient,
		VADConfig:     cfg.vadConfig,
		MaxConcurrent: cfg.maxConcurrentCalls,
		RAGClient:     ragClient,
		CallHistory:   callHistory,
	})

	gpu := newGPUHub(cfg.ollamaURL, cfg.whisperControlURL)

	mux := http.NewServeMux()
	registerRoutes(mux, deps{
		cfg:       cfg,
		asrRouter: asrRouter,
		ttsClient: ttsClient,
		svcMgr:    svcMgr,
		gpu:       gpu,
		wsHandler: handler,
	})

	addr := ":" + cfg.port
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		slog.Info("shutting down", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		slog.Info("unloading ollama models")
		if err := models.UnloadAllLLMs(ctx, cfg.ollamaURL); err != nil {
			slog.Warn("ollama unload", "error", err)
		}

		slog.Info("stopping ML services")
		stopRunningServices(ctx, svcMgr, "shutdown")

		srv.Shutdown(ctx)
	}()

	slog.Info("gateway starting", "addr", addr, "max_concurrent", cfg.maxConcurrentCalls)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

	slog.Info("gateway stopped")
}
