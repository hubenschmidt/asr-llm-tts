package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/models"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/orchestrator"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
)

type deps struct {
	ollamaURL         string
	ollamaModel       string
	whisperControlURL string
	asrRouter         *pipeline.ASRRouter
	llmRouter         *pipeline.AgentLLM
	ttsClient         *pipeline.TTSRouter
	svcMgr            *orchestrator.HTTPControlManager
	gpu               *gpuHub
	wsHandler         http.Handler
}

// registerRoutes wires all HTTP endpoints to the shared mux.
// Groups: WebSocket call handler, Prometheus metrics, model/GPU management,
// TTS warmup, STT model management, and service orchestration CRUD.
func registerRoutes(mux *http.ServeMux, d deps) {
	mux.Handle("/ws/call", d.wsHandler)
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok"))
	})

	mux.HandleFunc("/api/models", func(w http.ResponseWriter, r *http.Request) {
		llmModels, err := models.ListLLMModels(r.Context(), d.ollamaURL)
		if err != nil {
			slog.Error("list llm models", "error", err)
			llmModels = []string{d.ollamaModel}
		}
		loaded, _ := models.ListLoadedLLMs(r.Context(), d.ollamaURL)
		loadedNames := make([]string, 0, len(loaded))
		for _, m := range loaded {
			loadedNames = append(loadedNames, m.Name)
		}
		resp := map[string]interface{}{
			"asr": map[string]interface{}{
				"engines": d.asrRouter.Engines(),
			},
			"llm": map[string]interface{}{
				"active":  d.ollamaModel,
				"models":  llmModels,
				"loaded":  loadedNames,
				"engines": d.llmRouter.Engines(),
			},
			"tts": map[string]interface{}{
				"engines": d.ttsClient.Engines(),
			},
			"audio": map[string]interface{}{
				"bandwidth_modes": []map[string]interface{}{
					{"id": "wideband", "label": "Wideband", "sample_rate": nil, "bandpass": nil},
					{"id": "narrowband", "label": "Narrowband — Call Center (8kHz)", "sample_rate": 8000, "bandpass": map[string]int{"low_hz": 300, "high_hz": 3400}},
				},
				"default": "wideband",
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
		if err := models.PreloadLLM(r.Context(), d.ollamaURL, req.Model); err != nil {
			slog.Error("preload model", "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("model preloaded", "model", req.Model)
		d.gpu.broadcast(d.gpu.fetch())
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
		if err := unloadIfLLM(r.Context(), d.ollamaURL, req.Type, req.Model); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		d.gpu.broadcast(d.gpu.fetch())
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
		if !d.ttsClient.Has(req.Engine) {
			http.Error(w, "engine not available", http.StatusNotFound)
			return
		}
		slog.Info("warming up tts engine", "engine", req.Engine)
		_, err := d.ttsClient.Synthesize(r.Context(), "Hello.", req.Engine)
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
		if !d.ttsClient.Has(engine) {
			http.Error(w, "engine not available", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "ok", "engine": engine})
	})

	mux.HandleFunc("POST /api/gpu/unload-all", func(w http.ResponseWriter, r *http.Request) {
		slog.Info("unload-all requested")
		if err := models.UnloadAllLLMs(r.Context(), d.ollamaURL); err != nil {
			slog.Warn("unload-all ollama", "error", err)
		}
		stopRunningServices(r.Context(), d.svcMgr, "unload-all")
		data := d.gpu.fetch()
		d.gpu.broadcast(data)
		w.Header().Set("Content-Type", "application/json")
		if data != nil {
			w.Write(data)
			return
		}
		w.Write([]byte(`{"vram_total_mb":0,"vram_used_mb":0,"processes":[]}`))
	})

	mux.HandleFunc("GET /api/gpu", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		data := d.gpu.fetch()
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

		data := d.gpu.fetch()
		if data != nil {
			fmt.Fprintf(w, "data: %s\n\n", data)
			flusher.Flush()
		}

		ch := d.gpu.subscribe()
		defer d.gpu.unsubscribe(ch)
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

	mux.HandleFunc("GET /api/stt/models", func(w http.ResponseWriter, r *http.Request) {
		if d.whisperControlURL == "" {
			http.Error(w, "whisper-control not configured", http.StatusServiceUnavailable)
			return
		}
		resp, err := http.Get(d.whisperControlURL + "/models")
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/json")
		io.Copy(w, resp.Body)
	})

	mux.HandleFunc("POST /api/stt/models/download", func(w http.ResponseWriter, r *http.Request) {
		if d.whisperControlURL == "" {
			http.Error(w, "whisper-control not configured", http.StatusServiceUnavailable)
			return
		}
		client := &http.Client{}
		resp, err := client.Post(d.whisperControlURL+"/models/download", "application/json", r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(resp.StatusCode)
		flush := func() {}
		if f, ok := w.(http.Flusher); ok {
			flush = f.Flush
		}
		io.Copy(&flushWriter{w: w, flush: flush}, resp.Body)
	})

	mux.HandleFunc("GET /api/services", func(w http.ResponseWriter, r *http.Request) {
		services, err := d.svcMgr.StatusAll(r.Context())
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
		var params []string
		if q := r.URL.RawQuery; q != "" {
			params = append(params, q)
		}
		gpuData, err := d.svcMgr.Start(r.Context(), name, params...)
		if err != nil {
			slog.Error("service start failed", "name", name, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("service started", "name", name)
		d.gpu.broadcast(gpuData)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		json.NewEncoder(w).Encode(map[string]string{"status": "starting"})
	})

	mux.HandleFunc("POST /api/services/{name}/stop", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		slog.Info("service stop requested", "name", name)
		gpuData, err := d.svcMgr.Stop(r.Context(), name)
		if err != nil {
			slog.Error("service stop failed", "name", name, "error", err)
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		slog.Info("service stopped", "name", name)
		d.gpu.broadcast(gpuData)
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
	})

	mux.HandleFunc("GET /api/services/{name}/status", func(w http.ResponseWriter, r *http.Request) {
		name := r.PathValue("name")
		info, err := d.svcMgr.Status(r.Context(), name)
		if err != nil {
			http.Error(w, err.Error(), http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(info)
	})
}

func unloadIfLLM(ctx context.Context, ollamaURL, typ, model string) error {
	if typ != "llm" {
		return nil
	}
	slog.Info("unloading llm model", "model", model)
	if err := models.UnloadLLM(ctx, ollamaURL, model); err != nil {
		slog.Error("unload model", "error", err)
		return err
	}
	loaded, err := models.ListLoadedLLMs(ctx, ollamaURL)
	if err != nil {
		slog.Warn("list loaded models after unload", "error", err)
	}
	names := make([]string, len(loaded))
	for i, m := range loaded {
		names[i] = m.Name
	}
	slog.Info("model unloaded", "model", model, "still_loaded", names)
	return nil
}

func stopRunningServices(ctx context.Context, svcMgr *orchestrator.HTTPControlManager, label string) {
	svcs, _ := svcMgr.StatusAll(ctx)
	for _, svc := range svcs {
		stopIfRunning(ctx, svcMgr, svc, label)
	}
}

func stopIfRunning(ctx context.Context, svcMgr *orchestrator.HTTPControlManager, svc orchestrator.ServiceInfo, label string) {
	if svc.Status == orchestrator.StatusStopped {
		return
	}
	slog.Info(label+" stopping service", "name", svc.Name)
	if _, err := svcMgr.Stop(ctx, svc.Name); err != nil {
		slog.Warn(label+" stop", "name", svc.Name, "error", err)
	}
}

type flushWriter struct {
	w     io.Writer
	flush func()
}

func (fw *flushWriter) Write(p []byte) (int, error) {
	n, err := fw.w.Write(p)
	fw.flush()
	return n, err
}
