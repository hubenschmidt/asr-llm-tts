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
        {/* ASR Engine */}
        <div class="model-group">
          <label class="label">
            <StatusDot color={on.asrDotColor()} />
            ASR Engine
            <Tooltip text="Speech-to-text engine. whisper-server uses GPU acceleration via whisper.cpp." />
          </label>
          <div class="model-row-inner">
            <select
              value={c.asrEngine()}
              onChange={on.asrChange}
              class="select"
              disabled={c.isStreaming() || c.loadingASR()}
            >
              <Show when={!c.asrEngine()}>
                <option value="">Select engine...</option>
              </Show>
              <optgroup label="whisper-server (GPU)">
                <option value="whisper-server">whisper-server (GPU)</option>
              </optgroup>
            </select>
            <Show when={c.loadingASR()}>
              <span class="spinner" />
            </Show>
            <button
              class="unload-btn"
              disabled={c.isStreaming() || c.loadingASR() || !c.asrEngine()}
              onClick={on.unloadASR}
            >
              Unload
            </button>
          </div>
        </div>

        {/* ASR Model (whisper-server only) */}
        <Show when={c.asrEngine() === "whisper-server" && c.asrModels().length > 0}>
          <div class="model-group">
            <label class="label">ASR Model</label>
            <select
              value={c.asrModel()}
              onChange={on.asrModelChange}
              class="select"
              disabled={c.isStreaming() || c.loadingASR()}
            >
              <Show when={!c.asrModel()}>
                <option value="">Select model...</option>
              </Show>
              <For each={c.asrModels()}>
                {(m) => (
                  <option value={m.name} disabled={!m.downloaded}>
                    {m.name.replace("ggml-", "").replace(".bin", "")}
                    {m.downloaded ? ` (${m.size_mb} MB)` : " — not downloaded"}
                  </option>
                )}
              </For>
            </select>
            <div class="asr-model-list">
              <For each={c.asrModels().filter((m) => !m.downloaded)}>
                {(m) => (
                  <div class="asr-model-download-row">
                    <span class="asr-model-name">{m.name.replace("ggml-", "").replace(".bin", "")}</span>
                    <Show
                      when={c.downloadingModel() === m.name && c.downloadProgress()}
                      fallback={
                        <button
                          class="unload-btn"
                          disabled={!!c.downloadingModel()}
                          onClick={() => on.asrModelDownload(m.name)}
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

        {/* Language Model */}
        <div class="model-group">
          <label class="label">
            <StatusDot color={on.llmDotColor()} />
            Language Model
            <Tooltip text="LLM provider + model. Ollama runs locally; OpenAI and Anthropic use cloud APIs." />
          </label>
          <div class="model-row-inner">
            <select
              value={c.llmModel()}
              onChange={on.llmModelChange}
              class="select"
              disabled={c.isStreaming() || c.loadingLLM()}
            >
              <Show when={!c.llmModel()}>
                <option value="">Select model...</option>
              </Show>
              <For each={c.allLLMModels()}>
                {(group) => (
                  <optgroup label={group.label}>
                    <For each={group.models}>
                      {(m) => <option value={m}>{m}</option>}
                    </For>
                  </optgroup>
                )}
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
        {/* Audio Bandwidth */}
        <Show when={c.bandwidthModes().length > 0}>
          <div class="model-group">
            <label class="label">Audio Bandwidth</label>
            <select
              value={c.audioBandwidth()}
              onChange={on.bandwidthChange}
              class="select"
              disabled={c.isStreaming()}
            >
              <For each={c.bandwidthModes()}>
                {(m) => <option value={m.id}>{m.label}</option>}
              </For>
            </select>
          </div>
        </Show>
      </div>

    </div>
  );
};
