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
// Wraps the generic Router with an ASR-specific Transcribe convenience method.
type ASRRouter struct {
	*Router[ASRTranscriber]
}

// NewASRRouter creates a router with registered ASR backends and a fallback default.
func NewASRRouter(backends map[string]ASRTranscriber, fallback string) *ASRRouter {
	return &ASRRouter{Router: NewRouter(backends, fallback)}
}

// Transcribe routes to the correct backend and transcribes the audio.
func (r *ASRRouter) Transcribe(ctx context.Context, samples []float32, engine string) (*ASRResult, error) {
	backend, err := r.Route(engine)
	if err != nil {
		return nil, err
	}
	return backend.Transcribe(ctx, samples)
}

// MultipartASRClient sends audio as multipart WAV to any whisper-compatible HTTP endpoint.
// Different backends only vary by endpoint path (e.g. /inference for whisper.cpp,
// /transcribe for ROCm whisper). The label field is used in error messages and logs.
type MultipartASRClient struct {
	url      string
	endpoint string
	label    string
	client   *http.Client
}

// NewASRClient creates a client for whisper.cpp (/inference endpoint).
func NewASRClient(url string, poolSize int) *MultipartASRClient {
	return &MultipartASRClient{
		url:      url,
		endpoint: "/inference",
		label:    "whisper",
		client:   NewPooledHTTPClient(poolSize, 30*time.Second),
	}
}

// NewROCmWhisperClient creates a client for the ROCm whisper API (/transcribe endpoint).
func NewROCmWhisperClient(url string, poolSize int) *MultipartASRClient {
	return &MultipartASRClient{
		url:      url,
		endpoint: "/transcribe",
		label:    "rocm-whisper",
		client:   NewPooledHTTPClient(poolSize, 60*time.Second),
	}
}

// Warmup sends a tiny silent clip to verify the server is responsive.
func (c *MultipartASRClient) Warmup(ctx context.Context) error {
	silence := make([]float32, 16000) // 1 second of silence at 16kHz
	body, contentType, err := buildMultipartAudio(silence)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", c.url+c.endpoint, body)
	if err != nil {
		return fmt.Errorf("create warmup request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("%s warmup: %w", c.label, err)
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s warmup status %d", c.label, resp.StatusCode)
	}
	return nil
}

// Transcribe sends float32 audio samples (16kHz mono) as multipart WAV and returns the transcript.
func (c *MultipartASRClient) Transcribe(ctx context.Context, samples []float32) (*ASRResult, error) {
	start := time.Now()

	body, contentType, err := buildMultipartAudio(samples)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.url+c.endpoint, body)
	if err != nil {
		return nil, fmt.Errorf("create %s request: %w", c.label, err)
	}
	req.Header.Set("Content-Type", contentType)

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("asr", "http").Inc()
		return nil, fmt.Errorf("%s request: %w", c.label, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		metrics.Errors.WithLabelValues("asr", "status").Inc()
		return nil, fmt.Errorf("%s status %d: %s", c.label, resp.StatusCode, string(respBody))
	}

	var result whisperResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", c.label, err)
	}

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("asr").Observe(latency.Seconds())

	return &ASRResult{
		Text:      result.Text,
		LatencyMs: float64(latency.Milliseconds()),
	}, nil
}

type whisperResponse struct {
	Text string `json:"text"`
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
