package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// ClassifyResult holds a classification response from the audioclassify sidecar.
type ClassifyResult struct {
	Label      string             `json:"label"`
	Confidence float64            `json:"confidence"`
	Scores     map[string]float64 `json:"scores"`
	LatencyMs  float64            `json:"latency_ms"`
}

// ClassifyClient calls the audioclassify sidecar for scene and emotion classification.
type ClassifyClient struct {
	url    string
	client *http.Client
}

// NewClassifyClient creates a client for the audioclassify HTTP sidecar.
func NewClassifyClient(url string) *ClassifyClient {
	return &ClassifyClient{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// ClassifyEmotion sends float32 samples to the /emotion endpoint.
func (c *ClassifyClient) ClassifyEmotion(ctx context.Context, samples []float32) (*ClassifyResult, error) {
	return c.post(ctx, "/emotion", samples)
}

func (c *ClassifyClient) post(ctx context.Context, path string, samples []float32) (*ClassifyResult, error) {
	buf := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(s))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+path, bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("classify request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("classify http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("classify status %d: %s", resp.StatusCode, string(body))
	}

	var result ClassifyResult
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("classify decode: %w", err)
	}
	return &result, nil
}
