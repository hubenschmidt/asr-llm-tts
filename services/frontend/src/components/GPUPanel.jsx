import { createSignal, onCleanup, Show, For } from "solid-js";

import "../style/gpu-panel.css";

const [gpu, setGpu] = createSignal(null);
const [sseStatus, setSseStatus] = createSignal("connecting");

// Connect SSE directly to gateway (Vite proxy buffers SSE)
const gwOrigin = `http://${window.location.hostname}:8000`;

export const GPUPanel = (props) => {
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
    <div class="gpu-container">
      <div class="gpu-header">
        <span class="gpu-dot" style={{ background: sseColor() }} />
        <h4 class="gpu-heading">GPU VRAM</h4>
        <Show when={gpu()?.processes.length > 0 && props.onUnloadAll}>
          <button class="gpu-unload-btn" onClick={props.onUnloadAll}>
            Unload All
          </button>
        </Show>
      </div>
      <Show when={gpu()} fallback={<p class="gpu-no-data">No GPU data</p>}>
        <div class="gpu-bar-track">
          <div class="gpu-bar-fill" style={{ width: `${pct()}%`, background: barColor() }} />
        </div>
        <div class="gpu-usage-text">
          {(gpu().vram_used_mb / 1024).toFixed(1)} / {(gpu().vram_total_mb / 1024).toFixed(1)} GB
        </div>
        <Show
          when={gpu().processes.length > 0}
          fallback={<p class="gpu-no-processes">No GPU processes</p>}
        >
          <For each={gpu().processes}>
            {(p) => (
              <div class="gpu-process-row">
                <span class="gpu-process-name">{p.name}</span>
                <span class="gpu-process-vram">
                  {p.vram_mb >= 1024 ? `${(p.vram_mb / 1024).toFixed(1)} GB` : `${p.vram_mb} MB`}
                </span>
              </div>
            )}
          </For>
        </Show>
      </Show>
    </div>
  );
};
