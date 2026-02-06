import { CallPanel } from "./components/CallPanel";

export default function App() {
  return (
    <div
      style={{
        minHeight: "100vh",
        background: "#0f0f1a",
        color: "#eee",
        fontFamily: "system-ui, -apple-system, sans-serif",
      }}
    >
      <CallPanel />
    </div>
  );
}
