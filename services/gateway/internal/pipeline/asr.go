package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// ASRTranscriber produces transcriptions from audio samples.
type ASRTranscriber interface {
	Transcribe(ctx context.Context, samples []float32) (*ASRResult, error)
}

// ASRResult holds the transcription output.
type ASRResult struct {
	Text      string  `json:"text"`
	LatencyMs float64 `json:"latency_ms"`
}

// ASRRouter dispatches to the correct ASR backend based on engine name.
type ASRRouter struct {
	backends map[string]ASRTranscriber
	fallback string
}

// NewASRRouter creates a router with registered backends.
func NewASRRouter(backends map[string]ASRTranscriber, fallback string) *ASRRouter {
	return &ASRRouter{backends: backends, fallback: fallback}
}

// Route returns the backend for the given engine name, falling back to the default.
func (r *ASRRouter) Route(engine string) (ASRTranscriber, error) {
	backend, ok := r.backends[engine]
	if !ok {
		backend, ok = r.backends[r.fallback]
	}
	if !ok {
		return nil, fmt.Errorf("no ASR backend for engine %q", engine)
	}
	return backend, nil
}

// Transcribe routes to the correct backend.
func (r *ASRRouter) Transcribe(ctx context.Context, samples []float32, engine string) (*ASRResult, error) {
	backend, err := r.Route(engine)
	if err != nil {
		return nil, err
	}
	return backend.Transcribe(ctx, samples)
}

// Engines returns the names of all registered backends.
func (r *ASRRouter) Engines() []string {
	names := make([]string, 0, len(r.backends))
	for k := range r.backends {
		names = append(names, k)
	}
	return names
}

// --- whisper.cpp backend ---

// ASRClient sends audio to whisper.cpp server and returns transcriptions.
type ASRClient struct {
	url    string
	client *http.Client
}

// NewASRClient creates a client pointing at the whisper.cpp server URL.
func NewASRClient(url string, poolSize int) *ASRClient {
	return &ASRClient{
		url:    url,
		client: NewPooledHTTPClient(poolSize, 30*time.Second),
	}
}

// Warmup sends a tiny silent clip to the whisper server to verify it's responsive.
func (c *ASRClient) Warmup(ctx context.Context) error {
	silence := make([]float32, 16000) // 1 second of silence at 16kHz
	body, contentType, err := buildMultipartAudio(silence)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/inference", body)
	if err != nil {
		return fmt.Errorf("create warmup request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("asr warmup: %w", err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("asr warmup status %d", resp.StatusCode)
	}
	return nil
}

// Transcribe sends float32 audio samples (16kHz mono) to whisper.cpp and returns the transcript.
func (c *ASRClient) Transcribe(ctx context.Context, samples []float32) (*ASRResult, error) {
	start := time.Now()

	body, contentType, err := buildMultipartAudio(samples)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/inference", body)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("asr", "http").Inc()
		return nil, fmt.Errorf("asr request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		metrics.Errors.WithLabelValues("asr", "status").Inc()
		return nil, fmt.Errorf("asr status %d: %s", resp.StatusCode, string(respBody))
	}

	var whisperResp whisperResponse
	if err = json.NewDecoder(resp.Body).Decode(&whisperResp); err != nil {
		return nil, fmt.Errorf("decode asr response: %w", err)
	}

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("asr").Observe(latency.Seconds())

	return &ASRResult{
		Text:      whisperResp.Text,
		LatencyMs: float64(latency.Milliseconds()),
	}, nil
}

type whisperResponse struct {
	Text string `json:"text"`
}

// --- ROCm whisper backend (jjajjara/rocm-whisper-api /transcribe) ---

// ROCmWhisperClient sends audio to the ROCm-accelerated whisper API.
type ROCmWhisperClient struct {
	url    string
	client *http.Client
}

// NewROCmWhisperClient creates a client for the ROCm whisper server.
func NewROCmWhisperClient(url string, poolSize int) *ROCmWhisperClient {
	return &ROCmWhisperClient{
		url:    url,
		client: NewPooledHTTPClient(poolSize, 60*time.Second),
	}
}

// Transcribe sends audio to /transcribe as multipart form.
func (c *ROCmWhisperClient) Transcribe(ctx context.Context, samples []float32) (*ASRResult, error) {
	start := time.Now()

	body, contentType, err := buildMultipartAudio(samples)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+"/transcribe", body)
	if err != nil {
		return nil, fmt.Errorf("create rocm-whisper request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("asr", "http").Inc()
		return nil, fmt.Errorf("rocm-whisper request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		metrics.Errors.WithLabelValues("asr", "status").Inc()
		return nil, fmt.Errorf("rocm-whisper status %d: %s", resp.StatusCode, string(respBody))
	}

	var result whisperResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode rocm-whisper response: %w", err)
	}

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("asr").Observe(latency.Seconds())

	return &ASRResult{
		Text:      result.Text,
		LatencyMs: float64(latency.Milliseconds()),
	}, nil
}

// --- shared helpers ---

func buildMultipartAudio(samples []float32) (*bytes.Buffer, string, error) {
	wavData := audio.SamplesToWAV(samples, 16000)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return nil, "", fmt.Errorf("create form file: %w", err)
	}

	if _, err = part.Write(wavData); err != nil {
		return nil, "", fmt.Errorf("write wav data: %w", err)
	}

	if err = writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close writer: %w", err)
	}

	return &body, writer.FormDataContentType(), nil
}
