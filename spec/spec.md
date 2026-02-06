# Real-Time ASR → LLM → TTS Streaming Pipeline — PoC Specification

## 1. Problem Statement

Enterprise call centers require real-time voice AI capable of transcribing caller speech, generating intelligent responses, and synthesizing natural-sounding replies — all within conversational latency bounds (~500ms end-to-end). This demands a streaming pipeline that handles concurrent audio sessions with robust codec support, voice activity detection, and per-stage observability.

This PoC demonstrates a production-grade architecture for that pipeline: a Go orchestrator managing WebSocket audio streams through ASR (whisper.cpp), LLM (Ollama), and TTS (Piper/Coqui) stages — with Prometheus metrics, Grafana dashboards, and a load testing harness for concurrency simulation.

## 2. Architecture

```
┌──────────────────── Docker Compose ────────────────────┐
│                                                         │
│  ┌───────────┐   ┌───────────────┐   ┌──────────────┐  │
│  │ Frontend  │──▶│ Go Gateway /  │──▶│  Piper TTS   │  │
│  │ (React)   │◀──│ Orchestrator  │◀──│  (Container) │  │
│  └───────────┘   └───────┬───────┘   └──────────────┘  │
│                          │           ┌──────────────┐   │
│                          │           │  Coqui TTS   │   │
│                          │           │  (Container) │   │
│                          │           └──────────────┘   │
│  ┌───────────┐   ┌───────┴───────┐   ┌──────────────┐  │
│  │ Load      │   │  Prometheus   │   │   Grafana    │  │
│  │ Tester    │   └───────────────┘   └──────────────┘  │
│  └───────────┘                                          │
└──────────────────────────┬──────────────────────────────┘
                           │ HTTP (host.docker.internal)
                 ┌─────────┴──────────┐
                 │   Host Services    │
                 │  ┌──────────────┐  │
                 │  │ whisper.cpp  │  │
                 │  │   server     │  │
                 │  │   (eGPU)     │  │
                 │  └──────────────┘  │
                 │  ┌──────────────┐  │
                 │  │   Ollama     │  │
                 │  │   (eGPU)     │  │
                 │  └──────────────┘  │
                 └────────────────────┘
```

### Deployment Topology

| Service | Runs In | GPU | Port | Purpose |
|---------|---------|-----|------|---------|
| whisper.cpp server | Host | eGPU (ROCm) | 8178 | Streaming ASR |
| Ollama | Host | eGPU (ROCm) | 11434 | LLM inference |
| Go Gateway | Docker | — | 8080 | Pipeline orchestrator, WebSocket |
| Piper TTS | Docker | — | 5100 | Low-latency TTS (ONNX, CPU) |
| Coqui TTS | Docker | — | 5200 | High-quality TTS (VITS, CPU) |
| Frontend | Docker | — | 3000 | React SPA |
| Prometheus | Docker | — | 9090 | Metrics collection |
| Grafana | Docker | — | 3001 | Metrics dashboards |
| Load Tester | Docker | — | — | Concurrent call simulation |

GPU services (whisper.cpp, Ollama) run on the host to avoid Docker GPU passthrough complexity with the AMD ROCm eGPU. Docker containers reach them via `host.docker.internal`.

## 3. Pipeline: Single Call Flow

```
1. Client connects via WebSocket to /ws/call
   → sends metadata: { codec, sample_rate, tts_engine, mode }

2. Gateway spawns goroutine for this call session

3. Audio chunks arrive (20ms frames)
   → Decode codec (G.711 μ-law/A-law → PCM, or Opus → PCM)
   → Resample to 16kHz mono

4. VAD filters silence, buffers speech segments
   → Energy-based detection with configurable threshold
   → Trailing silence timeout triggers end-of-utterance

5. On end-of-utterance:
   a. POST buffered audio to whisper.cpp /inference → transcript
   b. Record ASR latency
   c. Stream transcript back to client via WebSocket

6. Transcript → Ollama /api/chat (streaming response)
   → Record time-to-first-token
   → Accumulate tokens until sentence boundary

7. Sentence → POST to TTS /synthesize → WAV audio bytes
   → Record TTS latency

8. Audio bytes streamed back to client via WebSocket

9. All stage timings emitted as Prometheus histogram observations
```

