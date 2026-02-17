# SpeechLM Mode — Specification

> End-to-end speech language models as an alternative to the ASR → LLM → TTS pipeline.

## 1. Problem Statement

The current pipeline processes voice in three sequential stages:

| Stage | Typical Latency | Notes |
|-------|----------------|-------|
| ASR (Whisper.cpp) | ~200 ms | Depends on utterance length |
| LLM (Ollama) | ~1 500 ms | Dominated by TTFT + generation |
| TTS (Piper) | ~350 ms | Sentence-level synthesis |
| **Total E2E** | **~2 050 ms** | Before any audio reaches the client |

Each handoff adds serialization overhead, format conversion, and scheduling delay. The LLM stage cannot begin until ASR emits a final transcript; TTS cannot begin until the LLM emits at least one sentence. These sequential dependencies set a hard floor on latency that no single-stage optimization can break.

A **SpeechLM** bypasses all three handoffs by processing audio tokens directly and emitting audio tokens as output — collapsing the pipeline into a single inference pass.

## 2. What is a SpeechLM

A Speech Language Model is a transformer that operates on **audio tokens** (produced by a neural audio codec) instead of — or alongside — text tokens. Key properties:

- **Audio codec** (e.g. Mimi, SNAC, SpeechTokenizer) encodes waveforms into discrete token sequences at 12–50 Hz.
- The LM predicts the next audio token(s) autoregressively, the same way a text LM predicts the next word.
- A **vocoder** or codec decoder converts predicted tokens back to a waveform in real-time.
- Some models interleave a **text "inner monologue"** stream alongside audio tokens to improve coherence.
- Latency is bounded by codec frame size + one LM forward pass — typically **160–600 ms**.

## 3. Model Landscape

### 3.1 Moshi (Kyutai)

