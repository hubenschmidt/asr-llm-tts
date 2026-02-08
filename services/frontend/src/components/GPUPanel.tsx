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

export function GPUPanel() {
  const [gpu, setGpu] = createSignal<GPUInfo | null>(null);

  const poll = () => {
    fetch("/api/gpu")
      .then((r) => r.json())
      .then(setGpu)
      .catch(() => setGpu(null));
  };

  poll();
  const timer = setInterval(poll, 2000);
  onCleanup(() => clearInterval(timer));

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

  return (
    <div style={containerStyle}>
      <h4 style={headingStyle}>GPU VRAM</h4>
      <Show when={gpu()} fallback={<p style={{ color: "#555", "font-size": "12px" }}>No GPU data</p>}>
        {(g) => (
          <>
            <div style={barTrackStyle}>
              <div style={{ ...barFillStyle, width: `${pct()}%`, background: barColor() }} />
            </div>
            <div style={usageTextStyle}>
              {(g().vram_used_mb / 1024).toFixed(1)} / {(g().vram_total_mb / 1024).toFixed(1)} GB
            </div>
            <Show
              when={g().processes.length > 0}
              fallback={<p style={{ color: "#555", "font-size": "12px", margin: "4px 0 0" }}>No GPU processes</p>}
            >
              <For each={g().processes}>
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
          </>
        )}
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
  margin: "0 0 8px",
  "font-size": "12px",
  color: "#aaa",
  "text-transform": "uppercase",
  "letter-spacing": "1px",
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