### Latency Budget

| Stage | Target | Notes |
|-------|--------|-------|
| Codec decode + resample | < 5ms | Pure computation, no I/O |
| VAD + buffering | ~300ms trailing silence | Configurable |
| ASR (whisper.cpp) | < 200ms | GPU-accelerated, large-v3-turbo model |
| LLM time-to-first-token | < 150ms | Local Ollama, small model |
| TTS synthesis | < 200ms | Piper target; Coqui may be higher |
| **Total end-to-end** | **< 800ms** | From end-of-speech to first audio out |

## 4. Service Specifications

### 4.1 Go Gateway/Orchestrator

**Language**: Go 1.22+
**Key packages**: `gorilla/websocket`, `prometheus/client_golang`

**Responsibilities**:
- WebSocket server accepting audio streams
- Codec decoding (G.711 μ-law/A-law, Opus, raw PCM)
- Resampling to 16kHz mono (linear interpolation for upsampling)
- Energy-based VAD with configurable speech/silence thresholds
- HTTP clients to whisper.cpp, Ollama, and TTS backends
- Goroutine-per-call concurrency with `context.Context` cancellation
- Prometheus metrics endpoint at `/metrics`

**Concurrency Model**:
- Each WebSocket connection gets a dedicated goroutine
- Internal pipeline stages communicate via Go channels
- Shared `http.Client` connection pools to backends (configurable max connections)
- Backpressure: if ASR queue depth exceeds threshold, slow WebSocket reads
- Graceful shutdown via signal handling + context cancellation

**WebSocket Protocol**:
```
Client → Server:
  Binary frames: raw audio chunks
  Text frame (first): JSON metadata
    {
      "codec": "pcm" | "g711_ulaw" | "g711_alaw" | "opus",
      "sample_rate": 16000,
      "tts_engine": "piper" | "coqui",
      "mode": "conversation" | "transcribe_only"
    }

Server → Client:
  Text frames: JSON events
    { "type": "transcript",  "text": "...", "latency_ms": 142 }
    { "type": "llm_token",   "token": "..." }
    { "type": "llm_done",    "text": "...", "latency_ms": 312 }
    { "type": "tts_ready",   "latency_ms": 185 }
    { "type": "metrics",     "asr_ms": 142, "llm_ms": 312, "tts_ms": 185, "total_ms": 644 }
  Binary frames: TTS audio (WAV PCM)
```

**Prometheus Metrics**:
- `pipeline_calls_active` (gauge) — currently active call sessions
- `pipeline_calls_total` (counter) — total calls processed
- `pipeline_stage_duration_seconds` (histogram, labels: `stage={asr,llm,tts}`) — per-stage latency
- `pipeline_e2e_duration_seconds` (histogram) — end-to-end latency
- `pipeline_errors_total` (counter, labels: `stage`, `error_type`) — error counts
- `audio_chunks_processed_total` (counter) — total audio chunks
- `vad_speech_segments_total` (counter) — speech segments detected

### 4.2 Frontend (React)

**Framework**: React 18 + Vite + TypeScript

**Features**:
- **Mic mode**: Web Audio API `AudioWorklet` capturing PCM 16kHz mono, chunked at 20ms, streamed over WebSocket
- **File mode**: Load WAV/MP3 via `<input type="file">`, decode with Web Audio API, stream chunks over WebSocket at real-time rate
- **Transcript panel**: Scrolling transcript of ASR output
- **Audio playback**: Reassemble TTS binary frames, play via `AudioContext`
- **TTS toggle**: Dropdown to select Piper or Coqui
- **Metrics display**: Per-call latency breakdown (ASR/LLM/TTS/total), pulled from WebSocket events

### 4.3 Piper TTS

**Base image**: `python:3.11-slim`
**Install**: Download piper binary + `en_US-lessac-medium` ONNX voice model
**Server**: FastAPI with single endpoint

```
POST /synthesize
Content-Type: application/json
{ "text": "Hello, how can I help you?", "voice": "en_US-lessac-medium" }

Response: audio/wav (PCM 22050Hz mono)
```

**Characteristics**: ~100-200ms for short sentences, CPU-only, ~200MB container

