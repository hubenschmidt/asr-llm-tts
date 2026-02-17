package pipeline

import (
	"context"
	"encoding/binary"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/denoise"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/trace"
)

const (
	// emotionClassifyTimeout caps how long the fire-and-forget emotion
	// classification goroutine waits before giving up.
	emotionClassifyTimeout = 5 * time.Second

	// defaultConfidenceThreshold is the no-speech probability above which
	// an ASR result is discarded as noise.
	defaultConfidenceThreshold = 0.6

	// sentenceChannelBuffer is how many complete sentences can queue between
	// the LLM producer and the TTS consumer before back-pressure kicks in.
	sentenceChannelBuffer = 4

	// ttsSilenceSampleRate is the sample rate used when generating
	// inter-sentence silence WAV chunks.
	ttsSilenceSampleRate = 24000
)

// silenceWAV generates a minimal WAV file of silence for the given duration and sample rate.
func silenceWAV(ms, sampleRate int) []byte {
	numSamples := sampleRate * ms / 1000
	dataSize := numSamples * 2 // 16-bit mono
	buf := make([]byte, 44+dataSize)

	copy(buf[0:4], "RIFF")
	binary.LittleEndian.PutUint32(buf[4:8], uint32(36+dataSize))
	copy(buf[8:12], "WAVE")
	copy(buf[12:16], "fmt ")
	binary.LittleEndian.PutUint32(buf[16:20], 16) // PCM chunk size
	binary.LittleEndian.PutUint16(buf[20:22], 1)  // PCM format
	binary.LittleEndian.PutUint16(buf[22:24], 1)  // mono
	binary.LittleEndian.PutUint32(buf[24:28], uint32(sampleRate))
	binary.LittleEndian.PutUint32(buf[28:32], uint32(sampleRate*2)) // byte rate
	binary.LittleEndian.PutUint16(buf[32:34], 2)                   // block align
	binary.LittleEndian.PutUint16(buf[34:36], 16)                  // bits per sample
	copy(buf[36:40], "data")
	binary.LittleEndian.PutUint32(buf[40:44], uint32(dataSize))
	// samples are already zero (silence)
	return buf
}

// Config holds pipeline configuration.
type Config struct {
	ASRClient           *ASRRouter
	LLMClient           *AgentLLM
	TTSClient           *TTSRouter
	VADConfig           audio.VADConfig
	SessionID           string
	SystemPrompt        string
	LLMModel            string
	LLMEngine           string
	Denoiser            *denoise.Denoiser
	NoiseSuppression    bool
	ASRPrompt            string
	ConfidenceThreshold  float64
	ReferenceTranscript  string
	TTSSpeed             float64
	TTSPitch             float64
	TextNormalization    bool
	InterSentencePauseMs int
	ClassifyClient       *ClassifyClient
	AudioClassification  bool
	Tracer               *trace.Tracer
}

// turn holds one user→assistant exchange for conversation history.
type turn struct {
	user      string
	assistant string
}

// Pipeline processes a single call session through ASR → LLM → TTS.
type Pipeline struct {
	cfg        Config
	vad        *audio.VAD
	history    []turn
	snippetBuf []float32
}

// New creates a pipeline for a single call session.
func New(cfg Config) *Pipeline {
	return &Pipeline{
		cfg: cfg,
		vad: audio.NewVAD(cfg.VADConfig),
	}
}

// Event represents a pipeline output sent back to the client.
type Event struct {
	Type            string  `json:"type"`
	Text            string  `json:"text,omitempty"`
	Token           string  `json:"token,omitempty"`
	ASRMs           float64 `json:"asr_ms,omitempty"`
	LLMMs           float64 `json:"llm_ms,omitempty"`
	TTSMs           float64 `json:"tts_ms,omitempty"`
	TotalMs         float64 `json:"total_ms,omitempty"`
	LatencyMs       float64 `json:"latency_ms,omitempty"`
	NoSpeechProb    float64 `json:"no_speech_prob"`
	WER             float64 `json:"wer"`
	NoiseSuppressed bool            `json:"noise_suppressed"`
	Emotion         *ClassifyResult `json:"emotion,omitempty"`
	Audio           []byte          `json:"-"`
}

