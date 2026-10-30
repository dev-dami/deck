import type { Session, WebContents, WebContentsView, WebPreferences } from "electron";
import { createServer, type Server } from "node:net";
import type { ArtifactBounds, ArtifactShown } from "../shared/bridge";
import { ArtifactHosts, type Served, urlOf } from "./artifactProtocol";
import { previewPrefs } from "./preview";

// The artifact pane: one view that main lays over the window. Its page is
// generated code, so it gets less than the preview or the browser: a session
// of its own in memory, no preload, no IPC and no network. Everything it
// shows comes from ArtifactHosts over the deck-artifact scheme.

// No "persist:" prefix: nothing an artifact stores outlives the session.
export const ARTIFACT_PARTITION = "artifact";

// Hold an actual listening socket for the lifetime of the process. A fixed
// "unused" port could belong to a local proxy or another service. This
// endpoint never parses or forwards anything; every connection is destroyed.
// Keeping the server in this cached result also keeps its port reserved.
let refusal: Promise<{ server: Server; url: string }> | null = null;

function refusingProxy(): Promise<{ server: Server; url: string }> {
  if (refusal !== null) return refusal;
  const server = createServer((socket) => {
    socket.on("error", () => {});
    socket.destroy();
  });
  server.unref();
  refusal = new Promise<{ server: Server; url: string }>((resolve, reject) => {
    server.on("error", reject);
    server.listen({ host: "127.0.0.1", port: 0, exclusive: true }, () => {
      const address = server.address();
      if (address === null || typeof address === "string") {
        server.close();
        reject(new Error("Could not reserve the artifact network boundary."));
        return;
      }
      resolve({ server, url: `http://127.0.0.1:${address.port}` });
    });
  }).catch((error: unknown) => {
    refusal = null;
    throw error;
  });
  return refusal;
}

// Applies to the artifact session: only the artifact scheme gets through,
// and every permission, device and download is refused.
export async function lockArtifactSession(ses: Session, scheme: string): Promise<void> {
  ses.setPermissionRequestHandler((_wc, _perm, cb) => cb(false));
  ses.setPermissionCheckHandler(() => false);
  ses.setDevicePermissionHandler(() => false);
  ses.on("will-download", (e) => e.preventDefault());
  ses.webRequest.onBeforeRequest((details, cb) => {
    cb({ cancel: !details.url.startsWith(`${scheme}://`) });
  });
  // WebRTC opens sockets that webRequest never sees, loopback ones too. Its
  // TCP goes through our refusing endpoint, with no direct fallback and no
  // loopback exemption. The generated page cannot reach another host service.
  const proxy = await refusingProxy();
  await ses.setProxy({ mode: "fixed_servers", proxyRules: proxy.url, proxyBypassRules: "<-loopback>" });
}

// The page stays where it was opened: no navigation, redirect, window or
// frame, and nothing is handed to the system browser. WebRTC could still
// open UDP sockets that no request filter sees, so the page may use only
// proxied connections, and there is none.
export function denyEscapes(wc: WebContents): void {
  wc.on("will-navigate", (e) => e.preventDefault());
  wc.on("will-redirect", (e) => e.preventDefault());
  wc.on("will-frame-navigate", (e) => e.preventDefault());
  wc.setWindowOpenHandler(() => ({ action: "deny" }));
  wc.setWebRTCIPHandlingPolicy("disable_non_proxied_udp");
}

// Where the page opens: a React revision at its mount page, any other at the
// entry file's own path, so relative links in a nested file still resolve.
export function entryUrl(host: string, entry: Served): string {
  if (entry.kind === "react") return urlOf(host);
  return urlOf(host) + entry.entrypoint.split("/").map(encodeURIComponent).join("/");
}

export interface ArtifactDeps {
  create: (prefs: WebPreferences) => WebContentsView | null;
  remove: (view: WebContentsView) => void;
  zoom: () => number;
  send: (shown: ArtifactShown) => void;
  session: () => Session;
}

export class ArtifactPane {
  readonly hosts = new ArtifactHosts();
  private view: WebContentsView | null = null;
  private host: string | null = null;
  private placed: { b: ArtifactBounds; show: boolean } | null = null;

  constructor(private readonly d: ArtifactDeps) {}

  // Shows this revision's content in a fresh page, so nothing of an earlier
  // page (its events, its storage in memory, its timers) carries over.
  // False when there is no window.
  show(entry: Served, request: string): boolean {
    this.drop();
    const view = this.make(request);
    if (view === null) return false;
    this.host = this.hosts.add(entry);
    if (this.placed !== null) this.place(view, this.placed.b, this.placed.show);
    void view.webContents.loadURL(entryUrl(this.host, entry)).catch((error: unknown) => {
      if (this.view === view) this.d.send({ request, kind: "error", message: error instanceof Error ? error.message : "Could not load the artifact." });
    });
    return true;
  }

  bounds(b: ArtifactBounds): void {
    this.placed = { b, show: true };
    if (this.view !== null) this.place(this.view, b, true);
  }

  hide(): void {
    if (this.placed !== null) this.placed.show = false;
    this.view?.setVisible(false);
  }

  close(): void {
    this.drop();
    this.placed = null;
  }

  private place(view: WebContentsView, b: ArtifactBounds, show: boolean): void {
    const z = this.d.zoom();
    view.setBounds({ x: Math.round(b.x * z), y: Math.round(b.y * z), width: Math.round(b.w * z), height: Math.round(b.h * z) });
    view.webContents.setZoomFactor(b.zoom);
    view.setVisible(show);
  }

  private drop(): void {
    const view = this.view;
    this.view = null;
    if (this.host !== null) this.hosts.remove(this.host);
    this.host = null;
    if (view === null) return;
    this.d.remove(view);
    if (!view.webContents.isDestroyed()) view.webContents.close();
  }

  private make(request: string): WebContentsView | null {
    const view = this.d.create(previewPrefs(this.d.session()));
    if (view === null) return null;
    this.view = view;
    view.setVisible(false);
    const wc = view.webContents;
    denyEscapes(wc);
    // The page has no channel to Deck; what it reports is what main sees,
    // tagged with the show it belongs to.
    const say = (kind: ArtifactShown["kind"], message: string) => {
      if (this.view === view) this.d.send({ request, kind, message });
    };
    wc.on("console-message", (e) => {
      if (e.level === "error") say("error", e.message);
    });
    wc.on("render-process-gone", (_e, details) => say("gone", details.reason));
    wc.on("did-finish-load", () => say("loaded", ""));
    return view;
  }
}
