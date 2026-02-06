package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/ws"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))

	cfg := loadConfig()

	asrClient := pipeline.NewASRClient(cfg.whisperURL, cfg.asrPoolSize)
	llmClient := pipeline.NewLLMClient(cfg.ollamaURL, cfg.ollamaModel, cfg.llmSystemPrompt, cfg.llmMaxTokens, cfg.llmPoolSize)
	ttsClient := pipeline.NewTTSClient(cfg.piperURL, cfg.ttsPoolSize)

	handler := ws.NewHandler(ws.HandlerConfig{
		ASRClient:     asrClient,
		LLMClient:     llmClient,
		TTSClient:     ttsClient,
		VADConfig:     cfg.vadConfig,
		MaxConcurrent: cfg.maxConcurrentCalls,
	})

	mux := http.NewServeMux()
	mux.Handle("/ws/call", handler)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	addr := ":" + cfg.port
	srv := &http.Server{Addr: addr, Handler: mux}

	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		slog.Info("shutting down", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		srv.Shutdown(ctx)
	}()

	slog.Info("gateway starting", "addr", addr, "max_concurrent", cfg.maxConcurrentCalls)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		slog.Error("server failed", "error", err)
		os.Exit(1)
	}

	slog.Info("gateway stopped")
}

type config struct {
	port               string
	whisperURL         string
	ollamaURL          string
	ollamaModel        string
	llmSystemPrompt    string
	llmMaxTokens       int
	piperURL           string
	asrPoolSize        int
	llmPoolSize        int
	ttsPoolSize        int
	maxConcurrentCalls int
	vadConfig          audio.VADConfig
}

func loadConfig() config {
	vad := audio.DefaultVADConfig()
	vad.SpeechThresholdDB = envFloat("VAD_SPEECH_THRESHOLD_DB", vad.SpeechThresholdDB)

	return config{
		port:               envStr("GATEWAY_PORT", "8000"),
		whisperURL:         envStr("WHISPER_URL", "http://localhost:8178"),
		ollamaURL:          envStr("OLLAMA_URL", "http://localhost:11434"),
		ollamaModel:        envStr("OLLAMA_MODEL", "llama3.2:3b"),
		llmSystemPrompt:    envStr("LLM_SYSTEM_PROMPT", "You are a helpful call center agent. Keep responses concise and conversational."),
		llmMaxTokens:       envInt("LLM_MAX_TOKENS", 150),
		piperURL:           envStr("PIPER_URL", "http://localhost:5100"),
		asrPoolSize:        envInt("ASR_POOL_SIZE", 50),
		llmPoolSize:        envInt("LLM_POOL_SIZE", 50),
		ttsPoolSize:        envInt("TTS_POOL_SIZE", 50),
		maxConcurrentCalls: envInt("MAX_CONCURRENT_CALLS", 100),
		vadConfig:          vad,
	}
}

func envStr(key, fallback string) string {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	return val
}

func envInt(key string, fallback int) int {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	n, err := strconv.Atoi(val)
	if err != nil {
		return fallback
	}
	return n
}

func envFloat(key string, fallback float64) float64 {
	val := os.Getenv(key)
	if val == "" {
		return fallback
	}
	f, err := strconv.ParseFloat(val, 64)
	if err != nil {
		return fallback
	}
	return f
}
