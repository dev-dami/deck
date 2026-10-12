import { render } from "preact";
import { defaultStorage, deviceName, isPairHash, pairOfferFromHash, wsUrl } from "./auth";
import { Controller } from "./controller";
import { documentTarget } from "./theme";
import { App } from "./ui/App";
import { ControllerContext } from "./ui/hooks";
import { BrowserSocket } from "./websocket";

function vibrate(pattern: number | number[]): void {
  if (typeof navigator.vibrate === "function") navigator.vibrate(pattern);
}

function registerWorker(): void {
  // Service workers need a secure context; plain http on loopback qualifies,
  // and the app works fine without one.
  if (!window.isSecureContext || !("serviceWorker" in navigator)) return;
  navigator.serviceWorker.register("./sw.js").catch((err: unknown) => {
    console.warn("service worker registration failed", err);
  });
}

function boot(): void {
  // The pairing code is single-use and sensitive: lift it out of the URL
  // before anything else runs (history, referrers, screenshots) (§3.5).
  const offer = pairOfferFromHash(location.hash);
  if (isPairHash(location.hash)) {
    history.replaceState(null, "", `${location.pathname}${location.search}`);
  }

  const root = document.getElementById("app");
  if (root === null) throw new Error("#app is missing");

  const controller = new Controller({
    storage: defaultStorage(),
    fetchFn: (input, init) => fetch(input, init),
    socketFactory: (url) => new BrowserSocket(url),
    wsUrl: wsUrl(location),
    deviceName: deviceName(navigator.userAgent),
    vibrate,
    theme: documentTarget(document),
  });

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") controller.notifyVisible();
  });
  window.addEventListener("online", () => controller.notifyVisible());

  render(
    <ControllerContext.Provider value={controller}>
      <App />
    </ControllerContext.Provider>,
    root,
  );
  controller.start(offer);
  registerWorker();
}

boot();
