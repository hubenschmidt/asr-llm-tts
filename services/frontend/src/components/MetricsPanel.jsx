import { Show } from "solid-js";

export const MetricsPanel = (props) => {
  const avg = () => computeAverages(props.history);

  return (
    <div style={containerStyle}>
      <h3 style={{ margin: "0 0 12px" }}>Pipeline Metrics</h3>

      <Show when={props.metrics}>
        {(m) => (
          <div style={sectionStyle}>
            <h4 style={headingStyle}>Last Call</h4>
            <MetricRow label="ASR" ms={m().asr_ms} />
            <MetricRow label="LLM" ms={m().llm_ms} />
            <MetricRow label="TTS" ms={m().tts_ms} />
            <MetricRow label="E2E" ms={m().total_ms} highlight />
          </div>
        )}
      </Show>

      <Show when={props.history.length > 1}>
        <div style={sectionStyle}>
          <h4 style={headingStyle}>Average ({props.history.length} calls)</h4>
          <MetricRow label="ASR" ms={avg().asr_ms} />
          <MetricRow label="LLM" ms={avg().llm_ms} />
          <MetricRow label="TTS" ms={avg().tts_ms} />
          <MetricRow label="E2E" ms={avg().total_ms} highlight />
        </div>
      </Show>

      <Show when={!props.metrics}>
        <p style={{ color: "#888" }}>No metrics yet</p>
      </Show>
    </div>
  );
};

const MetricRow = (props) => {
  const color = () => props.ms > 1000 ? "#e74c3c" : props.ms > 500 ? "#f39c12" : "#2ecc71";
  return (
    <div
      style={{
        display: "flex",
        "justify-content": "space-between",
        padding: "4px 0",
        "font-weight": props.highlight ? "bold" : "normal",
      }}
    >
      <span>{props.label}</span>
      <span style={{ color: color(), "font-family": "monospace" }}>{props.ms.toFixed(0)}ms</span>
    </div>
  );
};

const computeAverages = (history) => {
  if (history.length === 0) {
    return { asr_ms: 0, llm_ms: 0, tts_ms: 0, total_ms: 0 };
  }
  const n = history.length;
  return {
    asr_ms: history.reduce((s, m) => s + m.asr_ms, 0) / n,
    llm_ms: history.reduce((s, m) => s + m.llm_ms, 0) / n,
    tts_ms: history.reduce((s, m) => s + m.tts_ms, 0) / n,
    total_ms: history.reduce((s, m) => s + m.total_ms, 0) / n,
  };
};

const containerStyle = {
  background: "#1a1a2e",
  "border-radius": "8px",
  padding: "16px",
  color: "#eee",
  width: "220px",
  "flex-shrink": "0",
};

const sectionStyle = { "margin-bottom": "16px" };

const headingStyle = {
  margin: "0 0 8px",
  "font-size": "14px",
  color: "#aaa",
};
