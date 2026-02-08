import { Show, For } from "solid-js";

import { TranscriptEntry, StreamingEntry, VUMeter, talkBtnLabel } from "./TranscriptWidgets";

export const CenterPanel = (props) => {
  const { config: c, on } = props;
  let fileInput;

  const handleFileSelect = (e) => {
    const file = e.target.files?.[0];
    if (file) on.startFile(file);
  };

  return (
    <div class="center-panel">
      <div class="transcript-box">
        <h3 class="transcript-heading">Transcript</h3>
        <For each={c.transcripts()}>
          {(t) => (
            <TranscriptEntry role={t.role} text={t.text} thinking={t.thinking} />
          )}
        </For>
        <Show when={c.llmResponse() || c.pendingThinking()}>
          <StreamingEntry text={c.llmResponse()} thinking={c.pendingThinking()} />
        </Show>
        <Show when={c.transcripts().length === 0 && !c.llmResponse()}>
          <p class="transcript-placeholder">Waiting for audio input...</p>
        </Show>
      </div>

      <Show when={c.isStreaming() || c.soundChecking()}>
        <VUMeter level={c.micLevel()} />
      </Show>

      <Show when={c.error()}>
        <div class="error-box">{c.error()}</div>
      </Show>

      <div class="controls">
        <button
          onClick={on.toggleSoundCheck}
          class={`btn ${c.soundChecking() ? "btn-danger" : "btn-secondary"}`}
          disabled={c.isStreaming()}
        >
          {c.soundChecking() ? "Stop Check" : "Sound Check"}
        </button>
        <Show
          when={!c.isStreaming()}
          fallback={
            <button onClick={on.stop} class="btn btn-danger">
              Stop
            </button>
          }
        >
          <button
            onClick={on.startMic}
            class="btn"
            disabled={c.loadingLLM() || c.loadingTTS() || !c.llmModel() || !c.ttsEngine()}
          >
            {talkBtnLabel(c.loadingLLM(), c.loadingTTS())}
          </button>
          <button
            onClick={() => fileInput.click()}
            class="btn btn-secondary"
            disabled={c.loadingLLM() || c.loadingTTS() || !c.llmModel() || !c.ttsEngine()}
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
  );
};
