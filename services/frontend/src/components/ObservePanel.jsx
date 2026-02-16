import { createSignal, onMount, For, Show } from "solid-js";
import { fetchSessions, fetchSession, fetchRun } from "../api/traces";

const SPAN_COLORS = {
  asr: "bar-asr",
  llm: "bar-llm",
  tts: "bar-tts",
  rag: "bar-rag",
  scene_classify: "bar-scene_classify",
  emotion_classify: "bar-emotion_classify",
};

const fmtTime = (iso) => {
  const d = new Date(iso);
  return d.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", second: "2-digit" });
};

const fmtDate = (iso) => {
  const d = new Date(iso);
  return d.toLocaleDateString([], { month: "short", day: "numeric" }) + " " + fmtTime(iso);
};

const fmtMs = (ms) => {
  if (!ms) return "—";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${(ms / 1000).toFixed(1)}s`;
};

export function ObservePanel() {
  const [sessions, setSessions] = createSignal([]);
  const [selectedSession, setSelectedSession] = createSignal(null);
  const [runs, setRuns] = createSignal([]);
  const [expandedRuns, setExpandedRuns] = createSignal({});
  const [runSpans, setRunSpans] = createSignal({});
  const [selectedSpan, setSelectedSpan] = createSignal(null);

  onMount(async () => {
    try {
      const data = await fetchSessions(50, 0);
      setSessions(data.sessions || []);
    } catch (e) {
      console.error("fetch sessions", e);
    }
  });

  const selectSession = async (id) => {
    setSelectedSession(id);
    setExpandedRuns({});
    setRunSpans({});
    setSelectedSpan(null);
    try {
      const data = await fetchSession(id);
      setRuns(data.runs || []);
    } catch (e) {
      console.error("fetch session", e);
    }
  };

  const toggleRun = async (run) => {
    const cur = expandedRuns();
    const isOpen = cur[run.id];
    setExpandedRuns({ ...cur, [run.id]: !isOpen });

    if (isOpen || runSpans()[run.id]) return;

    try {
      const data = await fetchRun(run.session_id, run.id);
      setRunSpans((prev) => ({ ...prev, [run.id]: data.spans || [] }));
    } catch (e) {
      console.error("fetch run", e);
    }
  };

  const sessionDuration = (s) => {
    if (!s.ended_at) return "active";
    const ms = new Date(s.ended_at) - new Date(s.started_at);
    return fmtMs(ms);
  };

  return (
    <div class="observe-layout">
      {/* Left — Session list */}
      <div class="session-list">
        <h2>Sessions</h2>
        <Show when={sessions().length === 0}>
          <div class="empty-state">No sessions recorded</div>
        </Show>
        <For each={sessions()}>
          {(s) => (
            <div
              class={`session-item ${selectedSession() === s.id ? "active" : ""}`}
              onClick={() => selectSession(s.id)}
            >
              <div class="session-id">{s.id.slice(0, 8)}</div>
              <div class="session-meta">
                {fmtDate(s.started_at)} &middot; {s.run_count} runs &middot; {sessionDuration(s)}
              </div>
            </div>
          )}
        </For>
      </div>

      {/* Center — Runs + waterfall */}
      <div class="trace-center">
        <h2>Runs</h2>
        <Show when={!selectedSession()}>
          <div class="empty-state">Select a session</div>
        </Show>
        <Show when={selectedSession() && runs().length === 0}>
          <div class="empty-state">No runs in this session</div>
        </Show>
        <For each={runs()}>
          {(run) => (
            <div class="run-card">
              <div class="run-header" onClick={() => toggleRun(run)}>
                <div class={`run-status ${run.status}`} />
                <div class="run-transcript">{run.transcript || "(empty)"}</div>
                <div class="run-duration">{fmtMs(run.duration_ms)}</div>
              </div>
              <Show when={expandedRuns()[run.id] && runSpans()[run.id]}>
                <Waterfall
                  spans={runSpans()[run.id]}
                  runDuration={run.duration_ms}
                  runStart={run.started_at}
                  onSelect={setSelectedSpan}
                />
              </Show>
            </div>
          )}
        </For>
      </div>

      {/* Right — Span detail */}
      <div class="span-detail">
        <h2>Span Detail</h2>
        <Show when={!selectedSpan()}>
          <div class="empty-state">Click a span</div>
        </Show>
        <Show when={selectedSpan()}>
          <SpanDetail span={selectedSpan()} />
        </Show>
      </div>
    </div>
  );
}

function Waterfall(props) {
  const barStyle = (span) => {
    const runStart = new Date(props.runStart).getTime();
    const spanStart = new Date(span.started_at).getTime();
    const offset = spanStart - runStart;
    const dur = props.runDuration || 1;
    const left = Math.max(0, (offset / dur) * 100);
    const width = Math.max(1, (span.duration_ms / dur) * 100);
    return { left: `${left}%`, width: `${Math.min(width, 100 - left)}%` };
  };

  return (
    <div class="waterfall">
      <For each={props.spans}>
        {(span) => (
          <div class="waterfall-row" onClick={() => props.onSelect(span)}>
            <div class="waterfall-label">{span.name}</div>
            <div class="waterfall-track">
              <div
                class={`waterfall-bar ${SPAN_COLORS[span.name] || ""}`}
                style={barStyle(span)}
              >
                <span class="waterfall-bar-time">{fmtMs(span.duration_ms)}</span>
              </div>
            </div>
          </div>
        )}
      </For>
    </div>
  );
}

function SpanDetail(props) {
  const s = () => props.span;
  return (
    <>
      <div class="span-detail-row">
        <div class="span-detail-label">Name</div>
        <div class="span-detail-value">
          <span class={`run-status ${s().status}`} style={{ display: "inline-block", "margin-right": "6px", "vertical-align": "middle" }} />
          {s().name}
        </div>
      </div>
      <div class="span-detail-row">
        <div class="span-detail-label">Duration</div>
        <div class="span-detail-value">{fmtMs(s().duration_ms)}</div>
      </div>
      <div class="span-detail-row">
        <div class="span-detail-label">Started</div>
        <div class="span-detail-value">{fmtTime(s().started_at)}</div>
      </div>
      <Show when={s().input}>
        <div class="span-detail-row">
          <div class="span-detail-label">Input</div>
          <pre>{s().input}</pre>
        </div>
      </Show>
      <Show when={s().output}>
        <div class="span-detail-row">
          <div class="span-detail-label">Output</div>
          <pre>{s().output}</pre>
        </div>
      </Show>
      <Show when={s().error}>
        <div class="span-detail-row">
          <div class="span-detail-label">Error</div>
          <pre style={{ color: "#e74c3c" }}>{s().error}</pre>
        </div>
      </Show>
    </>
  );
}
