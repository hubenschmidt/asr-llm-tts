package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/audio"
	"github.com/hubenschmidt/asr-llm-tts-poc/gateway/internal/metrics"
)

// Config holds pipeline configuration.
type Config struct {
	ASRClient    *ASRRouter
	LLMClient    *AgentLLM
	TTSClient    *TTSRouter
	VADConfig    audio.VADConfig
	RAGClient    *RAGClient
	CallHistory  *CallHistoryClient
	SessionID    string
	SystemPrompt string
	LLMModel     string
	LLMEngine    string
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
	Type      string  `json:"type"`
	Text      string  `json:"text,omitempty"`
	Token     string  `json:"token,omitempty"`
	ASRMs     float64 `json:"asr_ms,omitempty"`
	LLMMs     float64 `json:"llm_ms,omitempty"`
	TTSMs     float64 `json:"tts_ms,omitempty"`
	TotalMs   float64 `json:"total_ms,omitempty"`
	LatencyMs float64 `json:"latency_ms,omitempty"`
	Audio     []byte  `json:"-"`
}

// EventCallback is invoked for each pipeline event (transcript, token, audio, metrics).
type EventCallback func(Event)

// ProcessChunk decodes, resamples, and VAD-processes an audio chunk.
// If the VAD detects end-of-speech, runs the full ASR → LLM → TTS pipeline.
func (p *Pipeline) ProcessChunk(ctx context.Context, data []byte, codec audio.Codec, sampleRate int, ttsEngine, sttEngine string, onEvent EventCallback) error {
	metrics.AudioChunks.Inc()

	samples, srcRate, err := audio.Decode(data, codec, sampleRate)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	resampled := audio.Resample(samples, srcRate, 16000)
	result := p.vad.Process(resampled)

	if !result.SpeechEnded {
		return nil
	}

	metrics.SpeechSegments.Inc()
	return p.runFullPipeline(ctx, result.Audio, ttsEngine, sttEngine, onEvent)
}

// ProcessChunkNoVAD decodes and resamples audio, appending to the snippet buffer
// without VAD processing. Used in snippet mode.
func (p *Pipeline) ProcessChunkNoVAD(data []byte, codec audio.Codec, sampleRate int) error {
	metrics.AudioChunks.Inc()

	samples, srcRate, err := audio.Decode(data, codec, sampleRate)
	if err != nil {
		return fmt.Errorf("decode: %w", err)
	}

	resampled := audio.Resample(samples, srcRate, 16000)
	p.snippetBuf = append(p.snippetBuf, resampled...)
	return nil
}

// ProcessBuffered runs the full pipeline on accumulated snippet audio, then clears the buffer.
func (p *Pipeline) ProcessBuffered(ctx context.Context, ttsEngine, sttEngine string, onEvent EventCallback) error {
	if len(p.snippetBuf) == 0 {
		return nil
	}

	buf := p.snippetBuf
	p.snippetBuf = nil
	metrics.SpeechSegments.Inc()
	return p.runFullPipeline(ctx, buf, ttsEngine, sttEngine, onEvent)
}

// Flush processes any remaining buffered audio in the VAD.
func (p *Pipeline) Flush(ctx context.Context, ttsEngine, sttEngine string, onEvent EventCallback) error {
	remaining := p.vad.Flush()
	if len(remaining) == 0 {
		return nil
	}

	metrics.SpeechSegments.Inc()
	return p.runFullPipeline(ctx, remaining, ttsEngine, sttEngine, onEvent)
}

