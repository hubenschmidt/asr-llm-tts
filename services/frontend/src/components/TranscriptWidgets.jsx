import { createSignal, Show } from "solid-js";
import { marked } from "marked";

marked.setOptions({ breaks: true, gfm: true });

export const StatusDot = (props) => (
  <span class="status-dot" style={{ background: props.color }} />
);

export const Tooltip = (props) => (
  <span class="tooltip-wrap">
    <span class="help-icon">?</span>
    <span class="tooltip">{props.text}</span>
  </span>
);

export const TranscriptEntry = (props) => {
  const [showThinking, setShowThinking] = createSignal(false);
  const isAgent = () => props.role === "agent";
  return (
    <div class={`transcript-line ${isAgent() ? "transcript-agent" : "transcript-user"}`}>
      <Show when={isAgent()} fallback={<p><strong>You: </strong>{props.text}</p>}>
        <div class="agent-markdown">
          <strong>Agent: </strong>
          <span innerHTML={marked.parse(props.text || "")} />
        </div>
      </Show>
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

export const StreamingEntry = (props) => {
  const [showThinking, setShowThinking] = createSignal(false);
  return (
    <div class="transcript-line transcript-agent">
      <Show when={props.text}>
        <div class="transcript-streaming agent-markdown">
          <strong>Agent: </strong>
          <span innerHTML={marked.parse(props.text || "")} />
        </div>
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

export const talkBtnLabel = (loadingLLM, loadingTTS) => {
  if (loadingLLM) return "Loading model...";
  if (loadingTTS) return "Checking TTS...";
  return "Talk";
};

const vuColor = (pct) => {
  if (pct >= 70) return "#e74c3c";
  if (pct >= 30) return "#f1c40f";
  return "#2ecc71";
};

export const VUMeter = (props) => {
  const pct = () => Math.min(100, props.level * 500);
  const color = () => vuColor(pct());
  return (
    <div class="vu-track">
      <div class="vu-bar" style={{ width: `${pct()}%`, background: color() }} />
    </div>
  );
};
