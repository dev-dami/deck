import { createRoot } from "react-dom/client";
import type { DeckBridge } from "../shared/bridge";
import "./styles.css";
import { App } from "./ui/App";

// The preload script puts the bridge on window; without it (a plain
// browser) there is nothing to talk to.
const bridge = (window as Window & { deck?: DeckBridge }).deck;
const root = document.getElementById("root");
if (root !== null) {
  createRoot(root).render(
    bridge === undefined ? <p className="loading grid h-full place-items-center text-sm">Open this page in the Deck desktop app.</p> : <App bridge={bridge} />,
  );
}
