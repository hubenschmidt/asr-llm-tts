import { createSignal, onMount, Show, For } from "solid-js";

import type { PipelineMetrics } from "../hooks/useAudioStream";
import { useAudioStream } from "../hooks/useAudioStream";
import { GPUPanel } from "./GPUPanel";
import { MetricsPanel } from "./MetricsPanel";

const DEFAULT_PROMPT = "You are a helpful call center agent. Keep responses concise and conversational.";

interface ServiceInfo {
  name: string;
  status: string;
  category: string;
}

const ENGINE_TO_SERVICE: Record<string, string> = {
  fast: "piper", quality: "piper", high: "piper",
  kokoro: "kokoro", chatterbox: "chatterbox", melotts: "melotts",
  "faster-whisper": "faster-whisper", "whisper-server": "whisper-server",
};

export function CallPanel() {
  const [ttsEngine, setTtsEngine] = createSignal("");
  const [sttEngine, setSttEngine] = createSignal("");
  const [systemPrompt, setSystemPrompt] = createSignal(DEFAULT_PROMPT);
  const [llmModel, setLlmModel] = createSignal("");
  const [llmModels, setLlmModels] = createSignal<string[]>([]);
  const [loadingLLM, setLoadingLLM] = createSignal(false);
  const [loadingTTS, setLoadingTTS] = createSignal(false);
  const [availableTTS, setAvailableTTS] = createSignal<string[]>([]);
  const [transcripts, setTranscripts] = createSignal<{ role: string; text: string }[]>([]);
  const [llmResponse, setLlmResponse] = createSignal("");
  const [latestMetrics, setLatestMetrics] = createSignal<PipelineMetrics | null>(null);
  const [metricsHistory, setMetricsHistory] = createSignal<PipelineMetrics[]>([]);
  const [error, setError] = createSignal<string | null>(null);
  const [micLevel, setMicLevel] = createSignal(0);
  const [serviceStatuses, setServiceStatuses] = createSignal<Record<string, string>>({});

  let playAudioCtx: AudioContext | null = null;
  let playAt = 0;
  let fileInput!: HTMLInputElement;

  const fetchModels = () => {
    fetch("/api/models")
      .then((r) => r.json())
      .then((data: { llm: { models: string[] }; tts?: { engines: string[] }; asr?: { engines: string[] } }) => {
        setLlmModels(data.llm.models);
        if (data.tts?.engines) setAvailableTTS(data.tts.engines);
      })
      .catch(() => {});
  };

  const SERVICE_TO_STT: Record<string, string> = {
    "whisper-server": "whisper-server",
    "faster-whisper": "faster-whisper",
  };

  const fetchServices = () => {
    fetch("/api/services")
      .then((r) => r.json())
      .then((data: ServiceInfo[]) => {
        const map: Record<string, string> = {};
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

  const startService = async (serviceName: string): Promise<void> => {
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "starting" }));
    const resp = await fetch(`/api/services/${serviceName}/start`, { method: "POST" });
    if (!resp.ok) throw new Error(`Service ${serviceName} failed to start`);
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "healthy" }));
  };

  const stopService = async (serviceName: string): Promise<void> => {
    await fetch(`/api/services/${serviceName}/stop`, { method: "POST" });
    setServiceStatuses((prev) => ({ ...prev, [serviceName]: "stopped" }));
  };

  const RED = "#e74c3c";
  const YELLOW = "#f1c40f";
  const GREEN = "#2ecc71";

  const sttDotColor = (): string => {
    if (!sttEngine()) return RED;
    const svc = ENGINE_TO_SERVICE[sttEngine()];
    if (!svc) return RED;
    const s = serviceStatuses()[svc] ?? "stopped";
    if (s === "healthy") return GREEN;
    if (s === "running" || s === "starting") return YELLOW;
    return RED;
  };

  const llmDotColor = (): string => {
    if (loadingLLM()) return YELLOW;
    if (llmModel()) return GREEN;
    return RED;
  };

  const ttsDotColor = (): string => {
    if (!ttsEngine()) return RED;
    if (loadingTTS()) return YELLOW;
    const svc = ENGINE_TO_SERVICE[ttsEngine()];
    if (!svc) return ttsEngine() ? GREEN : RED;
    const s = serviceStatuses()[svc] ?? "stopped";
    if (s === "healthy") return GREEN;
    if (s === "running" || s === "starting") return YELLOW;
    return RED;
  };

  const playAudio = (data: ArrayBuffer) => {
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

  const handleSTTChange = (e: Event) => {
    const engine = (e.target as HTMLSelectElement).value;
    setSttEngine(engine);
    const svc = ENGINE_TO_SERVICE[engine];
    if (!svc || serviceStatuses()[svc] === "healthy") return;
    startService(svc)
      .catch((err) => setError(`STT start failed: ${err instanceof Error ? err.message : err}`));
  };

  const handleLLMChange = (e: Event) => {
    const model = (e.target as HTMLSelectElement).value;
    if (!model) return;
    setLlmModel(model);
    setLoadingLLM(true);
    fetch("/api/models/preload", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ model }),
    })
      .then((r) => { if (!r.ok) throw new Error("preload failed"); })
      .catch((err) => setError(`Model preload failed: ${err instanceof Error ? err.message : err}`))
      .finally(() => () => setLoadingLLM(false));
  };

  const handleTTSChange = (e: Event) => {
    const engine = (e.target as HTMLSelectElement).value;
    if (!engine) return;
    setTtsEngine(engine);
    setLoadingTTS(true);
    const svc = ENGINE_TO_SERVICE[engine];
    const ready = svc && serviceStatuses()[svc] !== "healthy"
      ? startService(svc)
      : Promise.resolve();
    ready
      .then(() => fetch("/api/tts/warmup", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ engine }),
      }))
      .then((r) => { if (!r.ok) throw new Error("warmup failed"); })
      .catch((err) => setError(`TTS failed: ${err instanceof Error ? err.message : err}`))
      .finally(() => () => setLoadingTTS(false));
  };

  const handleFileSelect = (e: Event) => {
    const file = (e.target as HTMLInputElement).files?.[0];
    if (file) startFile(file);
  };

  return (
    <div style={layoutStyle}>
      <style>{`
        .tooltip-wrap:hover .tooltip { visibility: visible !important; opacity: 1 !important; }
        @keyframes spin { to { transform: rotate(360deg); } }
      `}</style>
      <div style={mainStyle}>
        <h2 style={{ margin: "0 0 16px" }}>ASR → LLM → TTS Pipeline</h2>

        <GPUPanel />

        <div style={modelColumnStyle}>
          {/* STT Engine */}
          <div style={modelGroupStyle}>
            <label style={labelStyle}>
              <StatusDot color={sttDotColor()} />
              STT Engine
              <Tooltip text="Speech-to-text engine. whisper ROCm uses GPU acceleration. faster-whisper uses INT8 quantization for 4x speed on CPU." />
            </label>
            <div style={modelRowInnerStyle}>
              <select
                value={sttEngine()}
                onChange={handleSTTChange}
                style={{ ...selectStyle, flex: "1" }}
                disabled={isStreaming()}
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
              <button
                style={unloadBtnStyle}
                disabled={isStreaming() || !sttEngine()}
                onClick={() => {
                  const svc = ENGINE_TO_SERVICE[sttEngine()];
                  setSttEngine("");
                  if (svc) stopService(svc).catch(() => {});
                }}
              >Unload</button>
            </div>
          </div>

          {/* Language Model */}
          <div style={modelGroupStyle}>
            <label style={labelStyle}>
              <StatusDot color={llmDotColor()} />
              Language Model
              <Tooltip text="Generates the text response from your transcribed speech. Larger models produce better answers but increase latency." />
            </label>
            <div style={modelRowInnerStyle}>
              <select
                value={llmModel()}
                onChange={handleLLMChange}
                style={{ ...selectStyle, flex: "1" }}
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
                <span style={spinnerStyle} />
              </Show>
              <button
                style={unloadBtnStyle}
                disabled={isStreaming() || loadingLLM() || !llmModel()}
                onClick={() => {
                  fetch("/api/models/unload", {
                    method: "POST",
                    headers: { "Content-Type": "application/json" },
                    body: JSON.stringify({ type: "llm", model: llmModel() }),
                  })
                    .then((r) => {
                      if (!r.ok) throw new Error("unload failed");
                      setLlmModel("");
                      ;
                    })
                    .catch((err) => setError(`Unload failed: ${err instanceof Error ? err.message : err}`));
                }}
              >Unload</button>
            </div>
          </div>

          {/* TTS Model */}
          <div style={modelGroupStyle}>
            <label style={labelStyle}>
              <StatusDot color={ttsDotColor()} />
              TTS Model
              <Tooltip text="Controls the voice output. Piper is lightweight CPU with 3 quality tiers. Kokoro offers professional CPU quality." />
            </label>
            <div style={modelRowInnerStyle}>
              <select
                value={ttsEngine()}
                onChange={handleTTSChange}
                style={{ ...selectStyle, flex: "1" }}
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
                <span style={spinnerStyle} />
              </Show>
              <button
                style={unloadBtnStyle}
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

        <div style={controlsStyle}>
          <Show
            when={!isStreaming()}
            fallback={<button onClick={stop} style={btnDangerStyle}>Stop</button>}
          >
            <button
              onClick={startMic}
              style={btnStyle}
              disabled={loadingLLM() || loadingTTS() || !llmModel() || !ttsEngine()}
            >
              {loadingLLM() ? "Loading model..." : loadingTTS() ? "Checking TTS..." : "Start Mic"}
            </button>
            <button
              onClick={() => fileInput.click()}
              style={btnSecondaryStyle}
              disabled={loadingLLM() || loadingTTS() || !llmModel() || !ttsEngine()}
            >
              Upload Audio
            </button>
            <input
              ref={fileInput!}
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
          style={promptStyle}
          disabled={isStreaming()}
          rows={2}
          placeholder="System prompt..."
        />

        <Show when={isStreaming()}>
          <VUMeter level={micLevel()} />
        </Show>

        <Show when={error()}>
          <div style={errorStyle}>{error()}</div>
        </Show>

        <div style={transcriptBoxStyle}>
          <h3 style={{ margin: "0 0 8px", "font-size": "14px", color: "#aaa" }}>
            Transcript
          </h3>
          <For each={transcripts()}>
            {(t) => (
              <p style={{ margin: "4px 0", color: t.role === "agent" ? "#6cb4ee" : "#eee" }}>
                <strong>{t.role === "agent" ? "Agent: " : "You: "}</strong>
                {t.text}
              </p>
            )}
          </For>
          <Show when={llmResponse()}>
            <p style={{ color: "#6cb4ee", "font-style": "italic", margin: "4px 0" }}>
              <strong>Agent: </strong>{llmResponse()}
            </p>
          </Show>
          <Show when={transcripts().length === 0 && !llmResponse()}>
            <p style={{ color: "#555" }}>
              Waiting for audio input...
            </p>
          </Show>
        </div>
      </div>

      <MetricsPanel metrics={latestMetrics()} history={metricsHistory()} />
    </div>
  );
}