### 4.4 Coqui TTS

**Base image**: `python:3.11-slim`
**Install**: `pip install TTS`
**Model**: `tts_models/en/ljspeech/vits` (auto-downloads on first run)
**Server**: FastAPI, same endpoint interface as Piper for drop-in switching

```
POST /synthesize
Content-Type: application/json
{ "text": "Hello, how can I help you?" }

Response: audio/wav (PCM 22050Hz mono)
```

**Characteristics**: ~500-1500ms, higher voice quality, CPU-only, ~1.5GB container

### 4.5 Load Tester

**Language**: Go
**Usage**:
```bash
loadtest --gateway ws://gateway:8080/ws/call \
         --concurrency 20 \
         --duration 60s \
         --audio-dir /samples \
         --codec pcm \
         --tts-engine piper
```

**Behavior**:
- Opens N concurrent WebSocket connections
- Each connection picks a random audio file from `--audio-dir`
- Streams audio chunks at real-time rate (simulating a live caller)
- Collects per-call metrics from server `metrics` events
- On completion, prints summary:
  ```
  Calls completed: 47
  Errors: 0
  ASR latency   p50=138ms  p95=215ms  p99=287ms
  LLM latency   p50=245ms  p95=410ms  p99=523ms
  TTS latency   p50=167ms  p95=289ms  p99=342ms
  E2E latency   p50=562ms  p95=891ms  p99=1102ms
  ```

### 4.6 Monitoring

**Prometheus**: Scrapes `gateway:8080/metrics` every 5s
**Grafana**: Pre-provisioned dashboard with panels:
- Active calls (gauge, real-time)
- Per-stage latency histograms (ASR, LLM, TTS)
- Calls/second throughput
- Error rate by stage
- P95 latency over time (line chart)

## 5. Audio Processing Details

### Codec Support

| Codec | Input Format | Decode | Output |
|-------|-------------|--------|--------|
| PCM | 16-bit LE, any rate | Passthrough | Resample to 16kHz mono |
| G.711 μ-law | 8kHz, 8-bit | μ-law expansion table | Resample 8kHz → 16kHz |
| G.711 A-law | 8kHz, 8-bit | A-law expansion table | Resample 8kHz → 16kHz |
| Opus | Variable rate | `gopus` decoder | Resample to 16kHz mono |

### Voice Activity Detection (VAD)

Energy-based VAD in the gateway:
- Compute RMS energy per audio chunk
- Speech threshold: configurable (default -30 dBFS)
- Minimum speech duration: 250ms (reject transients)
- Trailing silence timeout: 300ms (trigger end-of-utterance)
- Pre-speech buffer: keep 200ms of audio before speech onset for context

### Resampling

Linear interpolation for upsampling (8kHz → 16kHz). Sufficient for speech; more sophisticated sinc interpolation available as a future enhancement.

## 6. Configuration

### Environment Variables (`.env`)

```bash
# Host services (accessed via host.docker.internal from Docker)
WHISPER_URL=http://host.docker.internal:8178
OLLAMA_URL=http://host.docker.internal:11434
WHISPER_MODEL=ggml-large-v3-turbo.bin

# LLM
OLLAMA_MODEL=llama3.2:3b
LLM_SYSTEM_PROMPT="You are a helpful call center agent. Keep responses concise and conversational."
LLM_MAX_TOKENS=150

# TTS
PIPER_URL=http://piper:5100
COQUI_URL=http://coqui:5200
DEFAULT_TTS_ENGINE=piper
PIPER_VOICE=en_US-lessac-medium

# Gateway
GATEWAY_PORT=8080
MAX_CONCURRENT_CALLS=100
ASR_POOL_SIZE=10
LLM_POOL_SIZE=10
TTS_POOL_SIZE=10

# VAD
VAD_SPEECH_THRESHOLD_DB=-30
VAD_SILENCE_TIMEOUT_MS=300
VAD_MIN_SPEECH_MS=250

# Monitoring
PROMETHEUS_PORT=9090
GRAFANA_PORT=3001
```

## 7. File Structure

