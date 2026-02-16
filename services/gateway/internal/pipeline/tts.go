package pipeline

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// TTSOptions holds per-call TTS tuning parameters.
type TTSOptions struct {
	Speed float64
	Pitch float64
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
// If the backend supports SSML, wraps text with prosody/break tags.
func (r *TTSRouter) Synthesize(ctx context.Context, text, engine string, opts TTSOptions) (*TTSResult, error) {
	start := time.Now()

	backend, err := r.Route(engine)
	if err != nil {
		return nil, err
	}

	audioData, err := backend.SynthesizeAudio(ctx, text, opts)
	if err != nil {
		return nil, err
	}

	latency := time.Since(start)

	return &TTSResult{
		Audio:     audioData,
		LatencyMs: float64(latency.Milliseconds()),
	}, nil
}

// --- Piper backend (local neural TTS via piper CLI, returns WAV) ---

type piperSynthesizer struct {
	modelDir string
	voice    string
}

func NewPiperSynthesizer(modelDir, voice string) TTSSynthesizer {
	return &piperSynthesizer{modelDir: modelDir, voice: voice}
}

func (p *piperSynthesizer) SynthesizeAudio(ctx context.Context, text string, opts TTSOptions) ([]byte, error) {
	voice := p.voice
	if opts.Voice != "" {
		voice = opts.Voice
	}

	tmpFile, err := os.CreateTemp("", "piper-*.wav")
	if err != nil {
		return nil, fmt.Errorf("piper temp file: %w", err)
	}
	outPath := tmpFile.Name()
	tmpFile.Close()
	defer os.Remove(outPath)

	cmd := exec.CommandContext(ctx, "piper",
		"--model", filepath.Join(p.modelDir, voice+".onnx"),
		"--config", filepath.Join(p.modelDir, voice+".onnx.json"),
		"--output_file", outPath,
	)
	cmd.Stdin = strings.NewReader(text)

	output, err := cmd.CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("piper: %v\n%s", err, output)
	}

	return os.ReadFile(outPath)
}

