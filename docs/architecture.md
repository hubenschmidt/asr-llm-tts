# Pipeline Architecture

## End-to-End Flow

```mermaid
flowchart LR
    subgraph Browser
        MIC[Microphone]
        SPK[Speaker]
        UI[CallPanel UI]
    end

    subgraph Gateway ["Gateway (Go)"]
        direction TB
        WS[WebSocket Handler]
        DEC[Decode Opus/PCM]
        RS[Resample → 16 kHz]
        DN[RNNoise Denoise]
        VAD[Voice Activity Detection]
    end

    subgraph Pipeline ["Pipeline Stages"]
        direction TB
        ASR[ASR — Whisper]
        LLM[LLM — Ollama]
        TTS[TTS — Piper / Kokoro]
        CLS[Audio Classification]
    end

    MIC -- "binary audio" --> WS
    UI -- "callMetadata JSON\n(first frame)" --> WS
    WS --> DEC --> RS --> DN --> VAD

    VAD -- "speech ended" --> ASR
    VAD -- "parallel" -.-> CLS

    ASR -- "transcript" --> LLM
    LLM -- "sentence pipelining" --> TTS

    TTS -- "audio bytes" --> WS
    ASR -- "transcript event" --> WS
    LLM -- "llm_token events" --> WS
    CLS -. "emotion event" .-> WS

    WS -- "events + audio" --> SPK
    WS -- "events" --> UI
```

## Audio Processing Detail

```mermaid
flowchart LR
    subgraph Input
        RAW[Raw Audio\nOpus or PCM]
    end

    subgraph Preprocessing
        DEC2[Codec Decode\n→ float32 samples]
        RS2[Resample\n→ 16 kHz mono]
        DN2[RNNoise\nNoise Suppression]
        VAD2[VAD\nSilbero VAD]
    end

    subgraph ASR Detail
        WAV[Encode as WAV\n16 kHz mono PCM]
        HTTP["HTTP multipart POST\n→ Whisper server"]
        MEL["Log-Mel Spectrogram\n(80 bins × 3000)\ncomputed by Whisper"]
        ENC[Transformer Encoder\n→ hidden states]
        DEC3[Token Decoder\n→ text transcript]
    end

    RAW --> DEC2 --> RS2 --> DN2 --> VAD2
    VAD2 -- "speech segment\nfloat32[]" --> WAV --> HTTP --> MEL --> ENC --> DEC3
```

## Sentence Pipelining (LLM + TTS)

The LLM and TTS stages are **not** fully sequential. The gateway uses sentence pipelining — TTS begins synthesizing the first complete sentence while the LLM continues generating.

```mermaid
sequenceDiagram
    participant ASR
    participant LLM as LLM (Ollama)
    participant TTS as TTS (Piper/Kokoro)
    participant WS as WebSocket → Client

    ASR->>LLM: transcript
    activate LLM

    LLM-->>WS: llm_token (streaming)
    LLM->>TTS: sentence 1 complete
    activate TTS
    LLM-->>WS: llm_token (streaming)

    TTS->>WS: audio chunk (sentence 1)
    deactivate TTS

    LLM->>TTS: sentence 2 complete
    activate TTS
    deactivate LLM

    TTS->>WS: audio chunk (sentence 2)
    deactivate TTS

    WS->>WS: metrics event
```

## WebSocket Event Types

| Event | Direction | Payload |
|-------|-----------|---------|
| `callMetadata` | client → server | JSON: codec, sample_rate, engines, mode, prompts |
| binary frame | client → server | Encoded audio (Opus/PCM) |
| `transcript` | server → client | ASR text, latency |
| `llm_token` | server → client | Streaming token |
| `llm_done` | server → client | Full response text |
| `tts_ready` | server → client | Binary audio bytes |
| `emotion` | server → client | Audio classification result |
| `metrics` | server → client | ASR/LLM/TTS/total latency (ms), WER, no_speech_prob |

## Latency Breakdown

```mermaid
gantt
    title Typical E2E Latency (~2s)
    dateFormat X
    axisFormat %L ms

    section Sequential
    ASR (Whisper)           :active, asr, 0, 200
    LLM TTFT + generation   :active, llm, 200, 1700
    TTS sentence 1          :active, tts, 1700, 2050

    section Parallel
    Audio Classification    :done, cls, 0, 150
    TTS sentence 2          :active, tts2, 1900, 2200
```
