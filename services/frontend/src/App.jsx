import { CallPanel } from "./components/CallPanel";

const App = () => {
  return (
    <div
      style={{
        "min-height": "100vh",
        background: "#0f0f1a",
        color: "#eee",
        "font-family": "system-ui, -apple-system, sans-serif",
      }}
    >
      <CallPanel />
    </div>
  );
};

export default App;
