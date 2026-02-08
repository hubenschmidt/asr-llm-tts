package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"encoding/json"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/models"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/orchestrator"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/prompts"
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
	if cfg.fasterWhisperURL != "" {
		asrBackends["faster-whisper"] = pipeline.NewFasterWhisperClient(cfg.fasterWhisperURL, "tiny-int8", cfg.asrPoolSize)
	}
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
	if cfg.chatterboxURL != "" {
		ttsBackends["chatterbox"] = pipeline.NewOpenAISynthesizer(cfg.chatterboxURL, "chatterbox", "default", ttsHTTP)
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

	// GPU broadcast hub — SSE clients subscribe, service events trigger push
	var gpuMu sync.Mutex
	gpuSubs := map[chan []byte]struct{}{}

	gpuSubscribe := func() chan []byte {
		ch := make(chan []byte, 1)
		gpuMu.Lock()
		gpuSubs[ch] = struct{}{}
		gpuMu.Unlock()
		return ch
	}
	gpuUnsubscribe := func(ch chan []byte) {
		gpuMu.Lock()
		delete(gpuSubs, ch)
		gpuMu.Unlock()
	}

	enrichGPU := func(raw []byte) []byte {
		if raw == nil {
			return nil
		}
		type gpuProc struct {
			PID    int    `json:"pid"`
			Name   string `json:"name"`
			VRAMMB int    `json:"vram_mb"`
		}
		var gpu struct {
			VRAMTotalMB int       `json:"vram_total_mb"`
			VRAMUsedMB  int       `json:"vram_used_mb"`
			Processes   []gpuProc `json:"processes"`
		}
		if json.Unmarshal(raw, &gpu) != nil {
			return raw
		}

		// Filter out 0 MB processes (e.g. ollama parent)
		filtered := make([]gpuProc, 0, len(gpu.Processes))
		for _, p := range gpu.Processes {
			if p.VRAMMB > 0 {
				filtered = append(filtered, p)
			}
		}
		gpu.Processes = filtered

		// Replace ollama binary names with loaded model names
		loaded, _ := models.ListLoadedLLMs(context.Background(), cfg.ollamaURL)
		modelIdx := 0
		for i := range gpu.Processes {
			if !strings.Contains(gpu.Processes[i].Name, "ollama") {
				continue
			}
			if modelIdx < len(loaded) {
				gpu.Processes[i].Name = loaded[modelIdx].Name
				modelIdx++
			}
		}

		enriched, err := json.Marshal(gpu)
		if err != nil {
			return raw
		}
		return enriched
	}

	fetchGPU := func() []byte {
		if cfg.whisperControlURL == "" {
			return nil
		}
		resp, err := http.Get(cfg.whisperControlURL + "/gpu")
		if err != nil {
			slog.Error("gpu fetch failed", "error", err)
			return nil
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return enrichGPU(body)
	}

	broadcastGPU := func(data []byte) {
		if data == nil {
			return
		}
		slog.Info("gpu broadcast", "data", string(data))
		gpuMu.Lock()
		for ch := range gpuSubs {
			select {
			case ch <- data:
			default:
			}
		}
		gpuMu.Unlock()
	}

	mux := http.NewServeMux()
	mux.Handle("/ws/call", handler)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})
	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		llmModels, err := models.ListLLMModels(r.Context(), cfg.ollamaURL)
		if err != nil {
			slog.Error("list llm models", "error", err)
			llmModels = []string{cfg.ollamaModel}
		}
		resp := map[string]interface{}{
			"asr": map[string]interface{}{
				"engines": asrRouter.Engines(),
			},
			"llm": map[string]interface{}{
				"active": cfg.ollamaModel,
				"models": llmModels,
			},
			"tts": map[string]interface{}{
				"engines": ttsClient.Engines(),
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	})
	mux.HandleFunc("/api/models/preload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		slog.Info("preloading llm model", "model", req.Model)
		if err := models.PreloadLLM(r.Context(), cfg.ollamaURL, req.Model); err != nil {
			slog.Error("preload model", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("model preloaded", "model", req.Model)
		broadcastGPU(fetchGPU())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/models/unload", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Type  string `json:"type"`
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if req.Type == "llm" {
			slog.Info("unloading llm model", "model", req.Model)
			if err := models.UnloadLLM(r.Context(), cfg.ollamaURL, req.Model); err != nil {
				slog.Error("unload model", "error", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			loaded, err := models.ListLoadedLLMs(r.Context(), cfg.ollamaURL)
			if err != nil {
				slog.Warn("list loaded models after unload", "error", err)
			}
			names := make([]string, len(loaded))
			for i, m := range loaded {
				names[i] = m.Name
			}
			slog.Info("model unloaded", "model", req.Model, "still_loaded", names)
		}
		broadcastGPU(fetchGPU())
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/tts/warmup", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		var req struct {
			Engine string `json:"engine"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		if !ttsClient.HasEngine(req.Engine) {
			http.Error(w, "engine not available", http.StatusNotFound)
			return
		}
		slog.Info("warming up tts engine", "engine", req.Engine)
		_, err := ttsClient.Synthesize(r.Context(), "Hello.", req.Engine)
		if err != nil {
			slog.Error("tts warmup", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("tts engine warmed up", "engine", req.Engine)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
	})

	mux.HandleFunc("/api/tts/health", func(w http.ResponseWriter, r *http.Request) {
		engine := r.URL.Query().Get("engine")
		if !ttsClient.HasEngine(engine) {
			http.Error(w, "engine not available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "engine": engine})
	})

	mux.HandleFunc("POST /api/gpu/unload-all", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("unload-all requested")

		// Unload all Ollama models from VRAM
		if err := models.UnloadAllLLMs(r.Context(), cfg.ollamaURL); err != nil {
			slog.Warn("unload-all ollama", "error", err)
		}

		// Stop all running GPU services (whisper-server, etc.)
		svcs, _ := svcMgr.StatusAll(r.Context())
		for _, svc := range svcs {
			if svc.Status == orchestrator.StatusStopped {
				continue
			}
			slog.Info("unload-all stopping service", "name", svc.Name)
			if _, err := svcMgr.Stop(r.Context(), svc.Name); err != nil {
				slog.Warn("unload-all stop", "name", svc.Name, "error", err)
			}
		}

		// Fetch + broadcast fresh GPU state
		data := fetchGPU()
		broadcastGPU(data)

		w.Header().Set("Content-Type", "application/json")
		if data != nil {
			w.Write(data)
			return
		}
		w.Write([]byte(`{"vram_total_mb":0,"vram_used_mb":0,"processes":[]}`))
	})

	mux.HandleFunc("GET /api/gpu", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data := fetchGPU()
		if data == nil {
			w.Write([]byte(`{"vram_total_mb":0,"vram_used_mb":0,"processes":[]}`))
			return
		}
		w.Write(data)
	})

	mux.HandleFunc("GET /api/gpu/stream", func(w http.ResponseWriter, r *http.Request) {
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		// Send current state on connect
		data := fetchGPU()
		if data != nil {
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		ch := gpuSubscribe()
		defer gpuUnsubscribe(ch)
		slog.Info("gpu/stream client connected", "remote", r.RemoteAddr)

		for {
			select {
			case <-r.Context().Done():
				slog.Info("gpu/stream client disconnected", "remote", r.RemoteAddr)
				return
			case msg := <-ch:
				fmt.Fprintf(w, "data: %s\n\n", msg)
				flusher.Flush()
			}
		}
	})

	mux.HandleFunc("GET /api/services", func(w http.ResponseWriter, r *http.Request) {
		services, err := svcMgr.StatusAll(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(services)
	})
	mux.HandleFunc("POST /api/services/{name}/start", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		slog.Info("service start requested", "name", name)
		gpuData, err := svcMgr.Start(r.Context(), name)
		if err != nil {
			slog.Error("service start failed", "name", name, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("service started", "name", name)
		broadcastGPU(gpuData)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "starting"})
	})
	mux.HandleFunc("POST /api/services/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		slog.Info("service stop requested", "name", name)
		gpuData, err := svcMgr.Stop(r.Context(), name)
		if err != nil {
			slog.Error("service stop failed", "name", name, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("service stopped", "name", name)
		broadcastGPU(gpuData)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	})
	mux.HandleFunc("GET /api/services/{name}/status", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		info, err := svcMgr.Status(r.Context(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
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

		// Unload Ollama models
		slog.Info("unloading ollama models")
		if err := models.UnloadAllLLMs(ctx, cfg.ollamaURL); err != nil {
			slog.Warn("ollama unload", "error", err)
		}

		// Stop all ML services
		slog.Info("stopping ML services")
		svcs, _ := svcMgr.StatusAll(ctx)
		for _, svc := range svcs {
			if svc.Status == orchestrator.StatusStopped {
				continue
			}
			slog.Info("stopping service", "name", svc.Name)
			if _, err := svcMgr.Stop(ctx, svc.Name); err != nil {
				slog.Warn("stop service", "name", svc.Name, "error", err)
			}
		}

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
	port      string
	ollamaURL string
	ollamaModel        string
	llmSystemPrompt    string
	llmMaxTokens       int
	piperURL           string
	asrPoolSize        int
	llmPoolSize        int
	ttsPoolSize        int
	maxConcurrentCalls int
	vadConfig          audio.VADConfig
	qdrantURL          string
	qdrantPoolSize     int
	embeddingModel     string
	vectorSize         int
	ragTopK            int
	ragScoreThreshold  float64
	kokoroURL string
	chatterboxURL      string
	melottsURL         string
	fasterWhisperURL   string
	whisperServerURL   string
	whisperControlURL  string
	elevenlabsAPIKey   string
	elevenlabsVoiceID  string
	elevenlabsModelID  string
}

func loadConfig() config {
	vad := audio.DefaultVADConfig()
	vad.SpeechThresholdDB = envFloat("VAD_SPEECH_THRESHOLD_DB", vad.SpeechThresholdDB)

	return config{
		port:      envStr("GATEWAY_PORT", "8000"),
		ollamaURL: envStr("OLLAMA_URL", "http://localhost:11434"),
		ollamaModel:        envStr("OLLAMA_MODEL", "llama3.2:3b"),
		llmSystemPrompt:    envStr("LLM_SYSTEM_PROMPT", prompts.DefaultSystem),
		llmMaxTokens:       envInt("LLM_MAX_TOKENS", 150),
		piperURL:           envStr("PIPER_URL", "http://localhost:5100"),
		asrPoolSize:        envInt("ASR_POOL_SIZE", 50),
		llmPoolSize:        envInt("LLM_POOL_SIZE", 50),
		ttsPoolSize:        envInt("TTS_POOL_SIZE", 50),
		maxConcurrentCalls: envInt("MAX_CONCURRENT_CALLS", 100),
		vadConfig:          vad,
		qdrantURL:          envStr("QDRANT_URL", ""),
		qdrantPoolSize:     envInt("QDRANT_POOL_SIZE", 10),
		embeddingModel:     envStr("EMBEDDING_MODEL", "nomic-embed-text"),
		vectorSize:         envInt("VECTOR_SIZE", 768),
		ragTopK:            envInt("RAG_TOP_K", 3),
		ragScoreThreshold:  envFloat("RAG_SCORE_THRESHOLD", 0.7),
		kokoroURL: envStr("KOKORO_URL", ""),
		chatterboxURL:      envStr("CHATTERBOX_URL", ""),
		melottsURL:         envStr("MELOTTS_URL", ""),
		fasterWhisperURL:   envStr("FASTER_WHISPER_URL", ""),
		whisperServerURL:   envStr("WHISPER_SERVER_URL", ""),
		whisperControlURL:  envStr("WHISPER_CONTROL_URL", ""),
		elevenlabsAPIKey:   envStr("ELEVENLABS_API_KEY", ""),
		elevenlabsVoiceID:  envStr("ELEVENLABS_VOICE_ID", "21m00Tcm4TlvDq8ikWAM"),
		elevenlabsModelID:  envStr("ELEVENLABS_MODEL_ID", "eleven_turbo_v2_5"),
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
