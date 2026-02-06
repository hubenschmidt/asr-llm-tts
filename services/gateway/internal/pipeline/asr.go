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

// ASRResult holds the transcription output.
type ASRResult struct {
	Text      string  `json:"text"`
	LatencyMs float64 `json:"latency_ms"`
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
