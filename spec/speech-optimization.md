# Speech Model Optimization & Applied Research

Proposed improvements to the ASR→LLM→TTS pipeline, targeting call-center-grade quality, noise robustness, and real-time responsiveness.

---

## 1. ASR Accuracy & Noise Robustness

### Current State
- Single ASR backend: Whisper via multipart POST (`pipeline/asr.go`)
- Audio preprocessing: G.711 codec decode, windowed-sinc resample to 16kHz (`audio/resample.go`)
- Post-ASR noise heuristic: regex filter for `[noise]`, `*static*`, hallucination tokens like "um", "uh" (`pipeline/pipeline.go:isNoiseOrArtifact`)
- No upstream noise suppression, echo cancellation, or gain normalization

### Proposed Improvements

**A. Noise suppression pre-stage**
- Add RNNoise (BSD, CPU-only, ~5ms/frame) as a preprocessing step in the audio pipeline between codec decode and resampling
- New interface: `AudioFilter` with `Process([]float32) []float32`, inserted into the processing chain in `pipeline.go` before `Transcribe()`
- Configurable via `gateway.json`: `"noiseSuppressionEnabled": true`
- Fallback: passthrough when disabled, zero-allocation

**B. Whisper prompt conditioning**
- Pass domain-specific initial prompt to Whisper (e.g., "Customer service call transcript:") to reduce hallucination and bias toward expected vocabulary
- Add `initial_prompt` field to the multipart form in `MultipartASRClient.Transcribe()`
- Configurable per-call via WS metadata `asr_prompt` field

**C. Confidence-gated filtering**
- Request Whisper `word_timestamps` + `no_speech_prob` in the inference call
- Replace regex-based `isNoiseOrArtifact` with confidence threshold: drop segments where `no_speech_prob > 0.6`
- Surface per-segment confidence in the `transcript` WS event for downstream quality monitoring

**D. WER evaluation harness**
- Offline tool: feed reference/hypothesis transcript pairs, compute WER/CER using Levenshtein distance
- Store per-call transcripts in a structured log (JSON lines) for batch evaluation
- Add Prometheus gauge `asr_wer_estimate` from periodic sample-based evaluation runs

---

## 2. TTS Naturalness — Prosody, Pacing, Pronunciation

### Current State
- 4 TTS backends: Piper (3 quality tiers), Kokoro, MeloTTS, ElevenLabs (`pipeline/tts.go`)
- Sentence splitting at `.!?` boundaries (`pipeline/sentence.go`) — no awareness of clause structure or breathing pauses
- MeloTTS has `speed` param but hardcoded to `1.0`; no pitch/stress controls exposed on any backend
- No number/spelling pronunciation normalization (e.g., "123" sent as-is to TTS)

### Proposed Improvements

**A. Text normalization pre-stage**
- Insert a `NormalizeForSpeech(text string) string` step before TTS dispatch in `consumeSentences()`
- Rules: expand numbers ("123" → "one hundred twenty-three"), spell out abbreviations ("Dr." → "Doctor"), handle currency ("$5.50" → "five dollars and fifty cents"), phone numbers, dates
- Implementation: rule-based with regex + lookup table (no ML dependency), ~200 lines Go

**B. Expose prosody controls per-backend**
- Add `TTSOptions` struct: `Speed float64`, `Pitch float64`, `Voice string`
- Thread from WS metadata → pipeline → `Synthesize()` call
- MeloTTS: map `Speed` to existing `speed` param; Kokoro: map `Voice`; ElevenLabs: add `stability`/`similarity_boost` params to request body
- Frontend: add sliders to `CallPanel.tsx` for speed (0.75–1.5x) and voice selection

**C. Improved sentence segmentation for pacing**
- Upgrade `sentence.go` splitter to also break on semicolons, em-dashes, and comma-separated clauses >15 words
- Insert 200ms silence padding between segments in the audio stream (configurable `inter_sentence_pause_ms`)
- This produces more natural pacing without requiring SSML

**D. SSML support (ElevenLabs / future backends)**
- For backends that accept SSML: wrap normalized text with `<prosody>` and `<break>` tags
- Guard: only emit SSML when `backend.SupportsSSML()` returns true; otherwise pass plain text

---

## 3. Latency vs. Quality Tradeoffs

### Current State
- Sentence-level pipelining: LLM streams tokens → sentence buffer → TTS goroutine per sentence (`pipeline.go:consumeSentences`)
- VAD: energy-based, fixed -30 dBFS threshold, 1s silence timeout (`audio/vad.go`)
- Piper offers 3 quality tiers (low/medium/high) but selection is per-backend, not adaptive
- E2E latency tracked via Prometheus histograms (`metrics/metrics.go`)
- Target budget: <800ms total (ASR <200ms, LLM TTFT <150ms, TTS <200ms)

### Proposed Improvements

**A. Adaptive quality tier selection**
- Define latency budget per-call in WS metadata: `"latency_budget_ms": 600`
- Gateway selects TTS quality tier dynamically: if ASR+LLM consumed >400ms of a 600ms budget → downgrade TTS to `fast`; otherwise use `quality`
- Implement as `selectTTSTier(budgetMs, elapsedMs int) string` in pipeline orchestration

**B. Chunked/streaming ASR**
- For long utterances (>3s), send partial audio to Whisper every 2s while still accumulating
- Emit interim `transcript_partial` WS events for real-time UI feedback
- Final `transcript` event replaces partials once VAD detects end-of-speech
- Reduces perceived latency: user sees transcription appearing while still speaking

