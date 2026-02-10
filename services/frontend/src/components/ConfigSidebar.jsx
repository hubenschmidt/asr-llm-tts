import { Show, For } from "solid-js";

import { StatusDot, Tooltip } from "./TranscriptWidgets";
import { GPUPanel } from "./GPUPanel";

export const ConfigSidebar = (props) => {
  const { config: c, on } = props;

  return (
    <div class={`sidebar-left ${props.collapsed ? "collapsed" : ""}`}>
      <h2>Configuration</h2>

      <GPUPanel onUnloadAll={on.unloadAll} />

      <div class="model-column">
        {/* STT Engine */}
        <div class="model-group">
          <label class="label">
            <StatusDot color={on.sttDotColor()} />
            STT Engine
            <Tooltip text="Speech-to-text engine. whisper-server uses GPU acceleration via whisper.cpp." />
          </label>
          <div class="model-row-inner">
            <select
              value={c.sttEngine()}
              onChange={on.sttChange}
              class="select"
              disabled={c.isStreaming() || c.loadingSTT()}
            >
              <Show when={!c.sttEngine()}>
                <option value="">Select engine...</option>
              </Show>
              <optgroup label="whisper-server (GPU)">
                <option value="whisper-server">whisper-server (GPU)</option>
              </optgroup>
            </select>
            <Show when={c.loadingSTT()}>
              <span class="spinner" />
            </Show>
            <button
              class="unload-btn"
              disabled={c.isStreaming() || c.loadingSTT() || !c.sttEngine()}
              onClick={on.unloadSTT}
            >
              Unload
            </button>
          </div>
        </div>

        {/* STT Model (whisper-server only) */}
        <Show when={c.sttEngine() === "whisper-server" && c.sttModels().length > 0}>
          <div class="model-group">
            <label class="label">STT Model</label>
            <select
              value={c.sttModel()}
              onChange={on.sttModelChange}
              class="select"
              disabled={c.isStreaming() || c.loadingSTT()}
            >
              <Show when={!c.sttModel()}>
                <option value="">Select model...</option>
              </Show>
              <For each={c.sttModels()}>
                {(m) => (
                  <option value={m.name} disabled={!m.downloaded}>
                    {m.name.replace("ggml-", "").replace(".bin", "")}
                    {m.downloaded ? ` (${m.size_mb} MB)` : " — not downloaded"}
                  </option>
                )}
              </For>
            </select>
            <div class="stt-model-list">
              <For each={c.sttModels().filter((m) => !m.downloaded)}>
                {(m) => (
                  <div class="stt-model-download-row">
                    <span class="stt-model-name">{m.name.replace("ggml-", "").replace(".bin", "")}</span>
                    <Show
                      when={c.downloadingModel() === m.name && c.downloadProgress()}
                      fallback={
                        <button
                          class="unload-btn"
                          disabled={!!c.downloadingModel()}
                          onClick={() => on.sttModelDownload(m.name)}
                        >
                          {c.downloadingModel() === m.name ? "Starting..." : "Download"}
                        </button>
                      }
                    >
                      <div class="download-progress">
                        <div class="download-progress-bar">
                          <div
                            class="download-progress-fill"
                            style={{ width: `${c.downloadProgress().total > 0 ? (c.downloadProgress().bytes / c.downloadProgress().total * 100) : 0}%` }}
                          />
                        </div>
                        <span class="download-progress-text">
                          {Math.round(c.downloadProgress().bytes / 1024 / 1024)}
                          {c.downloadProgress().total > 0 ? ` / ${Math.round(c.downloadProgress().total / 1024 / 1024)} MB` : " MB"}
                        </span>
                      </div>
                    </Show>
                  </div>
                )}
              </For>
            </div>
          </div>
        </Show>

        {/* LLM Engine */}
        <div class="model-group">
          <label class="label">
            <StatusDot color={on.llmDotColor()} />
            LLM Engine
            <Tooltip text="LLM provider. Ollama runs locally; OpenAI and Anthropic use cloud APIs." />
          </label>
          <div class="model-row-inner">
            <select
              value={c.llmEngine()}
              onChange={on.llmEngineChange}
              class="select"
              disabled={c.isStreaming() || c.loadingLLM()}
            >
              <For each={c.availableLLMEngines()}>
                {(e) => <option value={e}>{e}</option>}
              </For>
            </select>
          </div>
        </div>

        {/* Language Model */}
        <div class="model-group">
          <label class="label">Language Model</label>
          <div class="model-row-inner">
            <select
              value={c.llmModel()}
              onChange={on.llmChange}
              class="select"
              disabled={c.isStreaming() || c.loadingLLM()}
            >
              <Show when={!c.llmModel()}>
                <option value="">Select model...</option>
              </Show>
              <For each={c.llmModels()}>
                {(m) => <option value={m}>{m}</option>}
              </For>
            </select>
            <Show when={c.loadingLLM()}>
              <span class="spinner" />
            </Show>
            <Show when={c.llmEngine() === "ollama"}>
              <button
                class="unload-btn"
                disabled={c.isStreaming() || c.loadingLLM() || !c.llmModel()}
                onClick={on.unloadLLM}
              >
                Unload
              </button>
            </Show>
          </div>
        </div>

        {/* TTS Model */}
        <div class="model-group">
          <label class="label">
            <StatusDot color={on.ttsDotColor()} />
            TTS Model
            <Tooltip text="Controls the voice output. Piper is lightweight CPU with 3 quality tiers. Kokoro offers professional CPU quality." />
          </label>
          <div class="model-row-inner">
            <select
              value={c.ttsEngine()}
              onChange={on.ttsChange}
              class="select"
              disabled={c.isStreaming() || c.loadingTTS()}
            >
              <Show when={!c.ttsEngine()}>
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
                  disabled={!c.availableTTS().includes("elevenlabs")}
                >
                  ElevenLabs, cloud API, low latency
                  {!c.availableTTS().includes("elevenlabs") ? " — not configured" : ""}
                </option>
              </optgroup>
            </select>
            <Show when={c.loadingTTS()}>
              <span class="spinner" />
            </Show>
            <button
              class="unload-btn"
              disabled={c.isStreaming() || c.loadingTTS() || !c.ttsEngine()}
              onClick={on.unloadTTS}
            >
              Unload
            </button>
          </div>
        </div>
      </div>

    </div>
  );
};
