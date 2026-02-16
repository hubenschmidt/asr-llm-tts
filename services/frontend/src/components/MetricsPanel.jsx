import { Show } from "solid-js";

export const MetricsPanel = (props) => {
  const avg = () => computeAverages(props.history);

  return (
    <div style={containerStyle}>
      <h3 style={titleStyle}>Pipeline Metrics</h3>

      <Show when={props.metrics}>
        {(m) => (
          <div style={sectionStyle}>
            <h4 style={headingStyle}>Last Call</h4>
            <MetricRow label="ASR" ms={m().asr_ms} />
            <MetricRow label="LLM" ms={m().llm_ms} />
            <MetricRow label="TTS" ms={m().tts_ms} />
            <MetricRow label="E2E" ms={m().total_ms} highlight />

            <Show when={m().asr_ms > 0}>
              <h4 style={{ ...headingStyle, "margin-top": "10px" }}>ASR Detail</h4>
              <DetailRow label="No-speech prob" value={(m().no_speech_prob * 100).toFixed(1) + "%"} color={m().no_speech_prob > 0.4 ? "#f39c12" : "#00b8d4"} />
              <Show when={m().noise_suppressed}>
                <DetailRow label="Noise suppression" value="on" color="#2ecc71" />
              </Show>
              <Show when={m().wer !== null && m().wer >= 0}>
                <DetailRow label="WER" value={(m().wer * 100).toFixed(1) + "%"} color={m().wer > 0.3 ? "#e74c3c" : m().wer > 0.1 ? "#f39c12" : "#2ecc71"} />
              </Show>
            </Show>
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
        <p style={{ color: "#2a3545", "font-style": "italic", "font-size": "11px" }}>No metrics yet</p>
      </Show>
    </div>
  );
};

const metricColor = (ms) => {
  if (ms > 1000) return "#e74c3c";
  if (ms > 500) return "#f39c12";
  return "#00b8d4";
};

const MetricRow = (props) => {
  const color = () => metricColor(props.ms);
  return (
    <div
      style={{
        display: "flex",
        "justify-content": "space-between",
        padding: "3px 0",
        "font-weight": props.highlight ? "bold" : "normal",
        "font-size": "12px",
      }}
    >
      <span style={{ color: "#4a6880" }}>{props.label}</span>
      <span style={{ color: color() }}>{props.ms.toFixed(0)}ms</span>
    </div>
  );
};

const DetailRow = (props) => (
  <div style={{ display: "flex", "justify-content": "space-between", padding: "3px 0", "font-size": "11px" }}>
    <span style={{ color: "#4a6880" }}>{props.label}</span>
    <span style={{ color: props.color }}>{props.value}</span>
  </div>
);

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
  background: "#0f1420",
  border: "1px solid #1a2535",
  "border-radius": "6px",
  padding: "12px",
  color: "#c0c8d8",
};

const titleStyle = {
  margin: "0 0 12px",
  "font-size": "10px",
  color: "#4a6880",
  "text-transform": "uppercase",
  "letter-spacing": "1.5px",
  "font-weight": "400",
};

const sectionStyle = { "margin-bottom": "14px" };

const headingStyle = {
  margin: "0 0 6px",
  "font-size": "11px",
  color: "#4a9ec8",
  "font-weight": "400",
};