// EventCallback is invoked for each pipeline event (transcript, token, audio, metrics).
type EventCallback func(Event)

// ProcessChunk decodes, resamples, and VAD-processes an audio chunk.
// If the VAD detects end-of-speech, runs the full ASR → LLM → TTS pipeline.
func (p *Pipeline) ProcessChunk(ctx context.Context, data []byte, codec audio.Codec, sampleRate int, ttsEngine, asrEngine string, onEvent EventCallback) error {
	samples, srcRate, err := audio.Decode(data, codec, sampleRate)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	resampled := audio.Resample(samples, srcRate, 16000)

	// RNNoise expects 48 kHz internally and resamples from 16 kHz+.
	// G.711 input arrives at 8 kHz — too low for RNNoise, so skip denoising.
	if p.cfg.Denoiser != nil && srcRate >= 16000 {
		resampled = p.cfg.Denoiser.Denoise(resampled)
	}

	result := p.vad.Process(resampled)

	if !result.SpeechEnded {
		return nil
	}

	return p.runFullPipeline(ctx, result.Audio, ttsEngine, asrEngine, onEvent)
}

// ProcessChunkNoVAD decodes and resamples audio, appending to the snippet buffer
// without VAD processing. Used in snippet mode.
func (p *Pipeline) ProcessChunkNoVAD(data []byte, codec audio.Codec, sampleRate int) error {
	samples, srcRate, err := audio.Decode(data, codec, sampleRate)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	resampled := audio.Resample(samples, srcRate, 16000)
	p.snippetBuf = append(p.snippetBuf, resampled...)
	return nil
}

// ProcessBuffered runs the full pipeline on accumulated snippet audio, then clears the buffer.
func (p *Pipeline) ProcessBuffered(ctx context.Context, ttsEngine, asrEngine string, onEvent EventCallback) error {
	if len(p.snippetBuf) == 0 {
		return nil
	}

	buf := p.snippetBuf
	p.snippetBuf = nil
	return p.runFullPipeline(ctx, buf, ttsEngine, asrEngine, onEvent)
}

// ProcessTextMessage runs LLM-only pipeline for a typed chat message (no ASR, no TTS).
func (p *Pipeline) ProcessTextMessage(ctx context.Context, message string, onEvent EventCallback) error {
	message = strings.TrimSpace(message)
	if message == "" {
		return nil
	}

	llmInput := p.formatInput(message)

	llmResult, err := p.cfg.LLMClient.Chat(ctx, llmInput, p.cfg.SystemPrompt, p.cfg.LLMModel, p.cfg.LLMEngine, func(token string) {
		onEvent(Event{Type: "llm_token", Token: token})
	})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	slog.Info("chat_response", "text", llmResult.Text, "llm_ms", llmResult.LatencyMs)
	onEvent(Event{Type: "llm_done", Text: llmResult.Text, LatencyMs: llmResult.LatencyMs})
	if llmResult.Thinking != "" {
		onEvent(Event{Type: "thinking_done", Text: llmResult.Thinking})
	}

	p.history = append(p.history, turn{user: message, assistant: llmResult.Text})

	onEvent(Event{Type: "metrics", LLMMs: llmResult.LatencyMs})
	return nil
}

// Flush processes any remaining buffered audio in the VAD.
func (p *Pipeline) Flush(ctx context.Context, ttsEngine, asrEngine string, onEvent EventCallback) error {
	remaining := p.vad.Flush()
	if len(remaining) == 0 {
		return nil
	}

	return p.runFullPipeline(ctx, remaining, ttsEngine, asrEngine, onEvent)
}

