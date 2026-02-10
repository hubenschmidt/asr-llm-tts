import { createSignal, onMount } from "solid-js";

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
import { MetricsPanel } from "./MetricsPanel";
import { ConfigSidebar } from "./ConfigSidebar";
import { CenterPanel } from "./CenterPanel";

const PROMPT_PRESETS = {
  general: "You are a helpful call center agent. Keep responses concise and conversational.",
  algos: `You are an algorithm implementation assistant. When given an algorithm name or problem description:
1. Implement the solution immediately — do NOT ask clarifying questions
2. Add clear inline comments explaining each step of the algorithm
3. Include time and space complexity as a comment at the top
4. Use clean, idiomatic code in the requested language (default: JavaScript)
5. Include a brief usage example after the implementation`,
};

// Only host-managed services that need start/stop via whisper-control.
// Docker services (piper, kokoro, melotts) are always running.
const ENGINE_TO_SERVICE = {
  "whisper-server": "whisper-server",
};

const CLOUD_MODELS = {
  openai: ["gpt-5.2-codex", "gpt-5.2", "gpt-5-mini", "gpt-5-nano"],
  anthropic: ["claude-opus-4-6", "claude-sonnet-4-5", "claude-haiku-4-5"],
};

export const CallPanel = () => {
  const [ttsEngine, _setTtsEngine] = createSignal(localStorage.getItem("ttsEngine") || "");
  const [sttEngine, setSttEngine] = createSignal("");
  const [promptPreset, setPromptPreset] = createSignal(localStorage.getItem("promptPreset") || "general");
  const [systemPrompt, setSystemPrompt] = createSignal(localStorage.getItem("systemPrompt") || PROMPT_PRESETS.general);
  const [llmModel, _setLlmModel] = createSignal("");
  const [llmEngine, _setLlmEngine] = createSignal(localStorage.getItem("llmEngine") || "ollama");

  const setTtsEngine = (v) => { _setTtsEngine(v); localStorage.setItem("ttsEngine", v); };
  const setLlmModel = (v) => { _setLlmModel(v); localStorage.setItem("llmModel", v); };
  const setLlmEngine = (v) => { _setLlmEngine(v); localStorage.setItem("llmEngine", v); };
  const [ollamaModels, setOllamaModels] = createSignal([]);
  const allLLMModels = () => {
    const groups = [];
    if (ollamaModels().length > 0) groups.push({ label: "Ollama (Local)", models: ollamaModels() });
    if (availableLLMEngines().includes("openai")) groups.push({ label: "OpenAI (Cloud)", models: CLOUD_MODELS.openai });
    if (availableLLMEngines().includes("anthropic")) groups.push({ label: "Anthropic (Cloud)", models: CLOUD_MODELS.anthropic });
    return groups;
  };

  const modelToEngine = (model) => {
    for (const [engine, models] of Object.entries(CLOUD_MODELS)) {
      if (models.includes(model)) return engine;
    }
    return "ollama";
  };
  const [loadingSTT, setLoadingSTT] = createSignal(false);
  const [loadingLLM, setLoadingLLM] = createSignal(false);
  const [loadingTTS, setLoadingTTS] = createSignal(false);
  const [availableTTS, setAvailableTTS] = createSignal([]);
  const [availableLLMEngines, setAvailableLLMEngines] = createSignal(["ollama"]);
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
  const [mode, setMode] = createSignal(localStorage.getItem("callMode") || "talk");
  const [explainText, setExplainText] = createSignal(null);
  const [leftCollapsed, setLeftCollapsed] = createSignal(false);
  const [rightCollapsed, setRightCollapsed] = createSignal(false);

  const setSttModel = (v) => { _setSttModel(v); localStorage.setItem("sttModel", v); };

  let playAudioCtx = null;
  let playAt = 0;
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
        setOllamaModels(data.llm.models);
        if (data.llm?.engines) setAvailableLLMEngines(data.llm.engines);
        if (data.tts?.engines) setAvailableTTS(data.tts.engines);

        // Validate saved model belongs to the current engine's model list
        const cloud = CLOUD_MODELS[llmEngine()];
        const validModels = cloud || data.llm.models;
        if (llmModel() && validModels.includes(llmModel())) return;

        // Reset to a sensible default for the current engine
        if (cloud) { setLlmModel(cloud[0]); return; }
        if (data.llm.loaded?.length > 0) { setLlmModel(data.llm.loaded[0]); return; }
        const saved = localStorage.getItem("llmModel");
        if (saved && data.llm.models.includes(saved)) { setLlmModel(saved); return; }
        setLlmModel("");
      })
      .catch(() => {});
  };

  const SERVICE_TO_STT = {
    "whisper-server": "whisper-server",
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

  const { isStreaming, isRecording, startMic, startSnippet, pauseRecording, resumeRecording, processSnippet, startFile, stop, sendChat } = useAudioStream({
    ttsEngine,
    sttEngine,
    systemPrompt,
    llmModel,
    llmEngine,
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

  const handleLLMModelChange = (e) => {
    const model = e.target.value;
    if (!model) return;
    const engine = modelToEngine(model);
    const prev = llmModel();
    setLlmEngine(engine);
    setLlmModel(model);
    if (engine !== "ollama") return;
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

  const handleUnloadSTT = () => {
    const svc = ENGINE_TO_SERVICE[sttEngine()];
    if (!svc) { setSttEngine(""); return; }
    setLoadingSTT(true);
    stopService(svc)
      .then(() => setSttEngine(""))
      .catch((err) =>
        setError(`STT stop failed: ${err instanceof Error ? err.message : err}`),
      )
      .finally(() => setLoadingSTT(false));
  };

  const handleUnloadLLM = () => {
    setLoadingLLM(true);
    unloadModel("llm", llmModel())
      .then(() => setLlmModel(""))
      .catch((err) =>
        setError(`Unload failed: ${err instanceof Error ? err.message : err}`),
      )
      .finally(() => setLoadingLLM(false));
  };

  const handleUnloadTTS = () => {
    const svc = ENGINE_TO_SERVICE[ttsEngine()];
    setTtsEngine("");
    if (svc) stopService(svc).catch(() => {});
  };

  const handleUnloadAll = () => {
    unloadAllGPU()
      .then(() => {
        setSttEngine("");
        setLlmModel("");
        setTtsEngine("");
        setServiceStatuses({});
      })
      .catch((err) =>
        setError(`Unload all failed: ${err instanceof Error ? err.message : err}`),
      );
  };

  const handleSystemPromptChange = (e) => {
    setSystemPrompt(e.currentTarget.value);
    localStorage.setItem("systemPrompt", e.currentTarget.value);
  };

  const handlePromptPreset = (name) => {
    setPromptPreset(name);
    localStorage.setItem("promptPreset", name);
    const prompt = PROMPT_PRESETS[name] || PROMPT_PRESETS.general;
    setSystemPrompt(prompt);
    localStorage.setItem("systemPrompt", prompt);
  };

  const configProps = {
    sttEngine, sttModel, sttModels, llmEngine, llmModel, allLLMModels, ttsEngine,
    availableTTS, loadingSTT, loadingLLM, loadingTTS, isStreaming,
    systemPrompt, promptPreset, serviceStatuses, downloadingModel, downloadProgress,
  };

  const configHandlers = {
    sttChange: handleSTTChange,
    sttModelChange: handleSTTModelChange,
    sttModelDownload: handleSTTModelDownload,
    llmModelChange: handleLLMModelChange,
    ttsChange: handleTTSChange,
    unloadSTT: handleUnloadSTT,
    unloadLLM: handleUnloadLLM,
    unloadTTS: handleUnloadTTS,
    unloadAll: handleUnloadAll,
    systemPromptChange: handleSystemPromptChange,
    promptPresetChange: handlePromptPreset,
    sttDotColor,
    llmDotColor,
    ttsDotColor,
  };

  const centerProps = {
    transcripts, llmResponse, pendingThinking, isStreaming, isRecording, soundChecking,
    micLevel, error, loadingLLM, loadingTTS, llmModel, llmEngine, ttsEngine, mode, explainText,
  };

  const handleSetMode = (m) => { setMode(m); localStorage.setItem("callMode", m); };

  const handleSendChat = (text) => {
    setTranscripts((prev) => [...prev, { role: "user", text }]);
    sendChat(text);
  };

  const centerHandlers = {
    toggleSoundCheck,
    stop,
    startMic: () => { if (soundChecking()) stopSoundCheck(); startMic(); },
    startFile,
    setMode: handleSetMode,
    startSnippet: () => { if (soundChecking()) stopSoundCheck(); startSnippet(); },
    pauseRecording,
    resumeRecording,
    processSnippet,
    sendChat: handleSendChat,
    setExplainText: (text) => setExplainText(text),
    closeExplain: () => setExplainText(null),
  };

  return (
    <div class="layout">
      <ConfigSidebar config={configProps} on={configHandlers} collapsed={leftCollapsed()} />
      <button class="sidebar-toggle" onClick={() => setLeftCollapsed((v) => !v)}>
        {leftCollapsed() ? "\u203A" : "\u2039"}
      </button>
      <CenterPanel config={centerProps} on={centerHandlers} />
      <button class="sidebar-toggle" onClick={() => setRightCollapsed((v) => !v)}>
        {rightCollapsed() ? "\u2039" : "\u203A"}
      </button>
      <div class={`sidebar-right ${rightCollapsed() ? "collapsed" : ""}`}>
        <MetricsPanel metrics={latestMetrics()} history={metricsHistory()} />
        <div class="model-group" style={{ "margin-top": "12px" }}>
          <label class="label">System Prompt</label>
          <div class="mode-toggle" style={{ "margin-bottom": "6px" }}>
            <button
              class={`btn btn-sm ${promptPreset() === "general" ? "" : "btn-secondary"}`}
              onClick={() => handlePromptPreset("general")}
              disabled={isStreaming()}
            >
              General
            </button>
            <button
              class={`btn btn-sm ${promptPreset() === "algos" ? "" : "btn-secondary"}`}
              onClick={() => handlePromptPreset("algos")}
              disabled={isStreaming()}
            >
              Algos
            </button>
          </div>
          <textarea
            value={systemPrompt()}
            onInput={handleSystemPromptChange}
            class="prompt"
            disabled={isStreaming()}
            rows={14}
            placeholder="System prompt..."
          />
        </div>
      </div>
    </div>
  );
};
