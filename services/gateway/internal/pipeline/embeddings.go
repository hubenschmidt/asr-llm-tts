package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// EmbeddingClient generates vector embeddings via Ollama /api/embed.
type EmbeddingClient struct {
	url    string
	model  string
	client *http.Client
}

// NewEmbeddingClient creates an Ollama embedding client.
func NewEmbeddingClient(url, model string, poolSize int) *EmbeddingClient {
	return &EmbeddingClient{
		url:    url,
		model:  model,
		client: NewPooledHTTPClient(poolSize, 30*time.Second),
	}
}

// Embed sends text to Ollama and returns the embedding vector.
func (c *EmbeddingClient) Embed(ctx context.Context, text string) ([]float64, error) {
	start := time.Now()

	body, err := json.Marshal(embedRequest{Model: c.model, Input: text})
	if err != nil {
		return nil, fmt.Errorf("marshal embed request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/api/embed", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create embed request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embed request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("embed status %d", resp.StatusCode)
	}

	var result embedResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode embed response: %w", err)
	}

	if len(result.Embeddings) == 0 {
		return nil, fmt.Errorf("empty embedding response")
	}

	metrics.EmbeddingDuration.Observe(time.Since(start).Seconds())
	return result.Embeddings[0], nil
}

type embedRequest struct {
	Model string `json:"model"`
	Input string `json:"input"`
}

type embedResponse struct {
	Embeddings [][]float64 `json:"embeddings"`
}
