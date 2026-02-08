package models

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ASRModel represents a whisper model with download status.
type ASRModel struct {
	Name       string `json:"name"`
	SizeMB     int    `json:"size_mb"`
	Downloaded bool   `json:"downloaded"`
}

var asrCatalog = []struct {
	Name   string
	SizeMB int
}{
	{"tiny", 75},
	{"tiny.en", 75},
	{"base", 142},
	{"base.en", 142},
	{"small", 466},
	{"small.en", 466},
	{"small.en-tdrz", 466},
	{"medium", 1500},
	{"medium.en", 1500},
	{"large-v1", 3100},
	{"large-v2", 3100},
	{"large-v3", 3100},
	{"large-v3-turbo", 1600},
}

const huggingFaceBase = "https://huggingface.co/ggerganov/whisper.cpp/resolve/main/ggml"

// ListASRModels returns the model catalog with download status based on what exists in dir.
func ListASRModels(dir string) []ASRModel {
	downloaded := scanDownloaded(dir)
	out := make([]ASRModel, 0, len(asrCatalog))
	for _, c := range asrCatalog {
		out = append(out, ASRModel{
			Name:       c.Name,
			SizeMB:     c.SizeMB,
			Downloaded: downloaded[c.Name],
		})
	}
	return out
}

func scanDownloaded(dir string) map[string]bool {
	m := make(map[string]bool)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return m
	}
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "ggml-") || !strings.HasSuffix(name, ".bin") {
			continue
		}
		model := strings.TrimSuffix(strings.TrimPrefix(name, "ggml-"), ".bin")
		m[model] = true
	}
	return m
}

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
	resp, err := http.DefaultClient.Do(req)
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
	req, err := http.NewRequestWithContext(ctx, "POST", ollamaURL+"/api/generate", strings.NewReader(string(body)))
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

	// Poll /api/ps until model is confirmed unloaded
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		loaded, err := ListLoadedLLMs(ctx, ollamaURL)
		if err != nil {
			return nil // best-effort
		}
		found := false
		for _, m := range loaded {
			if m.Name == model {
				found = true
			}
		}
		if !found {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("model %s still loaded after timeout", model)
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
	req, err := http.NewRequestWithContext(ctx, "POST", ollamaURL+"/api/generate", strings.NewReader(string(body)))
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

// ProgressFunc is called periodically with bytes downloaded and total size.
type ProgressFunc func(downloaded, total int64)

// DownloadASRModel downloads a whisper model from HuggingFace to dir.
func DownloadASRModel(ctx context.Context, name, dir string, onProgress ProgressFunc) error {
	if !isValidASRModel(name) {
		return fmt.Errorf("unknown model: %s", name)
	}

	url := fmt.Sprintf("%s-%s.bin", huggingFaceBase, name)
	dest := filepath.Join(dir, fmt.Sprintf("ggml-%s.bin", name))
	tmp := dest + ".tmp"

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	client := &http.Client{Timeout: 30 * time.Minute}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download status %d", resp.StatusCode)
	}

	if err = os.MkdirAll(dir, 0o755); err != nil {
		return err
	}

	f, err := os.Create(tmp)
	if err != nil {
		return err
	}

	reader := &progressReader{r: resp.Body, total: resp.ContentLength, onProgress: onProgress}
	_, copyErr := io.Copy(f, reader)
	f.Close()
	if copyErr != nil {
		os.Remove(tmp)
		return copyErr
	}

	return os.Rename(tmp, dest)
}

type progressReader struct {
	r          io.Reader
	total      int64
	downloaded int64
	onProgress ProgressFunc
	lastReport int64
}

func (pr *progressReader) Read(p []byte) (int, error) {
	n, err := pr.r.Read(p)
	pr.downloaded += int64(n)
	if pr.onProgress == nil {
		return n, err
	}
	// Report every ~1MB to avoid flooding
	if pr.downloaded-pr.lastReport >= 1<<20 || err == io.EOF {
		pr.onProgress(pr.downloaded, pr.total)
		pr.lastReport = pr.downloaded
	}
	return n, err
}

// ParseActiveModel extracts the model name from a path like "/path/to/ggml-large-v3.bin".
func ParseActiveModel(modelPath string) string {
	base := filepath.Base(modelPath)
	name := strings.TrimSuffix(strings.TrimPrefix(base, "ggml-"), ".bin")
	if name == base {
		return base
	}
	return name
}

func isValidASRModel(name string) bool {
	for _, c := range asrCatalog {
		if c.Name == name {
			return true
		}
	}
	return false
}