function StatusDot(props: { color: string }) {
  return <span style={{ ...statusDotStyle, background: props.color }} />;
}

function Tooltip(props: { text: string }) {
  return (
    <span class="tooltip-wrap" style={tooltipWrapStyle}>
      <span style={helpIconStyle}>?</span>
      <span class="tooltip" style={tooltipStyle}>{props.text}</span>
    </span>
  );
}

function VUMeter(props: { level: number }) {
  const pct = () => Math.min(100, props.level * 500);
  const color = () => pct() < 30 ? "#2ecc71" : pct() < 70 ? "#f1c40f" : "#e74c3c";
  return (
    <div style={vuTrackStyle}>
      <div style={{ ...vuBarStyle, width: `${pct()}%`, background: color() }} />
    </div>
  );
}

const vuTrackStyle = {
  height: "8px",
  background: "#1a1a2e",
  "border-radius": "4px",
  "margin-bottom": "12px",
  overflow: "hidden",
};

const vuBarStyle = {
  height: "100%",
  "border-radius": "4px",
  transition: "width 50ms linear",
};

const layoutStyle = {
  display: "flex",
  gap: "24px",
  "max-width": "960px",
  margin: "40px auto",
  padding: "0 16px",
};

const mainStyle = { flex: "1", "min-width": "0" };