// runFullPipeline executes the complete ASR → LLM → TTS chain for one speech segment.
// ASR must complete first to produce the transcript.
// LLM and TTS run concurrently via sentence pipelining (see streamLLMWithTTS).
func (p *Pipeline) runFullPipeline(ctx context.Context, speechAudio []float32, ttsEngine, asrEngine string, onEvent EventCallback) error {
	e2eStart := time.Now()

	runID := ""
	if p.cfg.Tracer != nil {
		runID = p.cfg.Tracer.StartRun()
	}

	// Audio classification — fire-and-forget, parallel to ASR
	if p.cfg.AudioClassification && p.cfg.ClassifyClient != nil {
		audioSnap := make([]float32, len(speechAudio))
		copy(audioSnap, speechAudio)
		emotionCtx, emotionCancel := context.WithTimeout(context.Background(), emotionClassifyTimeout)
		go func() { defer emotionCancel(); p.classifyEmotion(emotionCtx, audioSnap, onEvent, runID) }()
	}

	transcript, asrResult, err := p.runASR(ctx, speechAudio, asrEngine, runID)
	if err != nil {
		p.endRun(runID, e2eStart, "", "", "error")
		return fmt.Errorf("asr: %w", err)
	}
	if transcript == "" {
		p.endRun(runID, e2eStart, asrResult.Text, "", "filtered")
		return nil
	}

	slog.Info("transcript", "text", transcript, "asr_ms", asrResult.LatencyMs, "no_speech_prob", asrResult.NoSpeechProb)
	onEvent(Event{Type: "transcript", Text: transcript, LatencyMs: asrResult.LatencyMs})

	wer := p.evaluateWER(transcript, asrResult)

	// LLM→TTS sentence pipelining
	llmInput := p.formatInput(transcript)
	ttsLatencyMs, llmResult, err := p.streamLLMWithTTS(ctx, llmInput, ttsEngine, onEvent, runID)
	if err != nil {
		p.endRun(runID, e2eStart, transcript, "", "error")
		return fmt.Errorf("llm+tts: %w", err)
	}

	p.history = append(p.history, turn{user: transcript, assistant: llmResult.Text})
	e2eLatency := time.Since(e2eStart)
	slog.Info("pipeline_done", "e2e_ms", e2eLatency.Milliseconds(), "asr_ms", asrResult.LatencyMs, "llm_ms", llmResult.LatencyMs, "tts_ms", ttsLatencyMs)

	onEvent(Event{
		Type:            "metrics",
		ASRMs:           asrResult.LatencyMs,
		LLMMs:           llmResult.LatencyMs,
		TTSMs:           ttsLatencyMs,
		TotalMs:         float64(e2eLatency.Milliseconds()),
		NoSpeechProb:    asrResult.NoSpeechProb,
		WER:             wer,
		NoiseSuppressed: p.cfg.NoiseSuppression,
	})

	p.endRun(runID, e2eStart, transcript, llmResult.Text, "ok")
	return nil
}

// runASR transcribes speech audio and filters noise/low-confidence results.
// Returns empty transcript if filtered.
func (p *Pipeline) runASR(ctx context.Context, speechAudio []float32, asrEngine, runID string) (string, *ASRResult, error) {
	asrStart := time.Now()
	asrResult, err := p.cfg.ASRClient.Transcribe(ctx, speechAudio, asrEngine, ASROptions{Prompt: p.cfg.ASRPrompt})
	asrOutput := ""
	if asrResult != nil {
		asrOutput = asrResult.Text
	}
	p.traceSpan(runID, "asr", asrStart, fmt.Sprintf("audio_samples=%d", len(speechAudio)), asrOutput, err)
	if err != nil {
		return "", nil, err
	}

	transcript := strings.TrimSpace(asrResult.Text)
	threshold := p.cfg.ConfidenceThreshold
	if threshold == 0 {
		threshold = defaultConfidenceThreshold
	}
	if transcript == "" || asrResult.NoSpeechProb > threshold || isNoiseTranscript(transcript) {
		return "", asrResult, nil
	}
	return transcript, asrResult, nil
}

// evaluateWER computes word error rate against the reference transcript, if configured.
func (p *Pipeline) evaluateWER(transcript string, asrResult *ASRResult) float64 {
	if p.cfg.ReferenceTranscript == "" {
		return -1
	}
	wer := ComputeWER(p.cfg.ReferenceTranscript, transcript)
	slog.Info("transcript_eval",
		"session_id", p.cfg.SessionID,
		"reference", p.cfg.ReferenceTranscript,
		"hypothesis", transcript,
		"wer", wer,
		"no_speech_prob", asrResult.NoSpeechProb,
		"asr_ms", asrResult.LatencyMs,
		"noise_suppression", p.cfg.NoiseSuppression,
	)
	return wer
}