// runFullPipeline executes the complete ASR → RAG → LLM → TTS chain for one speech segment.
// ASR must complete first to produce the transcript. RAG retrieval is best-effort (non-fatal).
// LLM and TTS run concurrently via sentence pipelining (see streamLLMWithTTS).
func (p *Pipeline) runFullPipeline(ctx context.Context, speechAudio []float32, ttsEngine, sttEngine string, onEvent EventCallback) error {
	e2eStart := time.Now()

	// ASR — must complete before LLM can start
	asrResult, err := p.cfg.ASRClient.Transcribe(ctx, speechAudio, sttEngine)
	if err != nil {
		return fmt.Errorf("asr: %w", err)
	}

	transcript := strings.TrimSpace(asrResult.Text)
	if transcript == "" {
		return nil
	}

	slog.Info("transcript", "text", transcript, "asr_ms", asrResult.LatencyMs)
	onEvent(Event{Type: "transcript", Text: transcript, LatencyMs: asrResult.LatencyMs})

	// RAG — retrieve relevant context (non-fatal on error)
	ragContext := p.retrieveRAGContext(ctx, transcript)

	// Build input with conversation history for multi-turn context
	llmInput := p.formatInput(transcript)

	// LLM→TTS sentence pipelining: TTS starts on each sentence while LLM keeps generating
	ttsLatencyMs, llmResult, err := p.streamLLMWithTTS(ctx, llmInput, ragContext, ttsEngine, onEvent)
	if err != nil {
		return fmt.Errorf("llm+tts: %w", err)
	}

	// Track conversation turn
	p.history = append(p.history, turn{user: transcript, assistant: llmResult.Text})

	// Store conversation turn async (fire-and-forget)
	if p.cfg.CallHistory != nil {
		p.cfg.CallHistory.StoreAsync(ctx, p.cfg.SessionID, transcript, llmResult.Text)
	}

	e2eLatency := time.Since(e2eStart)
	metrics.E2EDuration.Observe(e2eLatency.Seconds())

	slog.Info("pipeline_done", "e2e_ms", e2eLatency.Milliseconds(), "asr_ms", asrResult.LatencyMs, "llm_ms", llmResult.LatencyMs, "tts_ms", ttsLatencyMs)

	onEvent(Event{
		Type:    "metrics",
		ASRMs:   asrResult.LatencyMs,
		LLMMs:   llmResult.LatencyMs,
		TTSMs:   ttsLatencyMs,
		TotalMs: float64(e2eLatency.Milliseconds()),
	})

	return nil
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

func (p *Pipeline) retrieveRAGContext(ctx context.Context, transcript string) string {
	if p.cfg.RAGClient == nil {
		return ""
	}
	ragCtx, err := p.cfg.RAGClient.RetrieveContext(ctx, transcript)
	if err != nil {
		slog.Error("rag retrieval", "error", err)
		return ""
	}
	return ragCtx
}

// streamLLMWithTTS runs LLM streaming and TTS synthesis concurrently using a
// producer/consumer pattern. The LLM streams tokens into a sentenceBuffer (producer);
// when a sentence boundary is detected, the complete sentence is sent to a channel.
// A goroutine (consumer) reads sentences and synthesizes audio via TTS in parallel,
// so the first TTS audio is ready before the LLM finishes generating.
func (p *Pipeline) streamLLMWithTTS(ctx context.Context, transcript, ragContext, ttsEngine string, onEvent EventCallback) (float64, *LLMResult, error) {
	sentenceCh := make(chan string, 4)
	var ttsWg sync.WaitGroup
	var totalTTSMs float64
	var ttsMu sync.Mutex

	// TTS consumer goroutine — synthesizes each sentence as it arrives
	ttsWg.Add(1)
	go func() {
		defer ttsWg.Done()
		p.consumeSentences(ctx, sentenceCh, ttsEngine, onEvent, &totalTTSMs, &ttsMu)
	}()

	// LLM producer — stream content tokens, split at sentence boundaries.
	// Code blocks (``` fenced) are sent to the frontend but omitted from TTS.
	var sb sentenceBuffer
	var cf codeFilter

	llmResult, err := p.cfg.LLMClient.Chat(ctx, transcript, ragContext, p.cfg.SystemPrompt, p.cfg.LLMModel, p.cfg.LLMEngine, func(token string) {
		onEvent(Event{Type: "llm_token", Token: token})
		filtered := cf.Filter(token)
		if filtered == "" {
			return
		}
		s := sb.Add(filtered)
		if s != "" {
			sentenceCh <- s
		}
	})

	// Flush remaining text from sentence buffer
	remainder := sb.Flush()
	if remainder != "" {
		sentenceCh <- remainder
	}
	close(sentenceCh)
	ttsWg.Wait()

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

func (p *Pipeline) consumeSentences(ctx context.Context, sentenceCh <-chan string, ttsEngine string, onEvent EventCallback, totalMs *float64, mu *sync.Mutex) {
	for sentence := range sentenceCh {
		ttsResult, err := p.cfg.TTSClient.Synthesize(ctx, sentence, ttsEngine)
		if err != nil {
			slog.Error("tts sentence", "error", err, "text", sentence)
			onEvent(Event{Type: "error", Text: err.Error()})
			return
		}
		mu.Lock()
		*totalMs += ttsResult.LatencyMs
		mu.Unlock()
		onEvent(Event{Type: "tts_ready", Audio: ttsResult.Audio, LatencyMs: ttsResult.LatencyMs})
	}
}
