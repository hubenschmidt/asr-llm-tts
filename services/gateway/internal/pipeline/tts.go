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

// TTSOptions holds per-call TTS tuning parameters.
type TTSOptions struct {
	Speed float64
	Voice string
}

// TTSSynthesizer produces audio from text.
type TTSSynthesizer interface {
	SynthesizeAudio(ctx context.Context, text string, opts TTSOptions) ([]byte, error)
}

// TTSResult holds synthesized audio with timing.
type TTSResult struct {
	Audio     []byte  `json:"-"`
	LatencyMs float64 `json:"latency_ms"`
}

// TTSRouter dispatches to the correct TTS backend based on engine name.
// Wraps the generic Router with a TTS-specific Synthesize method that adds timing/metrics.
type TTSRouter struct {
	*Router[TTSSynthesizer]
}

// NewTTSRouter creates a router with registered TTS backends and a fallback default.
func NewTTSRouter(backends map[string]TTSSynthesizer, fallback string) *TTSRouter {
	return &TTSRouter{Router: NewRouter(backends, fallback)}
}

// Synthesize routes to the correct backend, synthesizes audio, and records latency metrics.
func (r *TTSRouter) Synthesize(ctx context.Context, text, engine string, opts TTSOptions) (*TTSResult, error) {
	start := time.Now()

	backend, err := r.Route(engine)
	if err != nil {
		return nil, err
	}

	audioData, err := backend.SynthesizeAudio(ctx, text, opts)
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

// --- Piper backend (local neural TTS via piper-tts, returns WAV) ---

type piperSynthesizer struct {
	url    string
	voice  string
	client *http.Client
}

func NewPiperSynthesizer(url, voice string, client *http.Client) TTSSynthesizer {
	return &piperSynthesizer{url: url, voice: voice, client: client}
}

func (p *piperSynthesizer) SynthesizeAudio(ctx context.Context, text string, opts TTSOptions) ([]byte, error) {
	voice := p.voice
	if opts.Voice != "" {
		voice = opts.Voice
	}
	body, err := json.Marshal(struct {
		Text  string `json:"text"`
		Voice string `json:"voice"`
	}{Text: text, Voice: voice})
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

// --- OpenAI-compatible backend (Kokoro, Orpheus — any server exposing /v1/audio/speech) ---

type openaiSynthesizer struct {
	url    string
	model  string
	voice  string
	client *http.Client
}

func NewOpenAISynthesizer(url, model, voice string, client *http.Client) TTSSynthesizer {
	return &openaiSynthesizer{url: url, model: model, voice: voice, client: client}
}

func (o *openaiSynthesizer) SynthesizeAudio(ctx context.Context, text string, opts TTSOptions) ([]byte, error) {
	voice := o.voice
	if opts.Voice != "" {
		voice = opts.Voice
	}
	body, err := json.Marshal(struct {
		Input          string  `json:"input"`
		Model          string  `json:"model"`
		Voice          string  `json:"voice"`
		Speed          float64 `json:"speed,omitempty"`
		ResponseFormat string  `json:"response_format"`
	}{Input: text, Model: o.model, Voice: voice, Speed: opts.Speed, ResponseFormat: "wav"})
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

// --- ElevenLabs backend (cloud API, returns MP3 via api.elevenlabs.io) ---

type elevenlabsSynthesizer struct {
	apiKey  string
	voiceID string
	modelID string
	client  *http.Client
}

func NewElevenLabsSynthesizer(apiKey, voiceID, modelID string, client *http.Client) TTSSynthesizer {
	return &elevenlabsSynthesizer{apiKey: apiKey, voiceID: voiceID, modelID: modelID, client: client}
}

func (e *elevenlabsSynthesizer) SynthesizeAudio(ctx context.Context, text string, _ TTSOptions) ([]byte, error) {
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

// --- MeloTTS backend (self-hosted multilingual TTS, /convert/tts endpoint) ---

type meloSynthesizer struct {
	url    string
	client *http.Client
}

func NewMeloSynthesizer(url string, client *http.Client) TTSSynthesizer {
	return &meloSynthesizer{url: url, client: client}
}

func (m *meloSynthesizer) SynthesizeAudio(ctx context.Context, text string, opts TTSOptions) ([]byte, error) {
	speed := opts.Speed
	if speed <= 0 {
		speed = 1.0
	}
	body, err := json.Marshal(struct {
		Text      string  `json:"text"`
		Speed     float64 `json:"speed"`
		Language  string  `json:"language"`
		SpeakerID string  `json:"speaker_id"`
	}{Text: text, Speed: speed, Language: "EN", SpeakerID: "EN-Default"})
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
