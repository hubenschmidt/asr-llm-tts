package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/models"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/orchestrator"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/pipeline"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/trace"
)

const (
	// proxyTimeout is the HTTP client timeout for proxied requests to
	// backend sidecars (whisper-control model list, model download).
	proxyTimeout = 30 * time.Second

	// defaultTraceSessionLimit is how many trace sessions are returned
	// when the caller omits the ?limit= query parameter.
	defaultTraceSessionLimit = 20
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
	traceStore        *trace.Store
}

// registerRoutes wires all HTTP endpoints to the shared mux.
func registerRoutes(mux *http.ServeMux, d deps) {
	mux.Handle("/ws/call", d.wsHandler)
	mux.HandleFunc("/health", handleHealth)
	mux.HandleFunc("/api/models", d.handleModels)
	mux.HandleFunc("POST /api/models/preload", d.handlePreload)
	mux.HandleFunc("POST /api/models/unload", d.handleUnload)
	mux.HandleFunc("POST /api/tts/warmup", d.handleTTSWarmup)
	mux.HandleFunc("/api/tts/health", d.handleTTSHealth)
	mux.HandleFunc("POST /api/gpu/unload-all", d.handleGPUUnloadAll)
	mux.HandleFunc("GET /api/gpu", d.handleGPU)
	mux.HandleFunc("GET /api/gpu/stream", d.handleGPUStream)
	mux.HandleFunc("GET /api/asr/models", d.handleASRModels)
	mux.HandleFunc("POST /api/asr/models/download", d.handleASRDownload)
	mux.HandleFunc("GET /api/services", d.handleServices)
	mux.HandleFunc("POST /api/services/{name}/start", d.handleServiceStart)
	mux.HandleFunc("POST /api/services/{name}/stop", d.handleServiceStop)
	mux.HandleFunc("GET /api/services/{name}/status", d.handleServiceStatus)
	registerTraceRoutes(mux, d.traceStore)
}

func handleHealth(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ok"))
}

func (d deps) handleModels(w http.ResponseWriter, r *http.Request) {
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
}

func (d deps) handlePreload(w http.ResponseWriter, r *http.Request) {
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
}

func (d deps) handleUnload(w http.ResponseWriter, r *http.Request) {
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
}

func (d deps) handleTTSWarmup(w http.ResponseWriter, r *http.Request) {
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
	_, err := d.ttsClient.Synthesize(r.Context(), "Hello.", req.Engine, pipeline.TTSOptions{})
	if err != nil {
		slog.Error("tts warmup", "error", err)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	slog.Info("tts engine warmed up", "engine", req.Engine)
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

func (d deps) handleTTSHealth(w http.ResponseWriter, r *http.Request) {
	engine := r.URL.Query().Get("engine")
	if !d.ttsClient.Has(engine) {
		http.Error(w, "engine not available", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]string{"status": "ok", "engine": engine})
}

func (d deps) handleGPUUnloadAll(w http.ResponseWriter, r *http.Request) {
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
}

func (d deps) handleGPU(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	data := d.gpu.fetch()
	if data == nil {
		w.Write([]byte(`{"vram_total_mb":0,"vram_used_mb":0,"processes":[]}`))
		return
	}
	w.Write(data)
}

func (d deps) handleGPUStream(w http.ResponseWriter, r *http.Request) {
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
}

func (d deps) handleASRModels(w http.ResponseWriter, r *http.Request) {
	if d.whisperControlURL == "" {
		http.Error(w, "whisper-control not configured", http.StatusServiceUnavailable)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "GET", d.whisperControlURL+"/models", nil)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	client := &http.Client{Timeout: proxyTimeout}
	resp, err := client.Do(req)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()
	w.Header().Set("Content-Type", "application/json")
	io.Copy(w, resp.Body)
}

func (d deps) handleASRDownload(w http.ResponseWriter, r *http.Request) {
	if d.whisperControlURL == "" {
		http.Error(w, "whisper-control not configured", http.StatusServiceUnavailable)
		return
	}
	req, err := http.NewRequestWithContext(r.Context(), "POST", d.whisperControlURL+"/models/download", r.Body)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Content-Type", "application/json")
	client := &http.Client{Timeout: proxyTimeout}
	resp, err := client.Do(req)
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
}

func (d deps) handleServices(w http.ResponseWriter, r *http.Request) {
	services, err := d.svcMgr.StatusAll(r.Context())
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(services)
}

func (d deps) handleServiceStart(w http.ResponseWriter, r *http.Request) {
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
}

func (d deps) handleServiceStop(w http.ResponseWriter, r *http.Request) {
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
}

func (d deps) handleServiceStatus(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	info, err := d.svcMgr.Status(r.Context(), name)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(info)
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

func registerTraceRoutes(mux *http.ServeMux, store *trace.Store) {
	mux.HandleFunc("GET /api/traces/sessions", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(w, "tracing disabled", http.StatusNotFound)
			return
		}
		limit := queryInt(r, "limit", defaultTraceSessionLimit)
		offset := queryInt(r, "offset", 0)
		sessions, total, err := store.ListSessions(limit, offset)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"sessions": sessions, "total": total})
	})

	mux.HandleFunc("GET /api/traces/sessions/{id}", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(w, "tracing disabled", http.StatusNotFound)
			return
		}
		sess, runs, err := store.GetSession(r.PathValue("id"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"session": sess, "runs": runs})
	})

	mux.HandleFunc("GET /api/traces/sessions/{id}/runs/{runId}", func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			http.Error(w, "tracing disabled", http.StatusNotFound)
			return
		}
		run, spans, err := store.GetRun(r.PathValue("id"), r.PathValue("runId"))
		if err != nil {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]interface{}{"run": run, "spans": spans})
	})
}

func queryInt(r *http.Request, key string, fallback int) int {
	v := r.URL.Query().Get(key)
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}
