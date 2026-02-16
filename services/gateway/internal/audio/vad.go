package audio

import (
	"math"
	"time"
)

// VADConfig controls voice activity detection behavior.
type VADConfig struct {
	SpeechThresholdDB    float64
	SilenceTimeout       time.Duration
	MinSpeechDuration    time.Duration
	PreSpeechBuffer      time.Duration
	SampleRate           int
	CalibrationDuration  time.Duration // noise floor calibration window (0 = disabled)
	AdaptiveMarginDB     float64       // dB above noise floor for speech threshold
}

// DefaultVADConfig returns sensible defaults for call center audio.
func DefaultVADConfig() VADConfig {
	return VADConfig{
		SpeechThresholdDB:   -30,
		SilenceTimeout:      1000 * time.Millisecond,
		MinSpeechDuration:   500 * time.Millisecond,
		PreSpeechBuffer:     300 * time.Millisecond,
		SampleRate:          16000,
		CalibrationDuration: 500 * time.Millisecond,
		AdaptiveMarginDB:    10,
	}
}

// VAD implements energy-based voice activity detection with optional
// adaptive threshold calibration during the first N milliseconds.
type VAD struct {
	cfg            VADConfig
	isSpeech       bool
	speechStart    time.Time
	lastSpeechTime time.Time
	buffer         []float32
	preSpeech      []float32
	preSpeechLen   int

	// adaptive calibration
	calibrating        bool
	calibrationStart   time.Time
	calibrationReadings []float64
	threshold          float64
}

// NewVAD creates a VAD with the given config.
func NewVAD(cfg VADConfig) *VAD {
	preSpeechSamples := int(cfg.PreSpeechBuffer.Seconds() * float64(cfg.SampleRate))
	return &VAD{
		cfg:          cfg,
		preSpeechLen: preSpeechSamples,
		preSpeech:    make([]float32, 0, preSpeechSamples),
		calibrating:  cfg.CalibrationDuration > 0,
		threshold:    cfg.SpeechThresholdDB,
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

	if v.calibrating {
		v.calibrate(energyDB, now)
	}

	if energyDB >= v.threshold {
		return v.handleSpeech(samples, now)
	}
	return v.handleSilence(samples, now)
}

// calibrate collects energy readings during the calibration window, then
// computes the noise floor and sets the adaptive speech threshold.
func (v *VAD) calibrate(energyDB float64, now time.Time) {
	if v.calibrationStart.IsZero() {
		v.calibrationStart = now
	}
	v.calibrationReadings = append(v.calibrationReadings, energyDB)

	if now.Sub(v.calibrationStart) < v.cfg.CalibrationDuration {
		return
	}

	// Compute noise floor as average energy during calibration
	var sum float64
	for _, e := range v.calibrationReadings {
		sum += e
	}
	noiseFloor := sum / float64(len(v.calibrationReadings))

	adaptive := noiseFloor + v.cfg.AdaptiveMarginDB
	// Only adopt if it's stricter (higher) than the static default
	if adaptive > v.cfg.SpeechThresholdDB {
		v.threshold = adaptive
	}

	v.calibrating = false
	v.calibrationReadings = nil
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
