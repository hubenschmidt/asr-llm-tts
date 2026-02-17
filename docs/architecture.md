# Pipeline Architecture

## End-to-End Flow

```mermaid
flowchart LR
    subgraph Browser
        MIC[Microphone]
        SPK[Speaker]
        UI[CallPanel UI]
    end

    subgraph GW ["Gateway - Go"]
        direction TB
        WS[WebSocket Handler]
        DEC[Decode Opus/PCM]
        RS["Resample to 16 kHz"]
        DN[RNNoise Denoise]
        VAD[Voice Activity Detection]
    end

    subgraph Backends ["Pipeline Stages"]
        direction TB
        ASR["ASR (Whisper)"]
        LLM["LLM (Ollama)"]
        TTS["TTS (Piper)"]
        CLS[Audio Classification]
    end

    MIC -- binary audio --> WS
    UI -- callMetadata JSON --> WS
    WS --> DEC --> RS --> DN --> VAD

    VAD -- speech ended --> ASR
    VAD -. parallel .-> CLS

    ASR -- transcript --> LLM
    LLM -- sentence pipelining --> TTS

    TTS -- audio bytes --> WS
    ASR -- transcript event --> WS
    LLM -- llm_token events --> WS
    CLS -. emotion event .-> WS

    WS -- events + audio --> SPK
    WS -- events --> UI
```

## Audio Processing Detail

```mermaid
flowchart TB
    RAW["Raw Audio - Opus or PCM"] --> DEC2["Codec Decode to float32"]
    DEC2 --> RS2["Resample to 16 kHz mono"]
    RS2 --> DN2["RNNoise Noise Suppression"]
    DN2 --> VAD2["Silero VAD"]

    VAD2 -- "speech segment" --> WAV["Encode as WAV"]

    subgraph Gateway
        RAW
        DEC2
        RS2
        DN2
        VAD2
        WAV
    end

    WAV -- "HTTP multipart POST" --> MEL["Log-Mel Spectrogram<br/>80 bins x 3000"]
    MEL --> ENC["Transformer Encoder"]
    ENC --> DEC3["Token Decoder"]
    DEC3 --> OUT["Text Transcript"]

    subgraph Whisper ["Whisper Server"]
        MEL
        ENC
        DEC3
        OUT
    end
```

## Sentence Pipelining (LLM + TTS)

The LLM and TTS stages are **not** fully sequential. The gateway uses sentence pipelining: TTS begins synthesizing the first complete sentence while the LLM continues generating.

```mermaid
sequenceDiagram
    participant ASR
    participant LLM as LLM - Ollama
    participant TTS as TTS - Piper
    participant WS as WebSocket to Client

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
```

## WebSocket Event Types

| Event | Direction | Payload |
|-------|-----------|---------|
| `callMetadata` | client to server | JSON: codec, sample_rate, engines, mode, prompts |
| binary frame | client to server | Encoded audio (Opus/PCM) |
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