const controlsStyle = {
  display: "flex",
  gap: "8px",
  "margin-bottom": "16px",
  "align-items": "center",
};

const selectStyle = {
  padding: "8px 12px",
  "border-radius": "4px",
  border: "1px solid #444",
  background: "#2a2a3e",
  color: "#eee",
  "max-width": "100%",
};

const btnStyle = {
  padding: "8px 20px",
  "border-radius": "4px",
  border: "none",
  background: "#2ecc71",
  color: "#fff",
  cursor: "pointer",
  "font-weight": "bold",
};

const btnSecondaryStyle = { ...btnStyle, background: "#3498db" };
const btnDangerStyle = { ...btnStyle, background: "#e74c3c" };

const modelColumnStyle = {
  display: "flex",
  "flex-direction": "column",
  gap: "12px",
  "margin-bottom": "16px",
};

const modelGroupStyle = { "min-width": "0" };

const modelRowInnerStyle = {
  display: "flex",
  "align-items": "center",
  gap: "8px",
};

const unloadBtnStyle = {
  padding: "6px 10px",
  "border-radius": "4px",
  border: "1px solid #555",
  background: "#333",
  color: "#ccc",
  cursor: "pointer",
  "font-size": "12px",
  "white-space": "nowrap",
  "flex-shrink": "0",
};

