import type { PipelineMetrics } from "../hooks/useAudioStream";

interface MetricsPanelProps {
  metrics: PipelineMetrics | null;
  history: PipelineMetrics[];
}

export function MetricsPanel({ metrics, history }: MetricsPanelProps) {
  const avg = computeAverages(history);

  return (
    <div style={containerStyle}>
      <h3 style={{ margin: "0 0 12px" }}>Pipeline Metrics</h3>

      {metrics && (
        <div style={sectionStyle}>
          <h4 style={headingStyle}>Last Call</h4>
          <MetricRow label="ASR" ms={metrics.asr_ms} />
          <MetricRow label="LLM" ms={metrics.llm_ms} />
          <MetricRow label="TTS" ms={metrics.tts_ms} />
          <MetricRow label="E2E" ms={metrics.total_ms} highlight />
        </div>
      )}

      {history.length > 1 && (
        <div style={sectionStyle}>
          <h4 style={headingStyle}>Average ({history.length} calls)</h4>
          <MetricRow label="ASR" ms={avg.asr_ms} />
          <MetricRow label="LLM" ms={avg.llm_ms} />
          <MetricRow label="TTS" ms={avg.tts_ms} />
          <MetricRow label="E2E" ms={avg.total_ms} highlight />
        </div>
      )}

      {!metrics && <p style={{ color: "#888" }}>No metrics yet</p>}
    </div>
  );
}

function MetricRow({
  label,
  ms,
  highlight,
}: {
  label: string;
  ms: number;
  highlight?: boolean;
}) {
  const color = ms > 1000 ? "#e74c3c" : ms > 500 ? "#f39c12" : "#2ecc71";
  return (
    <div
      style={{
        display: "flex",
        justifyContent: "space-between",
        padding: "4px 0",
        fontWeight: highlight ? "bold" : "normal",
      }}
    >
      <span>{label}</span>
      <span style={{ color, fontFamily: "monospace" }}>{ms.toFixed(0)}ms</span>
    </div>
  );
}

function computeAverages(history: PipelineMetrics[]): PipelineMetrics {
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
}

const containerStyle: React.CSSProperties = {
  background: "#1a1a2e",
  borderRadius: 8,
  padding: 16,
  color: "#eee",
  minWidth: 250,
};

const sectionStyle: React.CSSProperties = { marginBottom: 16 };

const headingStyle: React.CSSProperties = {
  margin: "0 0 8px",
  fontSize: 14,
  color: "#aaa",
};
