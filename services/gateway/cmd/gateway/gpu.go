package main

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/models"
)

type gpuHub struct {
	mu         sync.Mutex
	subs       map[chan []byte]struct{}
	ollamaURL  string
	controlURL string
}

func newGPUHub(ollamaURL, controlURL string) *gpuHub {
	return &gpuHub{
		subs:       map[chan []byte]struct{}{},
		ollamaURL:  ollamaURL,
		controlURL: controlURL,
	}
}

func (h *gpuHub) subscribe() chan []byte {
	ch := make(chan []byte, 1)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch
}

func (h *gpuHub) unsubscribe(ch chan []byte) {
	h.mu.Lock()
	delete(h.subs, ch)
	h.mu.Unlock()
}

// enrich augments raw GPU JSON by filtering out zero-VRAM processes and
// replacing generic "ollama" process names with the actual loaded model names
// so the frontend can display which LLM is consuming VRAM.
func (h *gpuHub) enrich(raw []byte) []byte {
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

	filtered := make([]gpuProc, 0, len(gpu.Processes))
	for _, p := range gpu.Processes {
		if p.VRAMMB > 0 {
			filtered = append(filtered, p)
		}
	}
	gpu.Processes = filtered

	loaded, _ := models.ListLoadedLLMs(context.Background(), h.ollamaURL)
	modelIdx := 0
	for i := range gpu.Processes {
		isOllama := strings.Contains(gpu.Processes[i].Name, "ollama")
		if isOllama && modelIdx < len(loaded) {
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

func (h *gpuHub) fetch() []byte {
	if h.controlURL == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "GET", h.controlURL+"/gpu", nil)
	if err != nil {
		slog.Error("gpu fetch failed", "error", err)
		return nil
	}
	resp, err := (&http.Client{Timeout: 5 * time.Second}).Do(req)
	if err != nil {
		slog.Error("gpu fetch failed", "error", err)
		return nil
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	return h.enrich(body)
}

func (h *gpuHub) broadcast(data []byte) {
	if data == nil {
		return
	}
	slog.Info("gpu broadcast", "data", string(data))
	h.mu.Lock()
	for ch := range h.subs {
		select {
		case ch <- data:
		default:
		}
	}
	h.mu.Unlock()
}
