# ASR-LLM-TTS Pipeline

Real-time voice pipeline for call center automation. Speak into the mic, get a transcription via whisper.cpp, a response from an LLM (Ollama), and hear it spoken back via Piper TTS.

![Conversation Demo](spec/screenshots/conversation-demo.png)

Browser captures mic audio over WebSocket. Gateway decodes, re-samples to 16kHz, and runs energy-based voice activity detector. When speech ends, post to whisper.cpp for transcription, stream the transcript to Ollama for a response, and pipe each completed sentence to Piper TTS while the LLM is still generating. Audio is sent via WebSocket. GPU-bound services (whisper.cpp, Ollama) run on the host for direct ROCm access (I am running an AMD GPU).

The gateway uses pipeline architecture. Each WebSocket connection gets its own goroutine with context-based cancellation. LLM and TTS stages overlap via channels so you hear the first sentence before the LLM finishes generating. A semaphore caps concurrent calls at a configurable limit (default 100), returning 503 when full.

## Setup

Build whisper.cpp (auto-detects ROCm/CUDA):

```bash
./scripts/build-whisper-server.sh
export PATH="$HOME/.local/bin:$PATH"
```

Download models:

```bash
./scripts/download-models.sh
```

Start host services:

```bash
./scripts/start-host-services.sh
```

Start the stack:

```bash
docker compose up
```

Open http://localhost:3001, pick a voice (Fast or Quality), click Start Mic.

## Monitoring

Prometheus scrapes the gateway's `/metrics` endpoint. Grafana is pre-provisioned with a dashboard covering active calls, calls/sec, per-stage latency (ASR, LLM, TTS), E2E percentiles, error rates, and audio throughput.

Prometheus: http://localhost:9090
Grafana: http://localhost:3002 (admin/admin)

## Load testing

```bash
docker compose run --rm loadtest --concurrency 10 --duration 30s
```

Prints p50/p95/p99 per stage.

## Config

Everything is in `.env`. The important ones:

| Variable               | Default     | What it does                                 |
| ---------------------- | ----------- | -------------------------------------------- |
| OLLAMA_MODEL           | llama3.2:3b | Which LLM to use                             |
| DEFAULT_TTS_ENGINE     | fast        | fast (low latency) or quality (better voice) |
| MAX_CONCURRENT_CALLS   | 100         | Admission control limit                      |
| VAD_SILENCE_TIMEOUT_MS | 1000        | How long to wait after speech stops          |
| VAD_MIN_SPEECH_MS      | 500         | Ignore audio shorter than this               |
| ASR/LLM/TTS_POOL_SIZE  | 50          | HTTP connection pool per backend             |

## Project layout

```
services/
  gateway/          Go orchestrator (WebSocket, pipeline, VAD, codecs, metrics)
  frontend/         React app (AudioWorklet mic, VU meter, transcript, metrics panel)
  piper/            Go wrapper around piper CLI with two voice models
  loadtest/         Concurrent call simulator
  monitoring/       Prometheus + Grafana dashboards
scripts/
  build-whisper-server.sh
  start-host-services.sh
  download-models.sh
```
