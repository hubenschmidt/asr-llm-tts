import { createSignal, createEffect, onCleanup, Show } from "solid-js";

import { marked } from "./TranscriptWidgets";

const EXPLAIN_PROMPT = `You are a code explanation assistant. Given an algorithm or code solution, provide a clear, detailed explanation covering:
1. What the algorithm does and why this approach works
2. Step-by-step walkthrough of the logic
3. Time and space complexity analysis
4. Key edge cases handled
Be thorough but concise. Use markdown formatting.`;

export const ExplainPanel = (props) => {
  const [explanation, setExplanation] = createSignal("");
  const [loading, setLoading] = createSignal(false);
  let ws = null;

  const cleanup = () => {
    ws?.close();
    ws = null;
  };

  const explain = (text) => {
    cleanup();
    setExplanation("");
    setLoading(true);

    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const socket = new WebSocket(`${protocol}//${window.location.host}/ws/call`);
    socket.binaryType = "arraybuffer";
    ws = socket;

    socket.onopen = () => {
      socket.send(JSON.stringify({
        codec: "pcm",
        sample_rate: 48000,
        tts_engine: "",
        asr_engine: "",
        system_prompt: EXPLAIN_PROMPT,
        llm_model: props.llmModel(),
        llm_engine: props.llmEngine(),
        mode: "text",
      }));
      socket.send(JSON.stringify({ action: "chat", message: text }));
    };

    socket.onmessage = (ev) => {
      if (ev.data instanceof ArrayBuffer) return;
      const event = JSON.parse(ev.data);
      if (event.type === "llm_token") { setExplanation((prev) => prev + (event.token ?? "")); return; }
      if (event.type === "llm_done") { setLoading(false); return; }
      if (event.type === "error") { setLoading(false); setExplanation((prev) => prev + "\n\nError: " + event.text); }
    };

    socket.onerror = () => setLoading(false);
  };

  createEffect(() => {
    const text = props.text;
    if (text) explain(text);
  });

  onCleanup(cleanup);

  return (
    <div class="explain-panel">
      <div class="explain-header">
        <span class="explain-title">Explain This</span>
        <button class="explain-close" onClick={props.onClose}>&times;</button>
      </div>
      <div class="explain-body">
        <Show when={explanation()}>
          <div class="agent-markdown" innerHTML={marked.parse(explanation())} />
        </Show>
        <Show when={loading() && !explanation()}>
          <p class="transcript-placeholder">Analyzing...</p>
        </Show>
        <Show when={loading() && explanation()}>
          <span class="spinner" style={{ "margin-top": "8px" }} />
        </Show>
      </div>
    </div>
  );
};
