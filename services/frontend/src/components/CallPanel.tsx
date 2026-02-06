import { useCallback, useRef, useState } from "react";

import type { PipelineMetrics } from "../hooks/useAudioStream";
import { useAudioStream } from "../hooks/useAudioStream";
import { MetricsPanel } from "./MetricsPanel";

export function CallPanel() {
  const [ttsEngine, setTtsEngine] = useState("fast");
  const [transcripts, setTranscripts] = useState<{ role: string; text: string }[]>([]);
  const [llmResponse, setLlmResponse] = useState("");
  const [latestMetrics, setLatestMetrics] = useState<PipelineMetrics | null>(
    null,
  );
  const [metricsHistory, setMetricsHistory] = useState<PipelineMetrics[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [micLevel, setMicLevel] = useState(0);
  const audioCtxRef = useRef<AudioContext | null>(null);
  const playAtRef = useRef(0);
  const fileInputRef = useRef<HTMLInputElement>(null);

  const playAudio = useCallback((data: ArrayBuffer) => {
    if (!audioCtxRef.current) {
      audioCtxRef.current = new AudioContext();
    }
    const ctx = audioCtxRef.current;
    ctx.decodeAudioData(data.slice(0)).then((buf) => {
      const source = ctx.createBufferSource();
      source.buffer = buf;
      source.connect(ctx.destination);
      const startAt = Math.max(ctx.currentTime, playAtRef.current);
      source.start(startAt);
      playAtRef.current = startAt + buf.duration;
    });
  }, []);

  const { isStreaming, startMic, startFile, stop } = useAudioStream({
    ttsEngine,
    onTranscript: (text) => setTranscripts((prev) => [...prev, { role: "user", text }]),
    onLLMToken: (token) => setLlmResponse((prev) => prev + token),
    onLLMDone: (text) => {
      setTranscripts((prev) => [...prev, { role: "agent", text }]);
      setLlmResponse("");
    },
    onAudio: playAudio,
    onMetrics: (m) => {
      setLatestMetrics(m);
      setMetricsHistory((prev) => [...prev, m]);
    },
    onError: setError,
    onLevel: setMicLevel,
  });

  const handleFileSelect = useCallback(
    (e: React.ChangeEvent<HTMLInputElement>) => {
      const file = e.target.files?.[0];
      if (file) startFile(file);
    },
    [startFile],
  );

  return (
    <div style={layoutStyle}>
      <div style={mainStyle}>
        <h2 style={{ margin: "0 0 16px" }}>ASR → LLM → TTS Pipeline</h2>

        <div style={controlsStyle}>
          <select
            value={ttsEngine}
            onChange={(e) => setTtsEngine(e.target.value)}
            style={selectStyle}
            disabled={isStreaming}
          >
            <option value="fast">Fast (Low)</option>
            <option value="quality">Quality (Medium)</option>
          </select>

          {!isStreaming ? (
            <>
              <button onClick={startMic} style={btnStyle}>
                Start Mic
              </button>
              <button
                onClick={() => fileInputRef.current?.click()}
                style={btnSecondaryStyle}
              >
                Upload Audio
              </button>
              <input
                ref={fileInputRef}
                type="file"
                accept="audio/*"
                onChange={handleFileSelect}
                style={{ display: "none" }}
              />
            </>
          ) : (
            <button onClick={stop} style={btnDangerStyle}>
              Stop
            </button>
          )}
        </div>

        {isStreaming && <VUMeter level={micLevel} />}

        {error && <div style={errorStyle}>{error}</div>}

        <div style={transcriptBoxStyle}>
          <h3 style={{ margin: "0 0 8px", fontSize: 14, color: "#aaa" }}>
            Transcript
          </h3>
          {transcripts.map((t, i) => (
            <p key={i} style={{ margin: "4px 0", color: t.role === "agent" ? "#6cb4ee" : "#eee" }}>
              <strong>{t.role === "agent" ? "Agent: " : "You: "}</strong>
              {t.text}
            </p>
          ))}
          {llmResponse && (
            <p style={{ color: "#6cb4ee", fontStyle: "italic", margin: "4px 0" }}>
              <strong>Agent: </strong>{llmResponse}
            </p>
          )}
          {transcripts.length === 0 && !llmResponse && (
            <p style={{ color: "#555" }}>
              Waiting for audio input...
            </p>
          )}
        </div>
      </div>

      <MetricsPanel metrics={latestMetrics} history={metricsHistory} />
    </div>
  );
}

function VUMeter({ level }: { level: number }) {
  const pct = Math.min(100, level * 500);
  const color = pct < 30 ? "#2ecc71" : pct < 70 ? "#f1c40f" : "#e74c3c";
  return (
    <div style={vuTrackStyle}>
      <div style={{ ...vuBarStyle, width: `${pct}%`, background: color }} />
    </div>
  );
}

const vuTrackStyle: React.CSSProperties = {
  height: 8,
  background: "#1a1a2e",
  borderRadius: 4,
  marginBottom: 12,
  overflow: "hidden",
};

const vuBarStyle: React.CSSProperties = {
  height: "100%",
  borderRadius: 4,
  transition: "width 50ms linear",
};

const layoutStyle: React.CSSProperties = {
  display: "flex",
  gap: 24,
  maxWidth: 900,
  margin: "40px auto",
  padding: "0 16px",
};

const mainStyle: React.CSSProperties = { flex: 1 };

const controlsStyle: React.CSSProperties = {
  display: "flex",
  gap: 8,
  marginBottom: 16,
  alignItems: "center",
};

const selectStyle: React.CSSProperties = {
  padding: "8px 12px",
  borderRadius: 4,
  border: "1px solid #444",
  background: "#2a2a3e",
  color: "#eee",
};

const btnStyle: React.CSSProperties = {
  padding: "8px 20px",
  borderRadius: 4,
  border: "none",
  background: "#2ecc71",
  color: "#fff",
  cursor: "pointer",
  fontWeight: "bold",
};

const btnSecondaryStyle: React.CSSProperties = {
  ...btnStyle,
  background: "#3498db",
};

const btnDangerStyle: React.CSSProperties = {
  ...btnStyle,
  background: "#e74c3c",
};

const errorStyle: React.CSSProperties = {
  background: "#e74c3c22",
  border: "1px solid #e74c3c",
  padding: "8px 12px",
  borderRadius: 4,
  marginBottom: 12,
  color: "#e74c3c",
};

const transcriptBoxStyle: React.CSSProperties = {
  background: "#1a1a2e",
  borderRadius: 8,
  padding: 16,
  minHeight: 200,
  color: "#eee",
};
