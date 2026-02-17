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
)

// ASROptions holds per-call ASR tuning parameters.
type ASROptions struct {
	Prompt string
}

// ASRTranscriber produces transcriptions from audio samples.
type ASRTranscriber interface {
	Transcribe(ctx context.Context, samples []float32, opts ASROptions) (*ASRResult, error)
}

// ASRResult holds the transcription output.
type ASRResult struct {
	Text         string  `json:"text"`
	LatencyMs    float64 `json:"latency_ms"`
	NoSpeechProb float64 `json:"no_speech_prob"`
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
func (r *ASRRouter) Transcribe(ctx context.Context, samples []float32, engine string, opts ASROptions) (*ASRResult, error) {
	backend, err := r.Route(engine)
	if err != nil {
		return nil, err
	}
	return backend.Transcribe(ctx, samples, opts)
}

// MultipartASRClient sends audio as multipart WAV to any whisper-compatible HTTP endpoint.
// Different backends only vary by endpoint path (e.g. /inference for whisper.cpp,
// /transcribe for ROCm whisper). The label field is used in error messages and logs.
type MultipartASRClient struct {
	url           string
	endpoint      string
	label         string
	defaultPrompt string // fallback prompt when ASROptions.Prompt is empty
	client        *http.Client
}

// NewASRClient creates a client for whisper.cpp (/inference endpoint).
func NewASRClient(url string, poolSize int, prompt string) *MultipartASRClient {
	return &MultipartASRClient{
		url:           url,
		endpoint:      "/inference",
		label:         "whisper",
		defaultPrompt: prompt,
		client:        NewPooledHTTPClient(poolSize, 30*time.Second),
	}
}

// Transcribe sends float32 audio samples (16kHz mono) as multipart WAV and returns the transcript.
func (c *MultipartASRClient) Transcribe(ctx context.Context, samples []float32, opts ASROptions) (*ASRResult, error) {
	start := time.Now()

	prompt := c.defaultPrompt
	if opts.Prompt != "" {
		prompt = opts.Prompt
	}

	body, contentType, err := buildMultipartAudio(samples, prompt)
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
		return nil, fmt.Errorf("%s request: %w", c.label, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("%s status %d: %s", c.label, resp.StatusCode, string(respBody))
	}

	var result whisperResponse
	if err = json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode %s response: %w", c.label, err)
	}

	latency := time.Since(start)

	return &ASRResult{
		Text:         result.Text,
		LatencyMs:    float64(latency.Milliseconds()),
		NoSpeechProb: result.NoSpeechProb,
	}, nil
}

type whisperResponse struct {
	Text         string  `json:"text"`
	NoSpeechProb float64 `json:"no_speech_prob"`
}

// --- shared helpers ---

func buildMultipartAudio(samples []float32, prompt string) (*bytes.Buffer, string, error) {
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

	if prompt != "" {
		if err = writer.WriteField("initial_prompt", prompt); err != nil {
			return nil, "", fmt.Errorf("write prompt field: %w", err)
		}
	}

	if err = writer.Close(); err != nil {
		return nil, "", fmt.Errorf("close writer: %w", err)
	}

	return &body, writer.FormDataContentType(), nil
}
