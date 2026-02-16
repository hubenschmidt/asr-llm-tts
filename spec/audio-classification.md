# Audio Classification

Two-pass audio classification added to the pipeline: (1) scene/event detection pre-ASR to identify speech vs noise/music/silence, (2) emotion/sentiment detection post-ASR for call analytics. Both use encoder-only models in a Python+PyTorch sidecar. Both run as non-blocking goroutines — zero impact on E2E latency.

## Models (encoder-only, CPU-friendly)

| Model | Size | Latency | Labels |
|---|---|---|---|
| YAMNet (scene) | ~14 MB ONNX | ~5 ms/chunk CPU | 521 AudioSet classes → collapsed to 5: speech, music, noise, silence, other |
| emotion2vec base (emotion) | ~90 MB | ~50 ms/2 s segment CPU | 6: neutral, happy, angry, sad, frustrated, surprised |

## Sidecar Service: `audioclassify` (port 5300)

Python + FastAPI + PyTorch.

### Endpoints

| Method | Path | Body | Response |
|---|---|---|---|
| GET | `/health` | — | `{"status":"ok"}` |
| POST | `/scene` | binary float32 LE samples | `{"label":"speech","confidence":0.87,"scores":{...},"latency_ms":4.2}` |
| POST | `/emotion` | binary float32 LE samples | `{"label":"frustrated","confidence":0.72,"scores":{...},"latency_ms":48.1}` |

Models loaded at startup; inference on threadpool executor for non-blocking async.

## Pipeline Integration

### Scene classification
Goroutine in `ProcessChunk()` after denoise (line ~123), before VAD. Fire-and-forget, emits WS event.

### Emotion classification
Goroutine in `runFullPipeline()` parallel to ASR call. Fire-and-forget, emits WS event.

Both gated by `AudioClassification bool` in `pipeline.Config` (default false).

## WS Events

```json
{"type":"classification","scene":{"label":"speech","confidence":0.87,"scores":{"speech":0.87,"music":0.05,"noise":0.04,"silence":0.03,"other":0.01}}}
{"type":"classification","emotion":{"label":"frustrated","confidence":0.72,"scores":{"neutral":0.10,"happy":0.03,"angry":0.08,"sad":0.05,"frustrated":0.72,"surprised":0.02}}}
```

## Gateway Client (`pipeline/classify.go`)

`ClassifyClient` struct following `NoiseClient` pattern (noise.go):
- `ClassifyScene(ctx, samples) (*ClassifyResult, error)` — POST binary, parse JSON
- `ClassifyEmotion(ctx, samples) (*ClassifyResult, error)` — POST binary, parse JSON

## Config Threading

- `callMetadata.AudioClassification bool` → `pipeline.Config.AudioClassification` + `ClassifyClient`
- `AUDIOCLASSIFY_URL` env var in `main.go`, conditional registration
- Frontend: checkbox toggle in ASR Tuning, localStorage-persisted

## Frontend

- **MetricsPanel**: "Audio Classification" section with scene label + emotion label/confidence
- **CallPanel**: checkbox toggle with tooltip in ASR Tuning section

## Prometheus Metrics

| Metric | Type | Labels |
|---|---|---|
| `classify_scene_total` | CounterVec | label |
| `classify_emotion_total` | CounterVec | label |
| `classify_duration_seconds` | HistogramVec | type (scene/emotion) |

## Docker

- `services/audioclassify/` with Dockerfile, requirements.txt, main.py, models.py
- `docker-compose.yml` service entry (port 5300, healthcheck)
- `torch==2.5.0+cpu` to avoid CUDA/ROCm pull

## Latency Budget

| Classification | Latency | Blocks pipeline? |
|---|---|---|
| Scene (YAMNet) | ~5 ms | No (goroutine) |
| Emotion (emotion2vec) | ~50 ms | No (parallel w/ ASR ~180 ms) |

## Future Extensions

1. Scene-gated VAD (skip VAD when scene=noise with high confidence)
2. Emotion-informed LLM prompting (thread emotion into system prompt)
3. ONNX Runtime optimization (2-3× speedup over PyTorch)
4. Emotion time series visualization
