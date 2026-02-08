import { createSignal, onCleanup, Show, For } from "solid-js";

interface GPUProcess {
  pid: number;
  name: string;
  vram_mb: number;
}

interface GPUInfo {
  vram_total_mb: number;
  vram_used_mb: number;
  processes: GPUProcess[];
}

const [gpu, setGpu] = createSignal<GPUInfo | null>(null);
const [sseStatus, setSseStatus] = createSignal<"connecting" | "open" | "closed">("connecting");

// Connect SSE directly to gateway (Vite proxy buffers SSE)
const gwOrigin = `http://${window.location.hostname}:8000`;

export function GPUPanel() {
  const es = new EventSource(`${gwOrigin}/api/gpu/stream`);
  es.onopen = () => setSseStatus("open");
  es.onmessage = (e) => {
    setSseStatus("open");
    setGpu(JSON.parse(e.data));
  };
  es.onerror = () => {
    setSseStatus(es.readyState === EventSource.CLOSED ? "closed" : "connecting");
  };
  onCleanup(() => es.close());

  const pct = () => {
    const g = gpu();
    if (!g || !g.vram_total_mb) return 0;
    return (g.vram_used_mb / g.vram_total_mb) * 100;
  };

  const barColor = () => {
    const p = pct();
    if (p >= 80) return "#e74c3c";
    if (p >= 50) return "#f1c40f";
    return "#2ecc71";
  };

  const sseColor = () => {
    const s = sseStatus();
    if (s === "open") return "#2ecc71";
    if (s === "connecting") return "#f1c40f";
    return "#e74c3c";
  };

  return (
    <div style={containerStyle}>
      <div style={{ display: "flex", "align-items": "center", gap: "6px", "margin-bottom": "8px" }}>
        <span style={{ ...dotStyle, background: sseColor() }} />
        <h4 style={{ ...headingStyle, margin: "0" }}>GPU VRAM</h4>
      </div>
      <Show when={gpu()} fallback={<p style={{ color: "#555", "font-size": "12px" }}>No GPU data</p>}>
        <div style={barTrackStyle}>
          <div style={{ ...barFillStyle, width: `${pct()}%`, background: barColor() }} />
        </div>
        <div style={usageTextStyle}>
          {(gpu()!.vram_used_mb / 1024).toFixed(1)} / {(gpu()!.vram_total_mb / 1024).toFixed(1)} GB
        </div>
        <Show
          when={gpu()!.processes.length > 0}
          fallback={<p style={{ color: "#555", "font-size": "12px", margin: "4px 0 0" }}>No GPU processes</p>}
        >
          <For each={gpu()!.processes}>
            {(p) => (
              <div style={processRowStyle}>
                <span style={{ color: "#ccc" }}>{p.name}</span>
                <span style={{ color: "#888", "font-family": "monospace" }}>
                  {p.vram_mb >= 1024 ? `${(p.vram_mb / 1024).toFixed(1)} GB` : `${p.vram_mb} MB`}
                </span>
              </div>
            )}
          </For>
        </Show>
      </Show>
    </div>
  );
}

const containerStyle = {
  background: "#1a1a2e",
  "border-radius": "8px",
  padding: "12px 16px",
  "margin-bottom": "12px",
  color: "#eee",
};

const headingStyle = {
  "font-size": "12px",
  color: "#aaa",
  "text-transform": "uppercase",
  "letter-spacing": "1px",
};

const dotStyle = {
  display: "inline-block",
  width: "8px",
  height: "8px",
  "border-radius": "50%",
  "flex-shrink": "0",
};

const barTrackStyle = {
  height: "8px",
  background: "#2a2a3e",
  "border-radius": "4px",
  overflow: "hidden",
};

const barFillStyle = {
  height: "100%",
  "border-radius": "4px",
  transition: "width 300ms ease",
};

const usageTextStyle = {
  "font-size": "12px",
  color: "#888",
  "text-align": "right",
  "margin-top": "4px",
  "font-family": "monospace",
};

const processRowStyle = {
  display: "flex",
  "justify-content": "space-between",
  "font-size": "12px",
  padding: "2px 0",
  "margin-top": "2px",
};
