package pipeline

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"net/http"
	"time"
)

// NoiseClient calls the noisereduce sidecar to suppress background noise.
type NoiseClient struct {
	url    string
	client *http.Client
}

// NewNoiseClient creates a client for the noisereduce HTTP sidecar.
func NewNoiseClient(url string) *NoiseClient {
	return &NoiseClient{
		url:    url,
		client: &http.Client{Timeout: 5 * time.Second},
	}
}

// Denoise sends float32 samples to the sidecar and returns denoised samples.
func (c *NoiseClient) Denoise(ctx context.Context, samples []float32) ([]float32, error) {
	buf := make([]byte, len(samples)*4)
	for i, s := range samples {
		binary.LittleEndian.PutUint32(buf[i*4:], math.Float32bits(s))
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/denoise", bytes.NewReader(buf))
	if err != nil {
		return nil, fmt.Errorf("noise request: %w", err)
	}
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("noise http: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("noise status %d: %s", resp.StatusCode, string(body))
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("noise read: %w", err)
	}

	if len(respBytes)%4 != 0 {
		return nil, fmt.Errorf("noise response not aligned to float32")
	}

	out := make([]float32, len(respBytes)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(respBytes[i*4:]))
	}
	return out, nil
}
