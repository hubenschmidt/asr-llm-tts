package pipeline

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// TTSClient synthesizes speech from text via Piper HTTP API.
// Supports "fast" (low quality voice) and "quality" (medium quality voice) modes.
type TTSClient struct {
	piperURL string
	client   *http.Client
}

// NewTTSClient creates a TTS client pointing at the Piper service.
func NewTTSClient(piperURL string, poolSize int) *TTSClient {
	return &TTSClient{
		piperURL: piperURL,
		client:   NewPooledHTTPClient(poolSize, 30*time.Second),
	}
}

// Voice models mapped by engine mode.
var voiceModels = map[string]string{
	"fast":    "en_US-lessac-low",
	"quality": "en_US-lessac-medium",
	"piper":   "en_US-lessac-low",
	"coqui":   "en_US-lessac-medium",
}

// TTSResult holds synthesized audio with timing.
type TTSResult struct {
	Audio     []byte  `json:"-"`
	LatencyMs float64 `json:"latency_ms"`
}

// Synthesize converts text to speech. Engine selects voice: "fast" or "quality".
func (c *TTSClient) Synthesize(ctx context.Context, text, engine string) (*TTSResult, error) {
	start := time.Now()

	voice := resolveVoice(engine)

	reqBody, err := json.Marshal(ttsRequest{Text: text, Voice: voice})
	if err != nil {
		return nil, fmt.Errorf("marshal tts request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.piperURL+"/synthesize", bytes.NewReader(reqBody))
	if err != nil {
		return nil, fmt.Errorf("create tts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.client.Do(req)
	if err != nil {
		metrics.Errors.WithLabelValues("tts", "http").Inc()
		return nil, fmt.Errorf("tts request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		metrics.Errors.WithLabelValues("tts", "status").Inc()
		return nil, fmt.Errorf("tts status %d", resp.StatusCode)
	}

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read tts response: %w", err)
	}

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("tts").Observe(latency.Seconds())

	return &TTSResult{
		Audio:     audioData,
		LatencyMs: float64(latency.Milliseconds()),
	}, nil
}

func resolveVoice(engine string) string {
	voice, ok := voiceModels[engine]
	if !ok {
		return voiceModels["fast"]
	}
	return voice
}

type ttsRequest struct {
	Text  string `json:"text"`
	Voice string `json:"voice"`
}