// traceSpan records a completed span if tracing is enabled.
func (p *Pipeline) traceSpan(runID, name string, start time.Time, input, output string, err error) {
	if p.cfg.Tracer == nil || runID == "" {
		return
	}
	status, errMsg := "ok", ""
	if err != nil {
		status, errMsg = "error", err.Error()
	}
	p.cfg.Tracer.RecordSpan(runID, name, start, float64(time.Since(start).Milliseconds()), input, output, status, errMsg)
}

func (p *Pipeline) endRun(runID string, start time.Time, transcript, response, status string) {
	if p.cfg.Tracer == nil {
		return
	}
	p.cfg.Tracer.EndRun(runID, float64(time.Since(start).Milliseconds()), transcript, response, status)
}

// noisePatterns are common ASR hallucinations from background noise.
var noisePatterns = map[string]bool{
	"crunching": true, "static": true, "silence": true, "noise": true,
	"inaudible": true, "unintelligible": true, "background noise": true,
	"music": true, "typing": true, "breathing": true, "sigh": true,
	"cough": true, "sneeze": true, "laughter": true, "applause": true,
	"you": true, "the": true, "a": true, "um": true, "uh": true,
	"hmm": true, "ah": true, "oh": true, "mhm": true,
}

// isNoiseTranscript returns true if the ASR output is likely background noise.
func isNoiseTranscript(text string) bool {
	// Asterisk-wrapped text like *crunching*, *static*
	if strings.HasPrefix(text, "*") && strings.HasSuffix(text, "*") {
		return true
	}
	// Bracket-wrapped like [noise], [inaudible]
	if strings.HasPrefix(text, "[") && strings.HasSuffix(text, "]") {
		return true
	}
	// Parentheses-wrapped like (crunching)
	if strings.HasPrefix(text, "(") && strings.HasSuffix(text, ")") {
		return true
	}
	// Known noise words (short transcripts only)
	lower := strings.ToLower(text)
	return noisePatterns[lower]
}

// formatInput prepends conversation history to the current message.
func (p *Pipeline) formatInput(current string) string {
	if len(p.history) == 0 {
		return current
	}
	var b strings.Builder
	for _, t := range p.history {
		fmt.Fprintf(&b, "User: %s\nAssistant: %s\n", t.user, t.assistant)
	}
	fmt.Fprintf(&b, "User: %s", current)
	return b.String()
}

func (p *Pipeline) classifyEmotion(ctx context.Context, samples []float32, onEvent EventCallback, runID string) {
	start := time.Now()
	result, err := p.cfg.ClassifyClient.ClassifyEmotion(ctx, samples)
	out := ""
	if result != nil {
		out = fmt.Sprintf("label=%s conf=%.2f", result.Label, result.Confidence)
	}
	p.traceSpan(runID, "emotion_classify", start, fmt.Sprintf("samples=%d", len(samples)), out, err)
	if err != nil {
		slog.Warn("emotion classification failed", "error", err)
		return
	}
	onEvent(Event{Type: "classification", Emotion: result})
}

