import React from "react";
import ReactDOM from "react-dom/client";
import App from "./App";
import Picker from "./Picker";
import "./styles.css";

// The same bundle serves two Wails windows: the manager (default) and the
// hotkey popup, which main.go loads with ?mode=picker.
const isPicker = new URLSearchParams(window.location.search).get("mode") === "picker";
document.body.classList.toggle("is-picker", isPicker);

ReactDOM.createRoot(document.getElementById("root") as HTMLElement).render(
  <React.StrictMode>{isPicker ? <Picker /> : <App />}</React.StrictMode>,
);
