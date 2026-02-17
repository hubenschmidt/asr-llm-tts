package models

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"
)

// ListLLMModels queries Ollama /api/tags and returns installed model names.
func ListLLMModels(ctx context.Context, ollamaURL string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", ollamaURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ollama tags status %d", resp.StatusCode)
	}

	var result struct {
		Models []struct {
			Name string `json:"name"`
		} `json:"models"`
	}
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	names := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		if !strings.Contains(m.Name, "embed") {
			names = append(names, m.Name)
		}
	}
	return names, nil
}

// LoadedLLM describes a model currently loaded in Ollama.
type LoadedLLM struct {
	Name string `json:"name"`
	Size int64  `json:"size"`
}

// ListLoadedLLMs returns the models currently loaded in Ollama VRAM via /api/ps.
func ListLoadedLLMs(ctx context.Context, ollamaURL string) ([]LoadedLLM, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", ollamaURL+"/api/ps", nil)
	if err != nil {
		return nil, err
	}
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result struct {
		Models []LoadedLLM `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}
	return result.Models, nil
}

// UnloadLLM triggers Ollama to unload a model from GPU VRAM and waits
// until the model is confirmed unloaded (or timeout).
func UnloadLLM(ctx context.Context, ollamaURL, model string) error {
	body, err := json.Marshal(map[string]any{"model": model, "keep_alive": 0, "stream": false})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", ollamaURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama unload status %d", resp.StatusCode)
	}

	return waitForUnload(ctx, ollamaURL, model)
}

func waitForUnload(ctx context.Context, ollamaURL, model string) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := ListLoadedLLMs(ctx, ollamaURL)
		if err != nil {
			return nil // best-effort
		}
		if !isModelLoaded(loaded, model) {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("model %s still loaded after timeout", model)
}

func isModelLoaded(loaded []LoadedLLM, model string) bool {
	return slices.ContainsFunc(loaded, func(m LoadedLLM) bool { return m.Name == model })
}

// UnloadAllLLMs unloads every model currently loaded in Ollama VRAM.
func UnloadAllLLMs(ctx context.Context, ollamaURL string) error {
	loaded, err := ListLoadedLLMs(ctx, ollamaURL)
	if err != nil {
		return err
	}
	for _, m := range loaded {
		if err := UnloadLLM(ctx, ollamaURL, m.Name); err != nil {
			return fmt.Errorf("unload %s: %w", m.Name, err)
		}
	}
	return nil
}

// PreloadLLM triggers Ollama to load a model into GPU VRAM.
func PreloadLLM(ctx context.Context, ollamaURL, model string) error {
	body, err := json.Marshal(map[string]any{"model": model, "keep_alive": -1})
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", ollamaURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	client := &http.Client{Timeout: 10 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama preload status %d", resp.StatusCode)
	}
	return nil
}
