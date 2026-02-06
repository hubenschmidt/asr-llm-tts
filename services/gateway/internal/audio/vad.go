package audio

import (
	"math"
	"time"
)

// VADConfig controls voice activity detection behavior.
type VADConfig struct {
	SpeechThresholdDB float64
	SilenceTimeout    time.Duration
	MinSpeechDuration time.Duration
	PreSpeechBuffer   time.Duration
	SampleRate        int
}

// DefaultVADConfig returns sensible defaults for call center audio.
func DefaultVADConfig() VADConfig {
	return VADConfig{
		SpeechThresholdDB: -30,
		SilenceTimeout:    1000 * time.Millisecond,
		MinSpeechDuration: 500 * time.Millisecond,
		PreSpeechBuffer:   300 * time.Millisecond,
		SampleRate:        16000,
	}
}

// VAD implements energy-based voice activity detection.
type VAD struct {
	cfg            VADConfig
	isSpeech       bool
	speechStart    time.Time
	lastSpeechTime time.Time
	buffer         []float32
	preSpeech      []float32
	preSpeechLen   int
}

// NewVAD creates a VAD with the given config.
func NewVAD(cfg VADConfig) *VAD {
	preSpeechSamples := int(cfg.PreSpeechBuffer.Seconds() * float64(cfg.SampleRate))
	return &VAD{
		cfg:          cfg,
		preSpeechLen: preSpeechSamples,
		preSpeech:    make([]float32, 0, preSpeechSamples),
	}
}

// VADResult holds the output of processing an audio chunk.
type VADResult struct {
	SpeechEnded bool
	Audio       []float32
}

// Process feeds an audio chunk into the VAD and returns completed speech segments.
func (v *VAD) Process(samples []float32) VADResult {
	energyDB := computeEnergyDB(samples)
	now := time.Now()

	if energyDB >= v.cfg.SpeechThresholdDB {
		return v.handleSpeech(samples, now)
	}
	return v.handleSilence(samples, now)
}

func (v *VAD) handleSpeech(samples []float32, now time.Time) VADResult {
	if !v.isSpeech {
		v.isSpeech = true
		v.speechStart = now
		v.buffer = append(v.buffer, v.preSpeech...)
	}
	v.lastSpeechTime = now
	v.buffer = append(v.buffer, samples...)
	v.preSpeech = v.preSpeech[:0]
	return VADResult{}
}

func (v *VAD) handleSilence(samples []float32, now time.Time) VADResult {
	v.updatePreSpeech(samples)

	if !v.isSpeech {
		return VADResult{}
	}

	v.buffer = append(v.buffer, samples...)

	silenceDur := now.Sub(v.lastSpeechTime)
	speechDur := now.Sub(v.speechStart)

	if silenceDur < v.cfg.SilenceTimeout {
		return VADResult{}
	}

	v.isSpeech = false

	if speechDur < v.cfg.MinSpeechDuration {
		v.buffer = v.buffer[:0]
		return VADResult{}
	}

	audio := v.buffer
	v.buffer = nil
	return VADResult{SpeechEnded: true, Audio: audio}
}

func (v *VAD) updatePreSpeech(samples []float32) {
	v.preSpeech = append(v.preSpeech, samples...)
	if len(v.preSpeech) > v.preSpeechLen {
		excess := len(v.preSpeech) - v.preSpeechLen
		v.preSpeech = v.preSpeech[excess:]
	}
}

// Flush returns any buffered speech audio and resets the VAD.
func (v *VAD) Flush() []float32 {
	if len(v.buffer) == 0 {
		return nil
	}
	audio := v.buffer
	v.buffer = nil
	v.isSpeech = false
	return audio
}

func computeEnergyDB(samples []float32) float64 {
	if len(samples) == 0 {
		return -100
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	rms := math.Sqrt(sum / float64(len(samples)))
	if rms < 1e-10 {
		return -100
	}
	return 20 * math.Log10(rms)
}
