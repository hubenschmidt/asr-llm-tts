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

// TTSSynthesizer produces audio from text.
type TTSSynthesizer interface {
	SynthesizeAudio(ctx context.Context, text string) ([]byte, error)
}

// TTSResult holds synthesized audio with timing.
type TTSResult struct {
	Audio     []byte  `json:"-"`
	LatencyMs float64 `json:"latency_ms"`
}

// TTSRouter dispatches to the correct TTS backend based on engine name.
type TTSRouter struct {
	backends map[string]TTSSynthesizer
	fallback string
}

// NewTTSRouter creates a router with registered backends.
func NewTTSRouter(backends map[string]TTSSynthesizer, fallback string) *TTSRouter {
	return &TTSRouter{backends: backends, fallback: fallback}
}

// Synthesize routes to the correct backend and wraps the result with timing.
func (r *TTSRouter) Synthesize(ctx context.Context, text, engine string) (*TTSResult, error) {
	start := time.Now()

	backend, ok := r.backends[engine]
	if !ok {
		backend, ok = r.backends[r.fallback]
	}
	if !ok {
		return nil, fmt.Errorf("no TTS backend for engine %q", engine)
	}

	audioData, err := backend.SynthesizeAudio(ctx, text)
	if err != nil {
		metrics.Errors.WithLabelValues("tts", "synth").Inc()
		return nil, err
	}

	latency := time.Since(start)
	metrics.StageDuration.WithLabelValues("tts").Observe(latency.Seconds())

	return &TTSResult{
		Audio:     audioData,
		LatencyMs: float64(latency.Milliseconds()),
	}, nil
}

// --- Piper backend ---

type piperSynthesizer struct {
	url    string
	voice  string
	client *http.Client
}

func NewPiperSynthesizer(url, voice string, client *http.Client) TTSSynthesizer {
	return &piperSynthesizer{url: url, voice: voice, client: client}
}

func (p *piperSynthesizer) SynthesizeAudio(ctx context.Context, text string) ([]byte, error) {
	body, err := json.Marshal(struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}{Text: text, Voice: p.voice})
	if err != nil {
		return nil, fmt.Errorf("marshal piper request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", p.url+"/synthesize", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create piper request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return doTTSRequest(p.client, req)
}

// --- OpenAI-compatible backend (Kokoro, Orpheus) ---

type openaiSynthesizer struct {
	url    string
	model  string
	voice  string
	client *http.Client
}

func NewOpenAISynthesizer(url, model, voice string, client *http.Client) TTSSynthesizer {
	return &openaiSynthesizer{url: url, model: model, voice: voice, client: client}
}

func (o *openaiSynthesizer) SynthesizeAudio(ctx context.Context, text string) ([]byte, error) {
	body, err := json.Marshal(struct {
		Input          string `json:"input"`
		Model          string `json:"model"`
		Voice          string `json:"voice"`
		ResponseFormat string `json:"response_format"`
	}{Input: text, Model: o.model, Voice: o.voice, ResponseFormat: "wav"})
	if err != nil {
		return nil, fmt.Errorf("marshal openai tts request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", o.url+"/v1/audio/speech", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create openai tts request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return doTTSRequest(o.client, req)
}

// HasEngine reports whether the router has a backend for the given engine name.
func (r *TTSRouter) HasEngine(engine string) bool {
	_, ok := r.backends[engine]
	return ok
}

// Engines returns the names of all registered backends.
func (r *TTSRouter) Engines() []string {
	names := make([]string, 0, len(r.backends))
	for k := range r.backends {
		names = append(names, k)
	}
	return names
}

// --- ElevenLabs backend ---

type elevenlabsSynthesizer struct {
	apiKey  string
	voiceID string
	modelID string
	client  *http.Client
}

func NewElevenLabsSynthesizer(apiKey, voiceID, modelID string, client *http.Client) TTSSynthesizer {
	return &elevenlabsSynthesizer{apiKey: apiKey, voiceID: voiceID, modelID: modelID, client: client}
}

func (e *elevenlabsSynthesizer) SynthesizeAudio(ctx context.Context, text string) ([]byte, error) {
	body, err := json.Marshal(struct {
		Text    string `json:"text"`
		ModelID string `json:"model_id"`
	}{Text: text, ModelID: e.modelID})
	if err != nil {
		return nil, fmt.Errorf("marshal elevenlabs request: %w", err)
	}

	url := fmt.Sprintf("https://api.elevenlabs.io/v1/text-to-speech/%s", e.voiceID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create elevenlabs request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("xi-api-key", e.apiKey)
	req.Header.Set("Accept", "audio/mpeg")

	return doTTSRequest(e.client, req)
}

// --- MeloTTS backend ---

type meloSynthesizer struct {
	url    string
	client *http.Client
}

func NewMeloSynthesizer(url string, client *http.Client) TTSSynthesizer {
	return &meloSynthesizer{url: url, client: client}
}

func (m *meloSynthesizer) SynthesizeAudio(ctx context.Context, text string) ([]byte, error) {
	body, err := json.Marshal(struct {
		Text      string  `json:"text"`
		Speed     float64 `json:"speed"`
		Language  string  `json:"language"`
		SpeakerID string  `json:"speaker_id"`
	}{Text: text, Speed: 1.0, Language: "EN", SpeakerID: "EN-Default"})
	if err != nil {
		return nil, fmt.Errorf("marshal melo request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", m.url+"/convert/tts", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create melo request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	return doTTSRequest(m.client, req)
}

// --- shared HTTP helper ---

func doTTSRequest(client *http.Client, req *http.Request) ([]byte, error) {
	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tts request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("tts status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
