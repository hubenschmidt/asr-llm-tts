import { createSignal, Show } from "solid-js";
import { Marked } from "marked";
import { markedHighlight } from "marked-highlight";
import hljs from "highlight.js/lib/core";
import javascript from "highlight.js/lib/languages/javascript";
import typescript from "highlight.js/lib/languages/typescript";
import python from "highlight.js/lib/languages/python";
import go from "highlight.js/lib/languages/go";
import bash from "highlight.js/lib/languages/bash";
import json from "highlight.js/lib/languages/json";
import css from "highlight.js/lib/languages/css";
import xml from "highlight.js/lib/languages/xml";
import sql from "highlight.js/lib/languages/sql";
import rust from "highlight.js/lib/languages/rust";

hljs.registerLanguage("javascript", javascript);
hljs.registerLanguage("typescript", typescript);
hljs.registerLanguage("python", python);
hljs.registerLanguage("go", go);
hljs.registerLanguage("bash", bash);
hljs.registerLanguage("json", json);
hljs.registerLanguage("css", css);
hljs.registerLanguage("xml", xml);
hljs.registerLanguage("sql", sql);
hljs.registerLanguage("rust", rust);

export const marked = new Marked(
  markedHighlight({
    emptyLangClass: "hljs",
    langPrefix: "hljs language-",
    highlight(code, lang) {
      if (lang && hljs.getLanguage(lang)) return hljs.highlight(code, { language: lang }).value;
      return hljs.highlightAuto(code).value;
    },
  }),
  { breaks: true, gfm: true }
);

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
          <Show when={props.onExplain}>
            <button class="explain-icon" onClick={() => props.onExplain(props.text)} title="Explain this">?</button>
          </Show>
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