| | |
|---|---|
| Paper | [arXiv 2410.00037](https://arxiv.org/abs/2410.00037) |
| Code | [github.com/kyutai-labs/moshi](https://github.com/kyutai-labs/moshi) |
| Params | 7B (Temporal Transformer, init from Helium) + small Depth Transformer |
| Codec | Mimi — 24 kHz, 8 RVQ levels, 12.5 Hz token rate, 1.1 kbps |
| Latency | ~200 ms on NVIDIA L4 (theoretical min 160 ms) |
| Duplex | **Full-duplex** — models user + agent audio streams simultaneously |
| Streaming | WebSocket, Rust server supports 64 concurrent sessions on L40S |
| License | Code: MIT (Python) / Apache (Rust). Models: CC-BY 4.0 |
| Backends | Rust/Candle (production), PyTorch (research), MLX (Apple) |
| Quantization | bf16, int8 (Rust/Candle); int4/int8/bf16 (MLX) |
| VRAM | ~24 GB (bf16), less with int8 |
| Variants | Moshiko (base), Moshika (fine-tuned) |
| ROCm | **Not officially supported.** Rust backend uses Candle with `--features cuda` only. PyTorch backend may work via ROCm PyTorch builds but untested by Kyutai. |

**Key innovation:** Inner Monologue — predicts time-aligned text tokens before audio tokens per frame, improving factuality without an external ASR/TTS pipeline.

### 3.2 LLaMA-Omni / LLaMA-Omni2 (ICTNLP)

| | v1 (LLaMA-Omni) | v2 (LLaMA-Omni2) |
|---|---|---|
| Paper | [arXiv 2409.06666](https://arxiv.org/abs/2409.06666) | [arXiv 2505.02625](https://arxiv.org/abs/2505.02625) |
| Code | [github.com/ictnlp/LLaMA-Omni](https://github.com/ictnlp/LLaMA-Omni) | [github.com/ictnlp/LLaMA-Omni2](https://github.com/ictnlp/LLaMA-Omni2) |
| Base LLM | Llama-3.1-8B-Instruct | Qwen2.5-Instruct (0.5B–32B) |
| Speech encoder | Whisper-large-v3 | Whisper-large-v3 |
| Speech decoder | NAR CTC + HiFi-GAN vocoder | Autoregressive Transformer + CosyVoice2 flow-matching |
| Latency | ~236 ms | ~583 ms (R=3, W=10) |
| Training data | 200K samples (InstructS2S-200K) | 200K multi-turn dialogues |
| Training cost | 65 hours on 4× L40 | Similar scale |
| Duplex | Half-duplex | Half-duplex |
| License | Code: Apache 2.0. **Models: academic research only** | Same (commercial use requires author permission) |
| ROCm | Custom serving stack (not vLLM). Untested on ROCm. | Same |

**v2 trade-off:** Higher speech quality (UTMOS 4.19) at the cost of ~2.5× latency vs v1. The modular architecture (separate encoder, LLM, decoder) means individual components can be swapped.

### 3.3 Qwen2.5-Omni / Qwen3-Omni (Alibaba)

| | |
|---|---|
| Code | [github.com/QwenLM/Qwen2.5-Omni](https://github.com/QwenLM/Qwen2.5-Omni) |
| Params | 7B (Qwen2.5-Omni), MoE variants (Qwen3-Omni) |
| I/O | Text, audio, image, video → text + speech output |
| Architecture | Thinker (multimodal LLM) → Talker (speech decoder) → code2wav |
| Serving | vLLM-Omni with full audio output support |
| License | Apache 2.0 (Qwen model license) |
| ROCm | **Supported.** [Qwen2.5-Omni-ROCm](https://github.com/alexhegit/Qwen2.5-Omni-ROCm) community fork exists. vLLM-Omni has Day-0 ROCm support with pre-built Docker images (Jan 2026). Validated on MI300/MI350. |

**Strongest ROCm story** of any SpeechLM. The vLLM-Omni ecosystem provides production-grade serving with ROCm CI pipelines.

### 3.4 GLM-4-Voice (ZhipuAI / Tsinghua)

| | |
|---|---|
| Paper | [arXiv 2412.02612](https://arxiv.org/abs/2412.02612) |
| Code | [github.com/THUDM/GLM-4-Voice](https://github.com/THUDM/GLM-4-Voice) |
| Params | 9B (GLM-4-9B base) |
| Codec | Single-codebook speech tokenizer at 12.5 Hz, 175 bps (fine-tuned from Whisper-large-v3) |
| Speech decoder | CosyVoice-based, streaming, starts with 10 tokens |
| Languages | Chinese + English |
| License | Apache 2.0 |
| ROCm | PyTorch-based. No official ROCm testing. |

**Note:** Requires loading three models (tokenizer, LLM, decoder) simultaneously — high VRAM usage. Strongest in Chinese-language tasks; English quality untested at scale.

### 3.5 Mini-Omni (gpt-omni)

| | |
|---|---|
| Code | [github.com/gpt-omni/mini-omni](https://github.com/gpt-omni/mini-omni) |
| Params | ~0.5B (Qwen2-0.5B base) + 7 SNAC sub-LM heads |
| Codec | SNAC — 7 layers, generates 1 text + 7 audio tokens per step |
| I/O | Speech-in / speech-out |
| License | MIT |
| ROCm | PyTorch-based; likely works with ROCm builds given small model size. |

**Lightweight but limited.** The 0.5B backbone constrains reasoning quality. Useful as a proof-of-concept or for resource-constrained deployments.

### 3.6 Hybrid Models (Speech-in / Text-out)

These models skip ASR but still require a separate TTS stage:

| Model | Org | Params | I/O | License | Serving | ROCm |
|-------|-----|--------|-----|---------|---------|------|
| **Qwen2-Audio** | Alibaba | 7B | Audio → text | Apache 2.0 | HF Transformers, vLLM | vLLM has ROCm support |
| **Ultravox** | Fixie AI | 8B–70B | Audio → text | MIT | HF Transformers, vLLM (v0.13+) | vLLM ROCm |

These eliminate ASR latency (~200 ms) but retain TTS latency (~350 ms). Useful as an incremental improvement over the full pipeline.

### 3.7 Ollama Status

Audio input/output is **not supported** in Ollama ([issue #11798](https://github.com/ollama/ollama/issues/11798), opened Aug 2025). The underlying llama.cpp engine only supports image modality. No official timeline or maintainer commitment exists. **Cannot use Ollama for SpeechLM today.**

## 4. Recommendation

### Tier 1 — True Speech-in / Speech-out (best latency reduction)

| Model | Why | ROCm Status | Risk |
|-------|-----|-------------|------|
| **Qwen2.5-Omni** | Full I/O, vLLM-Omni serving, **best ROCm support** (pre-built Docker, MI300 validated), Apache 2.0 | Supported | Newer model, less community battle-testing |
| **Moshi** | Lowest latency (~200 ms), full-duplex, production Rust server | Not supported (CUDA only) | Requires NVIDIA GPU or manual Candle ROCm porting |
| **LLaMA-Omni2** | Scalable (0.5B–32B), modular, trainable on 4 GPUs | Untested | Higher latency (~583 ms), **academic-only license**, custom serving stack (not vLLM) |

**For AMD GPU (ROCm): Qwen2.5-Omni is the clear first choice.** It is the only Tier 1 model with validated ROCm support via vLLM-Omni.

**For NVIDIA GPU: Moshi** offers the best latency and full-duplex capability.

### Tier 2 — Hybrid (speech-in, text-out + existing TTS)

| Model | Benefit | Trade-off |
|-------|---------|-----------|
| **Qwen2-Audio** / **Ultravox** | Eliminates ASR stage, keeps existing TTS | Still ~350 ms TTS overhead |

### Tier 3 — Experimental

| Model | Note |
|-------|------|
| **GLM-4-Voice** | High VRAM, Chinese-focused, no ROCm testing |
| **Mini-Omni** | Tiny model, limited reasoning quality |

## 5. Integration Architecture

### 5.1 Mode Selection

The existing `callMetadata.mode` field (already in `ws/handler.go:56`) carries the mode. A new value `"speechlm"` activates the SpeechLM path. The `callMetadata` struct already supports engine selection — reuse it:

```json
{
  "mode": "speechlm",
  "speechlm_engine": "qwen2.5-omni",
  "sample_rate": 24000,
  "system_prompt": "You are a helpful assistant."
}
```

### 5.2 Event Flow

```
Client                    Gateway                   SpeechLM Server
  │                         │                            │
  │── callMetadata ────────►│                            │
  │   (mode: "speechlm")   │                            │
  │                         │── open stream ────────────►│
  │── binary audio ────────►│── forward audio ──────────►│
  │                         │                            │
  │                         │◄── transcript event ───────│  (inner monologue text)
  │◄── {"event":"transcript"}│                           │
  │                         │◄── audio chunks ───────────│
  │◄── binary audio ────────│                            │
  │                         │◄── metrics event ──────────│
  │◄── {"event":"metrics"} ─│                            │
```

The gateway proxies raw audio bidirectionally. The SpeechLM server emits the same WebSocket event types (`transcript`, `tts_ready`, `metrics`) so the frontend playback logic remains unchanged.

### 5.3 SpeechLM Service Deployment

```
┌─────────────────────────────────────┐
│  docker-compose                     │
│                                     │
│  ┌──────────┐    ┌───────────────┐  │
│  │ gateway  │───►│ speechlm      │  │
│  │ :8080    │    │ (vLLM-Omni)   │  │
│  └──────────┘    │ :8000         │  │
│                  └───────────────┘  │
└─────────────────────────────────────┘
```

For Qwen2.5-Omni on ROCm: use the official `vllm-omni` Docker image with ROCm support.

## 6. Gateway Integration Points

### 6.1 New Files

| File | Purpose |
|------|---------|
| `pipeline/speechlm.go` | `SpeechLMProcessor` interface + `SpeechLMRouter` (multi-backend dispatch) |
| `pipeline/speechlm_vllm.go` | vLLM-Omni client implementation |

### 6.2 Modified Files

| File | Change |
|------|--------|
| `ws/handler.go` | Route `mode == "speechlm"` to `ProcessSpeechLM()` instead of the ASR→LLM→TTS pipeline |
| `pipeline/pipeline.go` | Add `ProcessSpeechLM(ctx, audio, opts) (audioOut, transcript, error)` method |
| `cmd/gateway/main.go` | Register SpeechLM backends from `SPEECHLM_URL` env var |
| `CallPanel.tsx` | Add "SpeechLM" mode toggle + engine dropdown |
| `useAudioStream.ts` | Send `speechlm_engine` in WebSocket metadata when mode is `speechlm` |

### 6.3 Interface Design

```go
// SpeechLMProcessor processes audio end-to-end.
type SpeechLMProcessor interface {
    // ProcessAudio sends audio and streams back audio + transcript events.
    ProcessAudio(ctx context.Context, audio []byte, opts SpeechLMOptions) (*SpeechLMResult, error)
}

type SpeechLMOptions struct {
    SystemPrompt string
    SampleRate   int
    Engine       string
}

type SpeechLMResult struct {
    AudioOut   []byte
    Transcript string
    Latency    time.Duration
}
```

## 7. Comparison Matrix

| | Current Pipeline | SpeechLM (Qwen2.5-Omni) | SpeechLM (Moshi) |
|---|---|---|---|
| **E2E Latency** | ~2 000 ms | ~500–800 ms (est.) | ~200 ms |
| **Duplex** | Half (VAD-gated) | Half | Full |
| **Quality** | High (best-of-breed per stage) | Good (single model) | Good (7B) |
| **Flexibility** | Mix-and-match ASR/LLM/TTS | Single model | Single model |
| **GPU** | CPU-capable (Whisper.cpp + Piper) | AMD ROCm (MI300) or NVIDIA | NVIDIA only |
| **VRAM** | ~2 GB total | ~16 GB (7B bf16) | ~24 GB (7B bf16) |
| **Maturity** | Production-ready | Early production (vLLM-Omni 0.14) | Beta |
| **Ollama** | Yes (LLM stage) | No | No |

## 8. Open Questions

1. **ROCm on consumer AMD GPUs** — vLLM-Omni validates on MI300/MI350 (datacenter). Consumer GPUs (RX 7900 XTX) have partial ROCm support — needs testing.
2. **Quality parity** — Single-model SpeechLMs may lag behind best-of-breed pipelines on complex reasoning or voice quality. Needs A/B evaluation.
3. **Full-duplex UX** — Moshi supports full-duplex; the current frontend assumes half-duplex VAD-gated turns. Full-duplex requires barge-in handling and echo cancellation changes.
4. **Streaming granularity** — Current pipeline sends sentence-at-a-time TTS. SpeechLM sends continuous audio. The frontend audio player may need buffering changes.
5. **System prompt injection** — How do SpeechLMs handle system prompts? Qwen2.5-Omni accepts text system prompts; Moshi uses pre-training context. Needs validation.
6. **Multi-language** — GLM-4-Voice is strongest in Chinese; Moshi is English-focused. Qwen2.5-Omni supports both. Evaluate per use-case.
7. **Fallback strategy** — If SpeechLM service is unavailable, should the gateway fall back to the ASR→LLM→TTS pipeline transparently?

## References

- Moshi: [github.com/kyutai-labs/moshi](https://github.com/kyutai-labs/moshi), [arXiv 2410.00037](https://arxiv.org/abs/2410.00037)
- LLaMA-Omni: [github.com/ictnlp/LLaMA-Omni](https://github.com/ictnlp/LLaMA-Omni), [arXiv 2409.06666](https://arxiv.org/abs/2409.06666)
- LLaMA-Omni2: [github.com/ictnlp/LLaMA-Omni2](https://github.com/ictnlp/LLaMA-Omni2), [arXiv 2505.02625](https://arxiv.org/abs/2505.02625)
- Qwen2.5-Omni: [github.com/QwenLM/Qwen2.5-Omni](https://github.com/QwenLM/Qwen2.5-Omni)
- Qwen2.5-Omni ROCm: [github.com/alexhegit/Qwen2.5-Omni-ROCm](https://github.com/alexhegit/Qwen2.5-Omni-ROCm)
- vLLM-Omni: [github.com/vllm-project/vllm-omni](https://github.com/vllm-project/vllm-omni)
- ROCm + vLLM-Omni: [rocm.blogs.amd.com](https://rocm.blogs.amd.com/software-tools-optimization/vllm-omni/README.html)
- GLM-4-Voice: [github.com/THUDM/GLM-4-Voice](https://github.com/THUDM/GLM-4-Voice), [arXiv 2412.02612](https://arxiv.org/abs/2412.02612)
- Mini-Omni: [github.com/gpt-omni/mini-omni](https://github.com/gpt-omni/mini-omni)
- Ultravox: [github.com/fixie-ai/ultravox](https://github.com/fixie-ai/ultravox)
- Qwen2-Audio: [github.com/QwenLM/Qwen2-Audio](https://github.com/QwenLM/Qwen2-Audio)
- Ollama audio status: [github.com/ollama/ollama/issues/11798](https://github.com/ollama/ollama/issues/11798)
