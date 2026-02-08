import { createSignal, onMount, Show, For } from "solid-js";

import { fetchModels as apiFetchModels, preloadModel, unloadModel } from "../api/models";
import { unloadAllGPU } from "../api/gpu";
import { fetchServices as apiFetchServices, startService as apiStartService, stopService as apiStopService } from "../api/services";
import { warmupTTS } from "../api/tts";
import "../style/call-panel.css";
import { useAudioStream } from "../hooks/useAudioStream";
import { GPUPanel } from "./GPUPanel";
import { MetricsPanel } from "./MetricsPanel";

const DEFAULT_PROMPT = "You are a helpful call center agent. Keep responses concise and conversational.";

const ENGINE_TO_SERVICE = {
  fast: "piper", quality: "piper", high: "piper",
  kokoro: "kokoro", chatterbox: "chatterbox", melotts: "melotts",
  "faster-whisper": "faster-whisper", "whisper-server": "whisper-server",
};

export function CallPanel() {
  const [ttsEngine, setTtsEngine] = createSignal("");
  const [sttEngine, setSttEngine] = createSignal("");
  const [systemPrompt, setSystemPrompt] = createSignal(DEFAULT_PROMPT);
  const [llmModel, setLlmModel] = createSignal("");
  const [llmModels, setLlmModels] = createSignal([]);
  const [loadingSTT, setLoadingSTT] = createSignal(false);
  const [loadingLLM, setLoadingLLM] = createSignal(false);
  const [loadingTTS, setLoadingTTS] = createSignal(false);
  const [availableTTS, setAvailableTTS] = createSignal([]);
  const [transcripts, setTranscripts] = createSignal([]);
  const [llmResponse, setLlmResponse] = createSignal("");
  const [latestMetrics, setLatestMetrics] = createSignal(null);
  const [metricsHistory, setMetricsHistory] = createSignal([]);
  const [error, setError] = createSignal(null);
  const [micLevel, setMicLevel] = createSignal(0);
  const [serviceStatuses, setServiceStatuses] = createSignal({});

  let playAudioCtx = null;
  let playAt = 0;
  let fileInput;

  const fetchModels = () => {
    apiFetchModels()
      .then((data) => {
        setLlmModels(data.llm.models);
        if (data.tts?.engines) setAvailableTTS(data.tts.engines);
      })
      .catch(() => {});
  };

  const SERVICE_TO_STT = {
    "whisper-server": "whisper-server",
    "faster-whisper": "faster-whisper",
  };

  const fetchServices = () => {
    apiFetchServices()
      .then((data) => {
        const map = {};
        for (const svc of data) map[svc.name] = svc.status;
        setServiceStatuses(map);
        if (sttEngine()) return;
        for (const svc of data) {
          if (svc.category !== "stt") continue;
          if (svc.status !== "healthy" && svc.status !== "running") continue;
          const engine = SERVICE_TO_STT[svc.name];
          if (engine) { setSttEngine(engine); return; }
        }
      })
      .catch(() => {});
  };

  onMount(() => {
    fetchModels();
    fetchServices();
  });

  const startService = async (serviceName) => {
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "starting" }));
    await apiStartService(serviceName);
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "healthy" }));
  };

  const stopService = async (serviceName) => {
    await apiStopService(serviceName);
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "stopped" }));
  };

  const RED = "#e74c3c";
  const YELLOW = "#f1c40f";
  const GREEN = "#2ecc71";

  const sttDotColor = () => {
    if (!sttEngine()) return RED;
    if (loadingSTT()) return YELLOW;
    const svc = ENGINE_TO_SERVICE[sttEngine()];
    if (!svc) return RED;
    const s = serviceStatuses()[svc] ?? "stopped";
    if (s === "healthy") return GREEN;
    if (s === "running" || s === "starting") return YELLOW;
    return RED;
  };

  const llmDotColor = () => {
    if (loadingLLM()) return YELLOW;
    if (llmModel()) return GREEN;
    return RED;
  };

  const ttsDotColor = () => {
    if (!ttsEngine()) return RED;
    if (loadingTTS()) return YELLOW;
    const svc = ENGINE_TO_SERVICE[ttsEngine()];
    if (!svc) return ttsEngine() ? GREEN : RED;
    const s = serviceStatuses()[svc] ?? "stopped";
    if (s === "healthy") return GREEN;
    if (s === "running" || s === "starting") return YELLOW;
    return RED;
  };

  const playAudio = (data) => {
    if (!playAudioCtx) playAudioCtx = new AudioContext();
    const ctx = playAudioCtx;
    ctx.decodeAudioData(data.slice(0)).then((buf) => {
      const source = ctx.createBufferSource();
      source.buffer = buf;
      source.connect(ctx.destination);
      const startAt = Math.max(ctx.currentTime, playAt);
      source.start(startAt);
      playAt = startAt + buf.duration;
    });
  };

  const { isStreaming, startMic, startFile, stop } = useAudioStream({
    ttsEngine,
    sttEngine,
    systemPrompt,
    llmModel,
    onTranscript: (text) => setTranscripts((prev) => [...prev, { role: "user", text }]),
    onLLMToken: (token) => setLlmResponse((prev) => prev + token),
    onLLMDone: (text) => {
      setTranscripts((prev) => [...prev, { role: "agent", text }]);
      setLlmResponse("");
    },
    onAudio: playAudio,
    onMetrics: (m) => {
      setLatestMetrics(m);
      setMetricsHistory((prev) => [...prev, m]);
    },
    onError: (msg) => setError(msg),
    onLevel: setMicLevel,
  });

  const handleSTTChange = (e) => {
    const engine = e.target.value;
    setSttEngine(engine);
    const svc = ENGINE_TO_SERVICE[engine];
    if (!svc || serviceStatuses()[svc] === "healthy") return;
    setLoadingSTT(true);
    startService(svc)
      .catch((err) => setError(`STT start failed: ${err instanceof Error ? err.message : err}`))
      .finally(() => setLoadingSTT(false));
  };

  const handleLLMChange = (e) => {
    const model = e.target.value;
    if (!model) return;
    setLlmModel(model);
    setLoadingLLM(true);
    preloadModel(model)
      .catch((err) => setError(`Model preload failed: ${err instanceof Error ? err.message : err}`))
      .finally(() => setLoadingLLM(false));
  };

  const handleTTSChange = (e) => {
    const engine = e.target.value;
    if (!engine) return;
    setTtsEngine(engine);
    setLoadingTTS(true);
    const svc = ENGINE_TO_SERVICE[engine];
    const ready = svc && serviceStatuses()[svc] !== "healthy"
      ? startService(svc)
      : Promise.resolve();
    ready
      .then(() => warmupTTS(engine))
      .catch((err) => setError(`TTS failed: ${err instanceof Error ? err.message : err}`))
      .finally(() => setLoadingTTS(false));
  };

  const handleFileSelect = (e) => {
    const file = e.target.files?.[0];
    if (file) startFile(file);
  };

  return (
    <div class="layout">
      <div class="main">
        <h2 class="page-title">ASR → LLM → TTS Pipeline</h2>

        <GPUPanel onUnloadAll={() => {
          unloadAllGPU()
            .then(() => {
              setSttEngine("");
              setLlmModel("");
              setTtsEngine("");
              setServiceStatuses({});
            })
            .catch((err) => setError(`Unload all failed: ${err instanceof Error ? err.message : err}`));
        }} />

        <div class="model-column">
          {/* STT Engine */}
          <div class="model-group">
            <label class="label">
              <StatusDot color={sttDotColor()} />
              STT Engine
              <Tooltip text="Speech-to-text engine. whisper ROCm uses GPU acceleration. faster-whisper uses INT8 quantization for 4x speed on CPU." />
            </label>
            <div class="model-row-inner">
              <select
                value={sttEngine()}
                onChange={handleSTTChange}
                class="select"
                style={{ flex: "1" }}
                disabled={isStreaming() || loadingSTT()}
              >
                <Show when={!sttEngine()}>
                  <option value="">Select engine...</option>
                </Show>
                <optgroup label="whisper-server (GPU)">
                  <option value="whisper-server">whisper-server GPU (medium)</option>
                </optgroup>
                <optgroup label="faster-whisper (INT8, CPU)">
                  <option value="faster-whisper">faster-whisper tiny-int8</option>
                </optgroup>
              </select>
              <Show when={loadingSTT()}>
                <span class="spinner" />
              </Show>
              <button
                class="unload-btn"
                disabled={isStreaming() || loadingSTT() || !sttEngine()}
                onClick={() => {
                  const svc = ENGINE_TO_SERVICE[sttEngine()];
                  if (!svc) { setSttEngine(""); return; }
                  setLoadingSTT(true);
                  stopService(svc)
                    .then(() => setSttEngine(""))
                    .catch((err) => setError(`STT stop failed: ${err instanceof Error ? err.message : err}`))
                    .finally(() => setLoadingSTT(false));
                }}
              >Unload</button>
            </div>
          </div>

          {/* Language Model */}
          <div class="model-group">
            <label class="label">
              <StatusDot color={llmDotColor()} />
              Language Model
              <Tooltip text="Generates the text response from your transcribed speech. Larger models produce better answers but increase latency." />
            </label>
            <div class="model-row-inner">
              <select
                value={llmModel()}
                onChange={handleLLMChange}
                class="select"
                style={{ flex: "1" }}
                disabled={isStreaming() || loadingLLM()}
              >
                <Show when={!llmModel()}>
                  <option value="">Select model...</option>
                </Show>
                <For each={llmModels()}>
                  {(m) => <option value={m}>{m}</option>}
                </For>
              </select>
              <Show when={loadingLLM()}>
                <span class="spinner" />
              </Show>
              <button
                class="unload-btn"
                disabled={isStreaming() || loadingLLM() || !llmModel()}
                onClick={() => {
                  setLoadingLLM(true);
                  unloadModel("llm", llmModel())
                    .then(() => setLlmModel(""))
                    .catch((err) => setError(`Unload failed: ${err instanceof Error ? err.message : err}`))
                    .finally(() => setLoadingLLM(false));
                }}
              >Unload</button>
            </div>
          </div>

          {/* TTS Model */}
          <div class="model-group">
            <label class="label">
              <StatusDot color={ttsDotColor()} />
              TTS Model
              <Tooltip text="Controls the voice output. Piper is lightweight CPU with 3 quality tiers. Kokoro offers professional CPU quality." />
            </label>
            <div class="model-row-inner">
              <select
                value={ttsEngine()}
                onChange={handleTTSChange}
                class="select"
                style={{ flex: "1" }}
                disabled={isStreaming() || loadingTTS()}
              >
                <Show when={!ttsEngine()}>
                  <option value="">Select model...</option>
                </Show>
                <optgroup label="Piper (CPU)">
                  <option value="fast">Piper Fast, lowest latency (6MB)</option>
                  <option value="quality">Piper Quality, balanced (17MB)</option>
                  <option value="high">Piper High, most natural (109MB)</option>
                </optgroup>
                <optgroup label="Other Engines">
                  <option value="kokoro">Kokoro, professional, CPU (82M)</option>
                  <option value="chatterbox">Chatterbox, near-ElevenLabs quality (350M)</option>
                  <option value="melotts">MeloTTS, CPU real-time, multi-accent (208M)</option>
                  <option value="elevenlabs" disabled={!availableTTS().includes("elevenlabs")}>
                    ElevenLabs, cloud API, low latency{!availableTTS().includes("elevenlabs") ? " — not configured" : ""}
                  </option>
                </optgroup>
              </select>
              <Show when={loadingTTS()}>
                <span class="spinner" />
              </Show>
              <button
                class="unload-btn"
                disabled={isStreaming() || loadingTTS() || !ttsEngine()}
                onClick={() => {
                  const svc = ENGINE_TO_SERVICE[ttsEngine()];
                  setTtsEngine("");
                  if (svc) stopService(svc).catch(() => {});
                }}
              >Unload</button>
            </div>
          </div>
        </div>

        <div class="controls">
          <Show
            when={!isStreaming()}
            fallback={<button onClick={stop} class="btn btn-danger">Stop</button>}
          >
            <button
              onClick={startMic}
              class="btn"
              disabled={loadingLLM() || loadingTTS() || !llmModel() || !ttsEngine()}
            >
              {loadingLLM() ? "Loading model..." : loadingTTS() ? "Checking TTS..." : "Start Mic"}
            </button>
            <button
              onClick={() => fileInput.click()}
              class="btn btn-secondary"
              disabled={loadingLLM() || loadingTTS() || !llmModel() || !ttsEngine()}
            >
              Upload Audio
            </button>
            <input
              ref={fileInput}
              type="file"
              accept="audio/*"
              onChange={handleFileSelect}
              style={{ display: "none" }}
            />
          </Show>
        </div>

        <textarea
          value={systemPrompt()}
          onInput={(e) => setSystemPrompt(e.currentTarget.value)}
          class="prompt"
          disabled={isStreaming()}
          rows={2}
          placeholder="System prompt..."
        />

        <Show when={isStreaming()}>
          <VUMeter level={micLevel()} />
        </Show>

        <Show when={error()}>
          <div class="error-box">{error()}</div>
        </Show>

        <div class="transcript-box">
          <h3 class="transcript-heading">Transcript</h3>
          <For each={transcripts()}>
            {(t) => (
              <p class={`transcript-line ${t.role === "agent" ? "transcript-agent" : "transcript-user"}`}>
                <strong>{t.role === "agent" ? "Agent: " : "You: "}</strong>
                {t.text}
              </p>
            )}
          </For>
          <Show when={llmResponse()}>
            <p class="transcript-streaming">
              <strong>Agent: </strong>{llmResponse()}
            </p>
          </Show>
          <Show when={transcripts().length === 0 && !llmResponse()}>
            <p class="transcript-placeholder">Waiting for audio input...</p>
          </Show>
        </div>
      </div>

      <MetricsPanel metrics={latestMetrics()} history={metricsHistory()} />
    </div>
  );
}

function StatusDot(props) {
  return <span class="status-dot" style={{ background: props.color }} />;
}

function Tooltip(props) {
  return (
    <span class="tooltip-wrap">
      <span class="help-icon">?</span>
      <span class="tooltip">{props.text}</span>
    </span>
  );
}

function VUMeter(props) {
  const pct = () => Math.min(100, props.level * 500);
  const color = () => pct() < 30 ? "#2ecc71" : pct() < 70 ? "#f1c40f" : "#e74c3c";
  return (
    <div class="vu-track">
      <div class="vu-bar" style={{ width: `${pct()}%`, background: color() }} />
    </div>
  );
}