// streamLLMWithTTS runs LLM streaming and TTS synthesis concurrently using a
// producer/consumer pattern. The LLM streams tokens into a sentenceBuffer (producer);
// when a sentence boundary is detected, the complete sentence is sent to a channel.
// A goroutine (consumer) reads sentences and synthesizes audio via TTS in parallel,
// so the first TTS audio is ready before the LLM finishes generating.
func (p *Pipeline) streamLLMWithTTS(ctx context.Context, transcript, ttsEngine string, onEvent EventCallback, runID string) (float64, *LLMResult, error) {
	sentenceCh := make(chan string, sentenceChannelBuffer)
	var ttsWg sync.WaitGroup
	var totalTTSMs float64
	var ttsMu sync.Mutex

	// TTS consumer goroutine — synthesizes each sentence as it arrives
	ttsWg.Add(1)
	go func() {
		defer ttsWg.Done()
		p.consumeSentences(ctx, sentenceCh, ttsEngine, onEvent, &totalTTSMs, &ttsMu, runID)
	}()

	// LLM producer — stream content tokens, split at sentence boundaries.
	// Code blocks (``` fenced) are sent to the frontend but omitted from TTS.
	var sentenceBuf sentenceBuffer
	var codeFilt codeFilter

	llmStart := time.Now()
	llmResult, err := p.cfg.LLMClient.Chat(ctx, transcript, p.cfg.SystemPrompt, p.cfg.LLMModel, p.cfg.LLMEngine, func(token string) {
		onEvent(Event{Type: "llm_token", Token: token})
		filtered := codeFilt.Filter(token)
		if filtered == "" {
			return
		}
		s := sentenceBuf.Add(filtered)
		if s != "" {
			sentenceCh <- s
		}
	})

	// Flush remaining text from sentence buffer
	remainder := sentenceBuf.Flush()
	if remainder != "" {
		sentenceCh <- remainder
	}
	close(sentenceCh)
	ttsWg.Wait()

	llmOutput := ""
	if llmResult != nil {
		llmOutput = llmResult.Text
	}
	p.traceSpan(runID, "llm", llmStart, transcript, llmOutput, err)

	if err != nil {
		return 0, nil, err
	}

	slog.Info("llm_response", "text", llmResult.Text, "thinking_len", len(llmResult.Thinking), "llm_ms", llmResult.LatencyMs, "ttft_ms", llmResult.TimeToFirstTokenMs)
	onEvent(Event{Type: "llm_done", Text: llmResult.Text, LatencyMs: llmResult.LatencyMs})
	if llmResult.Thinking != "" {
		onEvent(Event{Type: "thinking_done", Text: llmResult.Thinking})
	}

	ttsMu.Lock()
	ttsMs := totalTTSMs
	ttsMu.Unlock()

	return ttsMs, llmResult, nil
}

func (p *Pipeline) consumeSentences(ctx context.Context, sentenceCh <-chan string, ttsEngine string, onEvent EventCallback, totalMs *float64, mu *sync.Mutex, runID string) {
	ttsOpts := TTSOptions{Speed: p.cfg.TTSSpeed, Pitch: p.cfg.TTSPitch}
	for sentence := range sentenceCh {
		if err := p.synthesizeSentence(ctx, sentence, ttsEngine, ttsOpts, onEvent, totalMs, mu, runID); err != nil {
			return
		}
	}
}

func (p *Pipeline) synthesizeSentence(ctx context.Context, sentence, ttsEngine string, ttsOpts TTSOptions, onEvent EventCallback, totalMs *float64, mu *sync.Mutex, runID string) error {
	sentence = StripMarkdown(sentence)
	if sentence == "" {
		return nil
	}
	if p.cfg.TextNormalization {
		sentence = NormalizeForSpeech(sentence)
	}

	ttsStart := time.Now()
	ttsResult, err := p.cfg.TTSClient.Synthesize(ctx, sentence, ttsEngine, ttsOpts)
	ttsOutput := ""
	if ttsResult != nil {
		ttsOutput = fmt.Sprintf("audio_bytes=%d", len(ttsResult.Audio))
	}
	p.traceSpan(runID, "tts", ttsStart, sentence, ttsOutput, err)
	if err != nil {
		slog.Error("tts sentence", "error", err, "text", sentence)
		onEvent(Event{Type: "error", Text: err.Error()})
		return err
	}

	mu.Lock()
	*totalMs += ttsResult.LatencyMs
	mu.Unlock()
	onEvent(Event{Type: "tts_ready", Audio: ttsResult.Audio, LatencyMs: ttsResult.LatencyMs})

	if p.cfg.InterSentencePauseMs > 0 {
		onEvent(Event{Type: "tts_ready", Audio: silenceWAV(p.cfg.InterSentencePauseMs, ttsSilenceSampleRate)})
	}
	return nil
}
