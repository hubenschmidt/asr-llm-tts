# Pipeline Architecture

## End-to-End Flow

```mermaid
flowchart TB
    MIC["Browser Microphone"]:::browser -- "binary audio + callMetadata" --> WS["WebSocket Handler"]:::gateway

    subgraph Gateway
        WS --> DEC["Decode PCM/G.711"]:::gateway
        DEC --> RS["Resample to 16 kHz"]:::gateway
        RS --> DN["RNNoise Denoise"]:::gateway
        DN --> VAD["Silero VAD"]:::gateway
    end

    VAD -- "speech ended" --> ASR["ASR - Whisper"]:::asr
    VAD -. "parallel" .-> CLS["Audio Classification"]:::asr
    ASR -- "transcript" --> LLM["LLM - Ollama"]:::llm
    LLM -- "sentence pipelining" --> TTS["TTS - Piper"]:::tts

    ASR -- "transcript event" --> WS
    LLM -- "llm_token events" --> WS
    TTS -- "audio bytes" --> WS
    CLS -. "emotion event" .-> WS

    WS -- "events + audio" --> SPK["Browser Speaker + UI"]:::browser

    classDef browser fill:#3b82f6,stroke:#1e40af,color:#fff
    classDef gateway fill:#6366f1,stroke:#4338ca,color:#fff
    classDef asr fill:#f59e0b,stroke:#b45309,color:#fff
    classDef llm fill:#10b981,stroke:#047857,color:#fff
    classDef tts fill:#ef4444,stroke:#b91c1c,color:#fff
```

## Audio Processing Detail

```mermaid
flowchart TB
    RAW["Raw Audio - PCM or G.711"]:::input --> DEC2["Codec Decode to float32"]:::preprocess
    DEC2 --> RS2["Resample to 16 kHz mono"]:::preprocess
    RS2 --> DN2["RNNoise Noise Suppression"]:::preprocess
    DN2 --> VAD2["Silero VAD"]:::preprocess

    VAD2 -- "speech segment" --> WAV["Encode as WAV"]:::preprocess

    subgraph Gateway
        RAW
        DEC2
        RS2
        DN2
        VAD2
        WAV
    end

    WAV -- "HTTP multipart POST" --> MEL["Log-Mel Spectrogram<br/>80 bins x 3000"]:::whisper
    MEL --> ENC["Transformer Encoder"]:::whisper
    ENC --> DEC3["Token Decoder"]:::whisper
    DEC3 --> OUT["Text Transcript"]:::output

    subgraph Whisper ["Whisper Server"]
        MEL
        ENC
        DEC3
        OUT
    end

    classDef input fill:#3b82f6,stroke:#1e40af,color:#fff
    classDef preprocess fill:#6366f1,stroke:#4338ca,color:#fff
    classDef whisper fill:#f59e0b,stroke:#b45309,color:#fff
    classDef output fill:#10b981,stroke:#047857,color:#fff
```

## Sentence Pipelining (LLM + TTS)

The LLM and TTS stages are **not** fully sequential. The gateway uses sentence pipelining: TTS begins synthesizing the first complete sentence while the LLM continues generating.

```mermaid
sequenceDiagram
    box rgb(245, 158, 11) ASR
        participant ASR
    end
    box rgb(16, 185, 129) LLM
        participant LLM as LLM - Ollama
    end
    box rgb(239, 68, 68) TTS
        participant TTS as TTS - Piper
    end
    box rgb(59, 130, 246) Client
        participant WS as WebSocket to Client
    end

    rect rgba(30, 30, 30, 0.85)
        ASR->>LLM: transcript
        activate LLM

        LLM-->>WS: llm_token streaming
        LLM->>TTS: sentence 1 complete
        activate TTS
        LLM-->>WS: llm_token streaming

        TTS->>WS: audio chunk sentence 1
        deactivate TTS

        LLM->>TTS: sentence 2 complete
        activate TTS
        deactivate LLM

        TTS->>WS: audio chunk sentence 2
        deactivate TTS

        WS->>WS: metrics event
    end
```

## Color Legend

| Color | Component |
|-------|-----------|
| **Blue** | Browser / Client |
| **Indigo** | Gateway preprocessing |
| **Amber** | ASR - Whisper |
| **Green** | LLM - Ollama |
| **Red** | TTS - Piper |

## WebSocket Event Types

| Event | Direction | Payload |
|-------|-----------|---------|
| `callMetadata` | client to server | JSON: codec, sample_rate, engines, mode, prompts |
| binary frame | client to server | Encoded audio (PCM/G.711) |
| `transcript` | server to client | ASR text, latency |
| `llm_token` | server to client | Streaming token |
| `llm_done` | server to client | Full response text |
| `tts_ready` | server to client | Binary audio bytes |
| `emotion` | server to client | Audio classification result |
| `metrics` | server to client | ASR/LLM/TTS/total latency ms, WER, no_speech_prob |

## Latency Breakdown

```mermaid
gantt
    title Typical E2E Latency
    dateFormat X
    axisFormat %L ms

    section Sequential
    ASR Whisper              :active, asr, 0, 200
    LLM TTFT + generation    :active, llm, 200, 1700
    TTS sentence 1           :active, tts, 1700, 2050

    section Parallel
    Audio Classification     :done, cls, 0, 150
    TTS sentence 2           :active, tts2, 1900, 2200
```
