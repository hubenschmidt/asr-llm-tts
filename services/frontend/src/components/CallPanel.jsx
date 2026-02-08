import { createSignal, onMount, Show, For } from "solid-js";

import {
  fetchModels as apiFetchModels,
  preloadModel,
  unloadModel,
} from "../api/models";
import { unloadAllGPU } from "../api/gpu";
import {
  fetchServices as apiFetchServices,
  startService as apiStartService,
  stopService as apiStopService,
  fetchSTTModels as apiFetchSTTModels,
  downloadSTTModel as apiDownloadSTTModel,
} from "../api/services";
import { warmupTTS } from "../api/tts";
import "../style/call-panel.css";
import { useAudioStream } from "../hooks/useAudioStream";
import { GPUPanel } from "./GPUPanel";
import { MetricsPanel } from "./MetricsPanel";

const DEFAULT_PROMPT =
  "You are a helpful call center agent. Keep responses concise and conversational.";

// Only host-managed services that need start/stop via whisper-control.
// Docker services (piper, kokoro, melotts, faster-whisper) are always running.
const ENGINE_TO_SERVICE = {
  "whisper-server": "whisper-server",
};

export const CallPanel = () => {
  const [ttsEngine, _setTtsEngine] = createSignal(localStorage.getItem("ttsEngine") || "");
  const [sttEngine, setSttEngine] = createSignal("");
  const [systemPrompt, setSystemPrompt] = createSignal(localStorage.getItem("systemPrompt") || DEFAULT_PROMPT);
  const [llmModel, _setLlmModel] = createSignal("");

  const setTtsEngine = (v) => { _setTtsEngine(v); localStorage.setItem("ttsEngine", v); };
  const setLlmModel = (v) => { _setLlmModel(v); localStorage.setItem("llmModel", v); };
  const [llmModels, setLlmModels] = createSignal([]);
  const [loadingSTT, setLoadingSTT] = createSignal(false);
  const [loadingLLM, setLoadingLLM] = createSignal(false);
  const [loadingTTS, setLoadingTTS] = createSignal(false);
  const [availableTTS, setAvailableTTS] = createSignal([]);
  const [transcripts, setTranscripts] = createSignal([]);
  const [llmResponse, setLlmResponse] = createSignal("");
  const [pendingThinking, setPendingThinking] = createSignal("");
  const [latestMetrics, setLatestMetrics] = createSignal(null);
  const [metricsHistory, setMetricsHistory] = createSignal([]);
  const [error, setError] = createSignal(null);
  const [micLevel, setMicLevel] = createSignal(0);
  const [serviceStatuses, setServiceStatuses] = createSignal({});
  const [soundChecking, setSoundChecking] = createSignal(false);
  const [sttModels, setSttModels] = createSignal([]);
  const [sttModel, _setSttModel] = createSignal(localStorage.getItem("sttModel") || "");
  const [downloadingModel, setDownloadingModel] = createSignal("");
  const [downloadProgress, setDownloadProgress] = createSignal(null);

  const setSttModel = (v) => { _setSttModel(v); localStorage.setItem("sttModel", v); };

  let playAudioCtx = null;
  let playAt = 0;
  let fileInput;
  let scCtx = null;
  let scStream = null;
  let scRaf = null;

  const stopSoundCheck = () => {
    cancelAnimationFrame(scRaf);
    scStream?.getTracks().forEach((t) => t.stop());
    scCtx?.close();
    scCtx = null;
    scStream = null;
    scRaf = null;
    setSoundChecking(false);
    setMicLevel(0);
  };

  const toggleSoundCheck = async () => {
    if (soundChecking()) { stopSoundCheck(); return; }
    try {
      scCtx = new AudioContext();
      scStream = await navigator.mediaDevices.getUserMedia({
        audio: { echoCancellation: true, autoGainControl: true, noiseSuppression: true },
      });
      const source = scCtx.createMediaStreamSource(scStream);
      const analyser = scCtx.createAnalyser();
      analyser.fftSize = 256;
      source.connect(analyser);
      const buf = new Float32Array(analyser.fftSize);
      const pump = () => {
        analyser.getFloatTimeDomainData(buf);
        let sum = 0;
        for (let i = 0; i < buf.length; i++) sum += buf[i] * buf[i];
        setMicLevel(Math.sqrt(sum / buf.length));
        scRaf = requestAnimationFrame(pump);
      };
      setSoundChecking(true);
      pump();
    } catch (err) {
      setError(`Mic access failed: ${err instanceof Error ? err.message : err}`);
    }
  };

  const fetchModels = () => {
    apiFetchModels()
      .then((data) => {
        setLlmModels(data.llm.models);
        if (data.tts?.engines) setAvailableTTS(data.tts.engines);
        if (llmModel()) return;
        if (data.llm.loaded?.length > 0) { setLlmModel(data.llm.loaded[0]); return; }
        const saved = localStorage.getItem("llmModel");
        if (saved && data.llm.models.includes(saved)) setLlmModel(saved);
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
        setServiceStatuses(Object.fromEntries(data.map((svc) => [svc.name, svc.status])));
        if (sttEngine()) return;
        const healthySTT = data
          .filter((svc) => svc.category === "stt")
          .filter((svc) => svc.status === "healthy" || svc.status === "running")
          .map((svc) => SERVICE_TO_STT[svc.name])
          .find((engine) => engine);
        if (healthySTT) setSttEngine(healthySTT);
      })
      .catch(() => {});
  };

  const fetchSTTModels = () => {
    apiFetchSTTModels()
      .then((data) => {
        setSttModels(data.models || []);
        if (!sttModel() && data.active) setSttModel(data.active);
      })
      .catch(() => {});
  };

  onMount(() => {
    fetchModels();
    fetchServices();
    fetchSTTModels();
  });

  const startService = async (serviceName, params) => {
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "starting" }));
    await apiStartService(serviceName, params);
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "healthy" }));
  };

  const stopService = async (serviceName) => {
    await apiStopService(serviceName);
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "stopped" }));
  };

  const RED = "#e74c3c";
  const YELLOW = "#f1c40f";
  const GREEN = "#2ecc71";
  const STATUS_COLORS = { healthy: GREEN, running: YELLOW, starting: YELLOW };

  const serviceColor = (svc) => STATUS_COLORS[serviceStatuses()[svc]] ?? RED;

  const sttDotColor = () => {
    if (!sttEngine()) return RED;
    if (loadingSTT()) return YELLOW;
    const svc = ENGINE_TO_SERVICE[sttEngine()];
    if (!svc) return GREEN;
    return serviceColor(svc);
  };

  const llmDotColor = () => {
    if (loadingLLM()) return YELLOW;
    return llmModel() ? GREEN : RED;
  };

  const ttsDotColor = () => {
    if (!ttsEngine()) return RED;
    if (loadingTTS()) return YELLOW;
    const svc = ENGINE_TO_SERVICE[ttsEngine()];
    if (!svc) return GREEN;
    return serviceColor(svc);
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
    onTranscript: (text) =>
      setTranscripts((prev) => [...prev, { role: "user", text }]),
    onLLMToken: (token) => setLlmResponse((prev) => prev + token),
    onLLMDone: (text) => {
      setTranscripts((prev) => [...prev, { role: "agent", text, thinking: pendingThinking() }]);
      setLlmResponse("");
      setPendingThinking("");
    },
    onThinkingDone: (text) => setPendingThinking(text),
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
    const prevSvc = ENGINE_TO_SERVICE[sttEngine()];
    setSttEngine(engine);
    const svc = ENGINE_TO_SERVICE[engine];
    const unload = prevSvc && prevSvc !== svc ? stopService(prevSvc) : Promise.resolve();
    if (!svc || serviceStatuses()[svc] === "healthy") {
      unload.catch(() => {});
      return;
    }
    setLoadingSTT(true);
    const modelParam = sttModel() ? `model=${sttModel()}` : "";
    unload
      .then(() => startService(svc, modelParam))
      .catch((err) =>
        setError(`STT start failed: ${err instanceof Error ? err.message : err}`),
      )
      .finally(() => setLoadingSTT(false));
  };

  const handleSTTModelChange = (e) => {
    const model = e.target.value;
    if (!model) return;
    setSttModel(model);
    const svc = ENGINE_TO_SERVICE[sttEngine()];
    if (!svc) return;
    setLoadingSTT(true);
    stopService(svc)
      .then(() => startService(svc, `model=${model}`))
      .catch((err) =>
        setError(`STT model switch failed: ${err instanceof Error ? err.message : err}`),
      )
      .finally(() => setLoadingSTT(false));
  };

  const handleSTTModelDownload = (name) => {
    setDownloadingModel(name);
    setDownloadProgress(null);
    apiDownloadSTTModel(name, (bytes, total) => setDownloadProgress({ bytes, total }))
      .then(() => fetchSTTModels())
      .catch((err) =>
        setError(`Download failed: ${err instanceof Error ? err.message : err}`),
      )
      .finally(() => { setDownloadingModel(""); setDownloadProgress(null); });
  };

  const handleLLMChange = (e) => {
    const model = e.target.value;
    if (!model) return;
    const prev = llmModel();
    setLlmModel(model);
    setLoadingLLM(true);
    const unload = prev && prev !== model ? unloadModel("llm", prev) : Promise.resolve();
    unload
      .then(() => preloadModel(model))
      .catch((err) =>
        setError(`Model preload failed: ${err instanceof Error ? err.message : err}`),
      )
      .finally(() => setLoadingLLM(false));
  };

  const handleTTSChange = (e) => {
    const engine = e.target.value;
    if (!engine) return;
    setTtsEngine(engine);
    setLoadingTTS(true);
    const svc = ENGINE_TO_SERVICE[engine];
    const needsStart = svc && serviceStatuses()[svc] !== "healthy";
    const ready = needsStart ? startService(svc) : Promise.resolve();
    ready
      .then(() => warmupTTS(engine))
      .catch((err) =>
        setError(`TTS failed: ${err instanceof Error ? err.message : err}`),
      )
      .finally(() => setLoadingTTS(false));
  };

  const handleFileSelect = (e) => {
    const file = e.target.files?.[0];
    if (file) startFile(file);
  };

  return (
    <div class="layout">
      {/* ── Left Sidebar: Config ── */}
      <div class="sidebar-left">
        <h2>Configuration</h2>

        <GPUPanel
          onUnloadAll={() => {
            unloadAllGPU()
              .then(() => {
                setSttEngine("");
                setLlmModel("");
                setTtsEngine("");
                setServiceStatuses({});
              })
              .catch((err) =>
                setError(
                  `Unload all failed: ${err instanceof Error ? err.message : err}`,
                ),
              );
          }}
        />

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
                disabled={isStreaming() || loadingSTT()}
              >
                <Show when={!sttEngine()}>
                  <option value="">Select engine...</option>
                </Show>
                <optgroup label="whisper-server (GPU)">
                  <option value="whisper-server">whisper-server (GPU)</option>
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
                  if (!svc) {
                    setSttEngine("");
                    return;
                  }
                  setLoadingSTT(true);
                  stopService(svc)
                    .then(() => setSttEngine(""))
                    .catch((err) =>
                      setError(`STT stop failed: ${err instanceof Error ? err.message : err}`),
                    )
                    .finally(() => setLoadingSTT(false));
                }}
              >
                Unload
              </button>
            </div>
          </div>

          {/* STT Model (whisper-server only) */}
          <Show when={sttEngine() === "whisper-server" && sttModels().length > 0}>
            <div class="model-group">
              <label class="label">STT Model</label>
              <select
                value={sttModel()}
                onChange={handleSTTModelChange}
                class="select"
                disabled={isStreaming() || loadingSTT()}
              >
                <Show when={!sttModel()}>
                  <option value="">Select model...</option>
                </Show>
                <For each={sttModels()}>
                  {(m) => (
                    <option value={m.name} disabled={!m.downloaded}>
                      {m.name.replace("ggml-", "").replace(".bin", "")}
                      {m.downloaded ? ` (${m.size_mb} MB)` : " — not downloaded"}
                    </option>
                  )}
                </For>
              </select>
              <div class="stt-model-list">
                <For each={sttModels().filter((m) => !m.downloaded)}>
                  {(m) => (
                    <div class="stt-model-download-row">
                      <span class="stt-model-name">{m.name.replace("ggml-", "").replace(".bin", "")}</span>
                      <Show
                        when={downloadingModel() === m.name && downloadProgress()}
                        fallback={
                          <button
                            class="unload-btn"
                            disabled={!!downloadingModel()}
                            onClick={() => handleSTTModelDownload(m.name)}
                          >
                            {downloadingModel() === m.name ? "Starting..." : "Download"}
                          </button>
                        }
                      >
                        <div class="download-progress">
                          <div class="download-progress-bar">
                            <div
                              class="download-progress-fill"
                              style={{ width: `${downloadProgress().total > 0 ? (downloadProgress().bytes / downloadProgress().total * 100) : 0}%` }}
                            />
                          </div>
                          <span class="download-progress-text">
                            {Math.round(downloadProgress().bytes / 1024 / 1024)}
                            {downloadProgress().total > 0 ? ` / ${Math.round(downloadProgress().total / 1024 / 1024)} MB` : " MB"}
                          </span>
                        </div>
                      </Show>
                    </div>
                  )}
                </For>
              </div>
            </div>
          </Show>

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
                    .catch((err) =>
                      setError(`Unload failed: ${err instanceof Error ? err.message : err}`),
                    )
                    .finally(() => setLoadingLLM(false));
                }}
              >
                Unload
              </button>
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
                  <option value="melotts">MeloTTS, CPU real-time, multi-accent (208M)</option>
                  <option
                    value="elevenlabs"
                    disabled={!availableTTS().includes("elevenlabs")}
                  >
                    ElevenLabs, cloud API, low latency
                    {!availableTTS().includes("elevenlabs") ? " — not configured" : ""}
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
              >
                Unload
              </button>
            </div>
          </div>
        </div>

        <div class="sidebar-section">
          <div class="sidebar-section-label">System Prompt</div>
          <textarea
            value={systemPrompt()}
            onInput={(e) => { setSystemPrompt(e.currentTarget.value); localStorage.setItem("systemPrompt", e.currentTarget.value); }}
            class="prompt"
            disabled={isStreaming()}
            rows={4}
            placeholder="System prompt..."
          />
        </div>
      </div>

      {/* ── Center: Transcript + Controls ── */}
      <div class="center-panel">
        <div class="transcript-box">
          <h3 class="transcript-heading">Transcript</h3>
          <For each={transcripts()}>
            {(t) => (
              <TranscriptEntry role={t.role} text={t.text} thinking={t.thinking} />
            )}
          </For>
          <Show when={llmResponse() || pendingThinking()}>
            <StreamingEntry text={llmResponse()} thinking={pendingThinking()} />
          </Show>
          <Show when={transcripts().length === 0 && !llmResponse()}>
            <p class="transcript-placeholder">Waiting for audio input...</p>
          </Show>
        </div>

        <Show when={isStreaming() || soundChecking()}>
          <VUMeter level={micLevel()} />
        </Show>

        <Show when={error()}>
          <div class="error-box">{error()}</div>
        </Show>

        <div class="controls">
          <button
            onClick={toggleSoundCheck}
            class={`btn ${soundChecking() ? "btn-danger" : "btn-secondary"}`}
            disabled={isStreaming()}
          >
            {soundChecking() ? "Stop Check" : "Sound Check"}
          </button>
          <Show
            when={!isStreaming()}
            fallback={
              <button onClick={stop} class="btn btn-danger">
                Stop
              </button>
            }
          >
            <button
              onClick={() => { if (soundChecking()) stopSoundCheck(); startMic(); }}
              class="btn"
              disabled={loadingLLM() || loadingTTS() || !llmModel() || !ttsEngine()}
            >
              {talkBtnLabel(loadingLLM(), loadingTTS())}
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
      </div>

      {/* ── Right Sidebar: Metrics ── */}
      <div class="sidebar-right">
        <MetricsPanel metrics={latestMetrics()} history={metricsHistory()} />
      </div>
    </div>
  );
};

