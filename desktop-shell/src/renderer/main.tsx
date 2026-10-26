import { IconContext } from "@phosphor-icons/react";
import { createRoot } from "react-dom/client";
import type { DeckBridge } from "../shared/bridge";
import { startAppIconSync } from "./appIcon/sync";
import "./styles.css";
import { App } from "./ui/App";

// The preload script puts the bridge on window; without it (a plain
// browser) there is nothing to talk to.
const bridge = (window as Window & { deck?: DeckBridge }).deck;
// Errors the page does not catch go to the app's log file.
if (bridge !== undefined) {
  const report = (what: unknown) =>
    void bridge.logFromRenderer("error", what instanceof Error ? (what.stack ?? what.message) : String(what)).catch(() => {});
  window.addEventListener("error", (e) => report(e.error ?? `${e.message} (${e.filename}:${e.lineno})`));
  window.addEventListener("unhandledrejection", (e) => report(e.reason));
  // The app's icon follows the theme, or the one picked in Settings.
  startAppIconSync(bridge);
}
const root = document.getElementById("root");
if (root !== null) {
  createRoot(root).render(
    // Phosphor's icons (ui/icons.tsx): one weight for the chrome.
    <IconContext.Provider value={{ weight: "regular", size: "1em" }}>
      {bridge === undefined ? <p className="loading grid h-full place-items-center text-sm">Open this page in the Deck desktop app.</p> : <App bridge={bridge} />}
    </IconContext.Provider>,
  );
}
