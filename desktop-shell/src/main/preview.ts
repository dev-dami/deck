import { connect } from "node:net";
import type { Session, WebContents, WebContentsView, WebPreferences } from "electron";
import type { PreviewBounds, PreviewNav, PreviewState } from "../shared/bridge";

// The preview pane's page: a dev server on this computer, in a view that
// main owns and lays over the window. The page gets nothing of the app: a
// session of its own that lives in memory only, the sandbox, no preload and
// no IPC. It may show only loopback http pages of ports a probe found open.

// No "persist:" prefix: the session keeps its cookies and cache in memory.
export const PREVIEW_PARTITION = "preview";

const PROBE_MS = 300;

// Whether something accepts a TCP connection on host:port within ms.
export function listening(port: number, host: string, ms = PROBE_MS): Promise<boolean> {
  return new Promise((done) => {
    const s = connect({ port, host });
    const end = (ok: boolean) => {
      s.destroy();
      done(ok);
    };
    s.setTimeout(ms, () => end(false));
    s.once("connect", () => end(true));
    s.once("error", () => end(false));
  });
}

export type Verdict = "load" | "external" | "deny";

// What happens to a URL the view is asked to show: a loopback http page of
// an open port loads in the view, any other http(s) page opens in the
// system browser, and anything else is refused.
export function judgeUrl(raw: string, open: ReadonlySet<number>): Verdict {
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return "deny";
  }
  const loopback = u.hostname === "localhost" || u.hostname === "127.0.0.1";
  if (u.protocol === "http:" && loopback && u.username === "" && u.password === "" && open.has(Number(u.port))) return "load";
  return u.protocol === "http:" || u.protocol === "https:" ? "external" : "deny";
}

// The view's page runs with no preload, no Node and the sandbox on.
export function previewPrefs(session: Session): WebPreferences {
  return {
    session,
    sandbox: true,
    contextIsolation: true,
    nodeIntegration: false,
    nodeIntegrationInSubFrames: false,
    webSecurity: true,
    webviewTag: false,
  };
}

// The preview session asks for nothing: every permission is denied and
// every download is cancelled.
export function lockSession(ses: Session): void {
  ses.setPermissionRequestHandler((_wc, _perm, cb) => cb(false));
  ses.setPermissionCheckHandler(() => false);
  ses.setDevicePermissionHandler(() => false);
  ses.on("will-download", (e) => e.preventDefault());
}

// Keeps a page on the open ports: main checks each navigation, redirect and
// new window against judgeUrl.
export function guardPage(wc: WebContents, open: ReadonlySet<number>, openExternal: (url: string) => void): void {
  const away = (url: string): boolean => {
    const v = judgeUrl(url, open);
    if (v === "external") openExternal(url);
    return v !== "load";
  };
  wc.on("will-navigate", (e) => {
    if (away(e.url)) e.preventDefault();
  });
  wc.on("will-redirect", (e) => {
    if (away(e.url)) e.preventDefault();
  });
  wc.setWindowOpenHandler(({ url }) => {
    if (!away(url)) void wc.loadURL(url).catch(() => {});
    return { action: "deny" };
  });
}

export interface PreviewDeps {
  // Makes a view with these preferences and adds it to the window; null
  // when there is no window.
  create: (prefs: WebPreferences) => WebContentsView | null;
  remove: (view: WebContentsView) => void;
  // The window's zoom factor: the renderer's bounds are in its CSS pixels.
  zoom: () => number;
  openExternal: (url: string) => void;
  send: (state: PreviewState) => void;
  session: () => Session;
}

// The preview view and the ports it may show.
export class Preview {
  // The ports the last probe of each found open.
  readonly open = new Set<number>();
  private view: WebContentsView | null = null;
  private pageZoom = 1;

  constructor(private readonly d: PreviewDeps) {}

  // The ports that answer on 127.0.0.1 or [::1]: a dev server that binds
  // "localhost" may take either.
  async probe(ports: number[]): Promise<number[]> {
    const up = await Promise.all(
      ports.map(async (p) => (await Promise.all([listening(p, "127.0.0.1"), listening(p, "::1")])).some(Boolean)),
    );
    ports.forEach((p, i) => (up[i] ? this.open.add(p) : this.open.delete(p)));
    return ports.filter((_, i) => up[i]);
  }

  load(url: string): boolean {
    if (judgeUrl(url, this.open) !== "load") return false;
    const view = this.view ?? this.make();
    if (view === null) return false;
    void view.webContents.loadURL(url).catch(() => {});
    return true;
  }

  nav(n: PreviewNav): void {
    const wc = this.view?.webContents;
    if (wc === undefined) return;
    const h = wc.navigationHistory;
    if (n.action === "path") {
      let next: string;
      try {
        next = new URL(n.path, new URL(wc.getURL()).origin).href;
      } catch {
        return;
      }
      this.load(next);
    } else if (n.action === "back") {
      if (h.canGoBack()) h.goBack();
    } else if (n.action === "forward") {
      if (h.canGoForward()) h.goForward();
    } else wc.reload();
  }

  bounds(b: PreviewBounds): void {
    const view = this.view;
    if (view === null) return;
    const z = this.d.zoom();
    view.setBounds({ x: Math.round(b.x * z), y: Math.round(b.y * z), width: Math.round(b.w * z), height: Math.round(b.h * z) });
    if (b.zoom !== this.pageZoom) {
      this.pageZoom = b.zoom;
      view.webContents.setZoomFactor(b.zoom);
    }
    view.setVisible(true);
  }

  hide(): void {
    this.view?.setVisible(false);
  }

  close(): void {
    const view = this.view;
    if (view === null) return;
    this.view = null;
    this.d.remove(view);
    if (!view.webContents.isDestroyed()) view.webContents.close();
  }

  private make(): WebContentsView | null {
    const view = this.d.create(previewPrefs(this.d.session()));
    if (view === null) return null;
    this.view = view;
    this.pageZoom = 1;
    view.setVisible(false);
    const wc = view.webContents;
    guardPage(wc, this.open, this.d.openExternal);
    const send = () => {
      if (this.view !== view || wc.isDestroyed()) return;
      this.d.send({
        url: wc.getURL(),
        canBack: wc.navigationHistory.canGoBack(),
        canForward: wc.navigationHistory.canGoForward(),
        loading: wc.isLoading(),
        title: wc.getTitle(),
      });
    };
    for (const ev of ["did-start-loading", "did-stop-loading", "did-navigate", "did-navigate-in-page", "page-title-updated"] as const) {
      wc.on(ev as "did-start-loading", send);
    }
    // A zoom is kept per origin; a new port starts at 1.
    wc.on("did-navigate", () => wc.setZoomFactor(this.pageZoom));
    return view;
  }
}
