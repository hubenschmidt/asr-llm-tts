import { Route, Router, A, useLocation } from "@solidjs/router";
import { CallPanel } from "./components/CallPanel";
import { ObservePanel } from "./components/ObservePanel";
import "./style/observe.css";

function NavToggle() {
  const loc = useLocation();
  const isObserve = () => loc.pathname === "/observe";
  return (
    <A class="nav-toggle" href={isObserve() ? "/" : "/observe"}>
      {isObserve() ? "Call" : "Observe"}
    </A>
  );
}

function Layout(props) {
  return (
    <div style={{ "min-height": "100vh", background: "#0b0e17", color: "#c0c8d8" }}>
      <NavToggle />
      {props.children}
    </div>
  );
}

const App = () => (
  <Router root={Layout}>
    <Route path="/" component={CallPanel} />
    <Route path="/observe" component={ObservePanel} />
  </Router>
);

export default App;
