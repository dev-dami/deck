import { render } from "preact";
import { defaultStorage, deviceName, pairCodeFromHash, wsUrl } from "./auth";
import { Controller } from "./controller";
import { isRecord } from "./protocol";
import { BrowserPush } from "./push-browser";
import { App } from "./ui/App";
import { ControllerContext } from "./ui/hooks";
import { BrowserSocket } from "./websocket";

function vibrate(pattern: number | number[]): void {
  if (typeof navigator.vibrate === "function") navigator.vibrate(pattern);
}

function registerWorker(): void {
  // Service workers need a secure context; plain http on a LAN IP does not
  // qualify, and the app works fine without one.
  if (!window.isSecureContext || !("serviceWorker" in navigator)) return;
  navigator.serviceWorker.register("./sw.js").catch((err: unknown) => {
    console.warn("service worker registration failed", err);
  });
}

function boot(): void {
  const root = document.getElementById("app");
  if (root === null) throw new Error("#app is missing");

  // The pairing code is single-use and sensitive: lift it out of the URL
  // before anything else can see it (history, referrers, screenshots).
  const code = pairCodeFromHash(location.hash);
  if (location.hash.startsWith("#pair=")) {
    history.replaceState(null, "", `${location.pathname}${location.search}`);
  }

  const controller = new Controller({
    storage: defaultStorage(),
    fetchFn: (input, init) => fetch(input, init),
    socketFactory: (url) => new BrowserSocket(url),
    wsUrl: wsUrl(location),
    deviceName: deviceName(navigator.userAgent),
    vibrate,
    pushBrowser: new BrowserPush(),
  });

  document.addEventListener("visibilitychange", () => {
    if (document.visibilityState === "visible") controller.notifyVisible();
  });
  window.addEventListener("online", () => controller.notifyVisible());

  // The worker cannot reach Deck (the token lives in page storage), so when the
  // browser rotates the push subscription it asks the page to re-register it.
  if ("serviceWorker" in navigator) {
    navigator.serviceWorker.addEventListener("message", (ev: MessageEvent<unknown>) => {
      if (isRecord(ev.data) && ev.data["type"] === "push-subscription-changed") {
        void controller.push?.sync();
      }
    });
  }

  render(
    <ControllerContext.Provider value={controller}>
      <App />
    </ControllerContext.Provider>,
    root,
  );
  void controller.start(code);
  registerWorker();
}

boot();
