import { createSignal, createEffect, Show, For } from "solid-js";

import { TranscriptEntry, StreamingEntry, VUMeter, talkBtnLabel } from "./TranscriptWidgets";

export const CenterPanel = (props) => {
  const { config: c, on } = props;
  let fileInput;
  let transcriptRef;
  const [chatInput, setChatInput] = createSignal("");

  createEffect(() => {
    c.transcripts();
    c.llmResponse();
    if (transcriptRef) transcriptRef.scrollTop = transcriptRef.scrollHeight;
  });

  const handleFileSelect = (e) => {
    const file = e.target.files?.[0];
    if (file) on.startFile(file);
  };

  const handleChatSubmit = (e) => {
    e.preventDefault();
    const text = chatInput().trim();
    if (!text) return;
    setChatInput("");
    on.sendChat(text);
  };

  const enginesReady = () => !c.loadingLLM() && !c.loadingTTS() && c.llmModel() && c.ttsEngine();

  return (
    <div class="center-panel">
      <div class="transcript-box" ref={transcriptRef}>
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
          <p class="transcript-placeholder">Waiting for input...</p>
        </Show>
      </div>

      <form class="chat-input-bar" onSubmit={handleChatSubmit}>
        <textarea
          class="chat-input"
          placeholder="Type a message..."
          value={chatInput()}
          onInput={(e) => setChatInput(e.currentTarget.value)}
          onKeyDown={(e) => { if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); handleChatSubmit(e); } }}
          disabled={!c.llmModel()}
          rows={1}
        />
        <button type="submit" class="btn btn-sm" disabled={!chatInput().trim() || !c.llmModel()}>
          Send
        </button>
      </form>

      <Show when={c.isStreaming() || c.soundChecking()}>
        <VUMeter level={c.micLevel()} />
      </Show>

      <Show when={c.error()}>
        <div class="error-box">{c.error()}</div>
      </Show>

      <div class="controls">
        <div class="mode-toggle">
          <button
            class={`btn btn-sm ${c.mode() === "talk" ? "" : "btn-secondary"}`}
            onClick={() => on.setMode("talk")}
            disabled={c.isStreaming()}
          >
            Talk
          </button>
          <button
            class={`btn btn-sm ${c.mode() === "snippet" ? "" : "btn-secondary"}`}
            onClick={() => on.setMode("snippet")}
            disabled={c.isStreaming()}
          >
            Snippet
          </button>
        </div>

        <button
          onClick={on.toggleSoundCheck}
          class={`btn ${c.soundChecking() ? "btn-danger" : "btn-secondary"}`}
          disabled={c.isStreaming()}
        >
          {c.soundChecking() ? "Stop Check" : "Sound Check"}
        </button>

        <Show when={c.mode() === "talk"}>
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
              disabled={!enginesReady()}
            >
              {talkBtnLabel(c.loadingLLM(), c.loadingTTS())}
            </button>
            <button
              onClick={() => fileInput.click()}
              class="btn btn-secondary"
              disabled={!enginesReady()}
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
        </Show>

        <Show when={c.mode() === "snippet"}>
          <Show
            when={c.isStreaming()}
            fallback={
              <button
                onClick={on.startSnippet}
                class="btn"
                disabled={!enginesReady()}
              >
                Start Session
              </button>
            }
          >
            <button
              onClick={c.isRecording() ? on.pauseRecording : on.resumeRecording}
              class={`btn ${c.isRecording() ? "btn-danger" : ""}`}
            >
              {c.isRecording() ? "Pause" : "Record"}
            </button>
            <button
              onClick={on.processSnippet}
              class="btn btn-success"
              disabled={c.isRecording()}
            >
              Process
            </button>
            <button onClick={on.stop} class="btn btn-secondary">
              End Session
            </button>
          </Show>
        </Show>
      </div>
    </div>
  );
};