const StatusDot = (props) => (
  <span class="status-dot" style={{ background: props.color }} />
);

const Tooltip = (props) => (
  <span class="tooltip-wrap">
    <span class="help-icon">?</span>
    <span class="tooltip">{props.text}</span>
  </span>
);

const TranscriptEntry = (props) => {
  const [showThinking, setShowThinking] = createSignal(false);
  const isAgent = () => props.role === "agent";
  return (
    <div class={`transcript-line ${isAgent() ? "transcript-agent" : "transcript-user"}`}>
      <p>
        <strong>{isAgent() ? "Agent: " : "You: "}</strong>
        {props.text}
      </p>
      <Show when={isAgent() && props.thinking}>
        <button class="thinking-toggle" onClick={() => setShowThinking((v) => !v)}>
          {showThinking() ? "Hide reasoning" : "Show reasoning"}
        </button>
        <Show when={showThinking()}>
          <pre class="thinking-block">{props.thinking}</pre>
        </Show>
      </Show>
    </div>
  );
};

const StreamingEntry = (props) => {
  const [showThinking, setShowThinking] = createSignal(false);
  return (
    <div class="transcript-line transcript-agent">
      <Show when={props.text}>
        <p class="transcript-streaming">
          <strong>Agent: </strong>
          {props.text}
        </p>
      </Show>
      <Show when={props.thinking}>
        <button class="thinking-toggle" onClick={() => setShowThinking((v) => !v)}>
          {showThinking() ? "Hide reasoning" : "Show reasoning"}
        </button>
        <Show when={showThinking()}>
          <pre class="thinking-block">{props.thinking}</pre>
        </Show>
      </Show>
    </div>
  );
};

const talkBtnLabel = (loadingLLM, loadingTTS) => {
  if (loadingLLM) return "Loading model...";
  if (loadingTTS) return "Checking TTS...";
  return "Talk";
};

const vuColor = (pct) => {
  if (pct >= 70) return "#e74c3c";
  if (pct >= 30) return "#f1c40f";
  return "#2ecc71";
};

const VUMeter = (props) => {
  const pct = () => Math.min(100, props.level * 500);
  const color = () => vuColor(pct());
  return (
    <div class="vu-track">
      <div class="vu-bar" style={{ width: `${pct()}%`, background: color() }} />
    </div>
  );
};