```
asr-llm-tts-poc/
├── spec/
│   ├── jd.md
│   └── spec.md                       # This document
├── docker-compose.yml
├── .env
├── services/
│   ├── gateway/
│   │   ├── Dockerfile
│   │   ├── go.mod
│   │   ├── cmd/gateway/
│   │   │   └── main.go
│   │   └── internal/
│   │       ├── pipeline/
│   │       │   ├── pipeline.go       # Stage orchestration
│   │       │   ├── asr.go            # whisper.cpp HTTP client
│   │       │   ├── llm.go            # Ollama HTTP client
│   │       │   └── tts.go            # TTS HTTP client
│   │       ├── audio/
│   │       │   ├── codec.go          # G.711, Opus decode
│   │       │   ├── resample.go       # 16kHz resampling
│   │       │   └── vad.go            # Voice activity detection
│   │       ├── ws/
│   │       │   └── handler.go        # WebSocket handler
│   │       └── metrics/
│   │           └── metrics.go        # Prometheus instrumentation
│   ├── frontend/
│   │   ├── Dockerfile
│   │   ├── package.json
│   │   └── src/
│   │       ├── App.tsx
│   │       ├── hooks/useAudioStream.ts
│   │       └── components/
│   │           ├── CallPanel.tsx
│   │           └── MetricsPanel.tsx
│   ├── piper/
│   │   ├── Dockerfile
│   │   └── server.py
│   ├── coqui/
│   │   ├── Dockerfile
│   │   └── server.py
│   ├── loadtest/
│   │   ├── Dockerfile
│   │   ├── go.mod
│   │   └── main.go
│   └── monitoring/
│       ├── prometheus.yml
│       └── grafana/
│           └── dashboard.json
├── samples/
└── scripts/
    ├── start-host-services.sh
    └── download-models.sh
```

## 8. Cloud Deployment Considerations

_Not implemented in this PoC, but documented to demonstrate architectural awareness._

### Kubernetes Deployment
- **GPU node pool** for whisper.cpp and Ollama pods (NVIDIA T4/A10G or AMD MI-series)
- **CPU node pool** for gateway, TTS, and frontend pods
- **Horizontal Pod Autoscaler** on `pipeline_calls_active` metric
- **Pod Disruption Budget** to ensure zero-downtime during rollouts

### Telephony Integration
- **SIP gateway** (Asterisk, FreeSWITCH, or Twilio Media Streams)
- Receives RTP audio (G.711 μ-law from PSTN), forwards to gateway via WebSocket
- Jitter buffer at the SIP gateway to smooth packet timing
- DTMF detection for IVR menu navigation

### Data Pipeline
- **Kafka** for async logging of transcripts, audio metadata, and metrics
- Cold storage of call recordings in S3/GCS for model retraining
- PII redaction pipeline before storage (credit card numbers, SSNs)

### Reliability
- **Circuit breakers** on all backend calls (ASR, LLM, TTS) with fallback behavior
- **Redis** for session state if running multiple gateway replicas
- **Health checks** and readiness probes on all services
- **Rate limiting** per-tenant at the gateway level

## 9. Implementation Order

1. `spec/spec.md` — this document
2. `docker-compose.yml` + `.env` — service topology
3. `services/gateway/` — Go orchestrator (core pipeline)
4. `services/piper/` — Piper TTS container
5. `services/coqui/` — Coqui TTS container
6. `services/frontend/` — React UI
7. `services/loadtest/` — load testing harness
8. `services/monitoring/` — Prometheus + Grafana
9. `scripts/` — host service helpers
10. End-to-end integration verification

## 10. Verification Plan

| Step | Action | Expected Result |
|------|--------|-----------------|
| 1 | `./scripts/start-host-services.sh` | whisper.cpp server on :8178, Ollama on :11434 |
| 2 | `docker-compose up --build` | All containers healthy |
| 3 | Open `http://localhost:3000`, click mic, speak | Transcript appears, TTS audio plays back |
| 4 | Upload a WAV file in frontend | Same pipeline flow, transcript + audio response |
| 5 | Toggle TTS engine to Coqui | Audio response from Coqui (higher quality, higher latency) |
| 6 | `docker-compose run loadtest --concurrency 10 --duration 30s` | Summary with p50/p95/p99 latencies |
| 7 | Open Grafana at `http://localhost:3001` | Dashboard shows active calls, latency histograms, throughput |