**C. Speculative TTS prefetch**
- After the first LLM sentence completes, begin synthesizing it immediately (already done)
- Additionally: for common conversational openers ("I'd be happy to help", "Let me look into that"), pre-synthesize and cache audio at startup
- Cache keyed by `(text, voice, tier)` tuple; LRU eviction; max 50 entries

**D. Adaptive VAD thresholds**
- Current: fixed -30 dBFS threshold
- Proposed: calibrate threshold during the first 500ms of each call (noise floor estimation)
- Set speech threshold to `noise_floor_dBFS + 10dB` dynamically
- Reduces false triggers in noisy environments, fewer wasted ASR calls

---

## 4. Emerging Speech Tech Integration

### Current State
- VAD: energy-based RMS only (`audio/vad.go`) — no ML-based detection
- No speaker diarization — all audio treated as single-speaker
- Codecs: PCM, G.711 μ-law/A-law (`audio/codec.go`) — no Opus
- No echo cancellation

### Proposed Improvements

**A. Silero VAD upgrade**
- Replace energy-based VAD with Silero VAD (ONNX, ~1ms/frame on CPU)
- Run as a sidecar microservice or embed via ONNX Go runtime (`onnxruntime-go`)
- Benefits: robust to music/noise, handles cross-talk, language-agnostic
- Fallback: retain energy-based VAD as backup when Silero unavailable

**B. Speaker diarization**
- Add pyannote-audio as a sidecar service (Python, GPU-optional)
- Post-ASR enrichment: after transcription, run diarization on the same audio segment
- Tag each transcript segment with `speaker_id` in the WS event
- Use case: call center with agent + customer on same stream; enables per-speaker analytics

**C. Opus codec support**
- Add Opus decode path in `audio/codec.go` using `gopus` (CGo wrapper around libopus)
- Benefits: 50-70% bandwidth reduction vs PCM, built-in packet loss concealment
- Frontend: negotiate codec in WS metadata; prefer Opus when `AudioEncoder` API available in browser

**D. Echo cancellation**
- Integrate SpeexDSP AEC (C library, Go CGo bindings) or WebRTC AEC3
- Required for full-duplex scenarios where TTS playback leaks into microphone
- Insert in audio pipeline after codec decode, before VAD
- Requires reference signal (TTS output audio) — feed back via shared ring buffer

---

## 5. Frontend Controls & WS Metadata Threading

All new parameters flow through the same path as existing settings: `CallPanel.tsx` UI → `useAudioStream.js` WS metadata JSON → `handler.go` `callMetadata` struct → pipeline.

### Current WS metadata fields (handler.go:53-62)
`codec`, `sample_rate`, `tts_engine`, `stt_engine`, `system_prompt`, `llm_model`, `llm_engine`, `mode`

### New metadata fields to add

| Field | Type | Default | UI Control | Section |
|---|---|---|---|---|
| `noise_suppression` | bool | `false` | Toggle switch | 1A |
| `asr_prompt` | string | `""` | Text input (collapsible "Advanced") | 1B |
| `confidence_threshold` | float | `0.6` | Slider (0.0–1.0) | 1C |
| `tts_speed` | float | `1.0` | Slider (0.75–1.5) | 2B |
| `tts_voice` | string | backend default | Dropdown (per-engine voices) | 2B |
| `text_normalization` | bool | `true` | Toggle switch | 2A |
| `inter_sentence_pause_ms` | int | `200` | Slider (0–500ms) | 2C |
| `latency_budget_ms` | int | `800` | Dropdown: "Low latency" (600) / "Balanced" (800) / "High quality" (1200) | 3A |
| `vad_mode` | string | `"energy"` | Dropdown: "Energy" / "Silero" | 4A |
| `diarization` | bool | `false` | Toggle switch | 4B |

### Implementation path

**Gateway (`handler.go`)**
- Extend `callMetadata` struct with the new fields
- Thread into `pipeline.RunConfig` (new struct or extend existing params passed to `runFullPipeline`)

**Pipeline (`pipeline.go`)**
- New `SpeechOpts` struct aggregating the per-call speech tuning params
- Passed alongside existing `ttsEngine`/`sttEngine` strings into pipeline stages
- Each stage reads only the fields it needs (noise suppression → audio filter, confidence → ASR post-filter, speed/voice → TTS client)

**Frontend (`CallPanel.tsx`)**
- Group new controls under a collapsible "Speech Tuning" panel to avoid cluttering the main UI
- Sensible defaults mean users can ignore the panel entirely
- `useAudioStream.js`: spread new fields into the `meta` object sent on WS open (lines 27-36)

---

## Evaluation & Verification Plan

| Metric | Tool | Target |
|---|---|---|
| WER improvement (noise suppression) | Offline eval harness with LibriSpeech + noise augmentation | <15% WER on noisy test set |
| TTS naturalness | Side-by-side MOS listening test (5 evaluators) | MOS >3.5 |
| E2E latency (P95) | Prometheus `e2e_duration` histogram | <800ms |
| VAD precision/recall | Labeled speech/silence test set | Precision >0.95, Recall >0.90 |
| Diarization DER | AMI meeting corpus subset | DER <20% |

## Implementation Priority

1. **Quick wins** (1-2 days each): Text normalization, adaptive VAD threshold, expose MeloTTS speed param, Whisper prompt conditioning
2. **Medium effort** (3-5 days each): RNNoise integration, chunked ASR, improved sentence segmentation, confidence-gated filtering
3. **Larger efforts** (1-2 weeks each): Silero VAD, Opus codec, speaker diarization, WER evaluation harness