const labelStyle = {
  display: "block",
  "font-size": "12px",
  color: "#888",
  "margin-bottom": "6px",
  "text-transform": "uppercase",
  "letter-spacing": "1px",
};

const promptStyle = {
  width: "100%",
  padding: "8px 12px",
  "border-radius": "4px",
  border: "1px solid #444",
  background: "#2a2a3e",
  color: "#eee",
  "font-size": "13px",
  "font-family": "inherit",
  resize: "vertical",
  "margin-bottom": "12px",
  "box-sizing": "border-box",
};

const errorStyle = {
  background: "#e74c3c22",
  border: "1px solid #e74c3c",
  padding: "8px 12px",
  "border-radius": "4px",
  "margin-bottom": "12px",
  color: "#e74c3c",
};

const transcriptBoxStyle = {
  background: "#1a1a2e",
  "border-radius": "8px",
  padding: "16px",
  "min-height": "200px",
  color: "#eee",
};

const tooltipWrapStyle = {
  position: "relative",
  display: "inline-block",
  "margin-left": "4px",
};

const helpIconStyle = {
  display: "inline-flex",
  "align-items": "center",
  "justify-content": "center",
  width: "14px",
  height: "14px",
  "border-radius": "50%",
  border: "1px solid #666",
  "font-size": "10px",
  color: "#888",
  cursor: "help",
  "vertical-align": "middle",
};

const tooltipStyle = {
  visibility: "hidden",
  opacity: "0",
  position: "absolute",
  top: "calc(100% + 6px)",
  left: "0",
  background: "#2a2a3e",
  border: "1px solid #555",
  "border-radius": "6px",
  padding: "8px 10px",
  "font-size": "12px",
  color: "#ddd",
  width: "220px",
  "text-transform": "none",
  "letter-spacing": "0",
  "line-height": "1.4",
  "z-index": "10",
  transition: "opacity 150ms",
  "pointer-events": "none",
};

const statusDotStyle = {
  display: "inline-block",
  width: "8px",
  height: "8px",
  "border-radius": "50%",
  "margin-right": "6px",
  "vertical-align": "middle",
};

const spinnerStyle = {
  display: "inline-block",
  width: "16px",
  height: "16px",
  border: "2px solid #444",
  "border-top-color": "#3498db",
  "border-radius": "50%",
  animation: "spin 0.6s linear infinite",
  "flex-shrink": "0",
};
