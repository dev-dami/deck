import type { BrowserWindow, Session, WebContents, WebContentsView } from "electron";
import type { BrowserBounds } from "../shared/bridge";
import type { BrowserPage, PageFactory, PageHooks } from "./browser";
import type { Cdp } from "./pageControl";
import { guardPage, lockSession, pagePrefs } from "./preview";
import type { Throttle } from "./tabs";

// The Electron half of the browser: a tab's page is a WebContentsView of
// the app's window, driven over webContents.debugger. Every page of the app
// shares the one Chromium Electron already runs; a page is a renderer for
// its site, made when the tab is used and let go when it is discarded.

// A page nobody looks at is one pixel, and Chromium is told the page is a
// window (VIEWPORT) so its layout, and what a click at a point means, are
// the same as when shown. Chromium does not deliver mouse input to a hidden
// page, so a page is made visible (still one pixel) for as long as an agent
// works on it and hidden again REST_MS after, which lets Chromium slow it.
const PARKED = { x: 0, y: 0, width: 1, height: 1 };
const VIEWPORT = { width: 1280, height: 800, deviceScaleFactor: 1, mobile: false };
const REST_MS = 3000;

const NAV_WAIT_MS = 20_000;

export interface ViewDeps {
  /** The window pages are added to; null when there is none. */
  window: () => BrowserWindow | null;
  /** The window's zoom factor: the renderer's bounds are in its CSS pixels. */
  zoom: () => number;
  /** Makes the view of a page; the app uses WebContentsView. */
  newView: (prefs: ReturnType<typeof pagePrefs>) => WebContentsView;
  /** The in-memory session of a Deck's tabs. */
  sessionFor: (deck: string) => Session;
}

class ElectronPage implements BrowserPage {
  private n = 0;
  private attached = false;
  private shown = false;
  private emulated = false;
  private resting: ReturnType<typeof setTimeout> | null = null;
  readonly cdp: Cdp;
  private readonly wc: WebContents;

  constructor(
    private readonly view: WebContentsView,
    private readonly d: ViewDeps,
    hooks: PageHooks,
  ) {
    this.wc = view.webContents;
    const wc = this.wc;
    view.setBackgroundColor("#00000000");
    wc.setBackgroundThrottling(true);
    wc.setAudioMuted(true);
    guardPage(wc, { who: () => hooks.who(), blocked: (url) => hooks.blocked(url) });
    for (const ev of ["did-start-loading", "did-stop-loading", "did-navigate", "did-navigate-in-page", "page-title-updated"] as const) {
      wc.on(ev as "did-start-loading", () => hooks.changed());
    }
    wc.on("did-navigate", () => this.n++);
    wc.debugger.on("message", (_e, method, params: { type?: string; message?: string }) => {
      // A dialog would hold the page (and the agent) until someone answers.
      if (method !== "Page.javascriptDialogOpening") return;
      hooks.note(`the page opened a dialog (${params.type ?? "alert"}): ${JSON.stringify((params.message ?? "").slice(0, 200))}; Deck ${params.type === "alert" ? "closed" : "dismissed"} it.`);
      void this.cdp.send("Page.handleJavaScriptDialog", { accept: params.type === "alert" || params.type === "beforeunload" }).catch(() => {});
    });
    wc.debugger.on("detach", () => {
      this.attached = false;
    });
    this.cdp = {
      send: async (method, params) => {
        if (!this.attached) {
          if (!wc.debugger.isAttached()) wc.debugger.attach("1.3");
          this.attached = true;
          await wc.debugger.sendCommand("Page.enable");
        }
        return wc.debugger.sendCommand(method, params);
      },
    };
  }

  info() {
    const wc = this.wc;
    const h = wc.navigationHistory;
    return { url: wc.getURL(), title: wc.getTitle(), loading: wc.isLoading(), canBack: h.canGoBack(), canForward: h.canGoForward() };
  }

  url = () => this.wc.getURL();
  title = () => this.wc.getTitle();
  loading = () => this.wc.isLoading();
  epoch = () => this.n;

  async navigate(url: string): Promise<{ ok: boolean; error?: string }> {
    const wc = this.wc;
    let timer: ReturnType<typeof setTimeout> | undefined;
    try {
      // Resolves when the page loaded, rejects when it failed; a slow page
      // is left loading rather than waited for.
      await Promise.race([
        wc.loadURL(url),
        new Promise<void>((done) => {
          timer = setTimeout(done, NAV_WAIT_MS);
        }),
      ]);
      return { ok: true };
    } catch (e) {
      const msg = e instanceof Error ? e.message : String(e);
      // A redirect the guard stopped, or a newer navigation, aborts this one.
      if (/ERR_ABORTED/.test(msg) && wc.isLoading()) return { ok: true };
      const code = /ERR_[A-Z0-9_]+/.exec(msg);
      return { ok: false, error: code === null ? msg : `net::${code[0]}` };
    } finally {
      clearTimeout(timer);
    }
  }

  async history(dir: "back" | "forward" | "reload"): Promise<boolean> {
    const h = this.wc.navigationHistory;
    if (dir === "reload") {
      this.wc.reload();
      return true;
    }
    if (dir === "back" ? !h.canGoBack() : !h.canGoForward()) return false;
    if (dir === "back") h.goBack();
    else h.goForward();
    return true;
  }

  whenLoaded(ms: number): Promise<boolean> {
    const wc = this.wc;
    if (!wc.isLoading()) return Promise.resolve(true);
    return new Promise((done) => {
      const finish = (ok: boolean) => {
        clearTimeout(timer);
        wc.removeListener("did-stop-loading", onStop);
        done(ok);
      };
      const onStop = () => finish(true);
      const timer = setTimeout(() => finish(false), ms);
      wc.once("did-stop-loading", onStop);
    });
  }

  /** Sets the page's viewport to a window's, or back to its view's size. */
  private async emulate(on: boolean): Promise<void> {
    if (on === this.emulated || this.wc.isDestroyed()) return;
    this.emulated = on;
    await this.cdp.send(on ? "Emulation.setDeviceMetricsOverride" : "Emulation.clearDeviceMetricsOverride", on ? VIEWPORT : undefined);
  }

  async awake(on: boolean): Promise<void> {
    if (this.resting !== null) clearTimeout(this.resting);
    this.resting = null;
    if (this.wc.isDestroyed() || this.shown) return;
    if (on) {
      await this.emulate(true);
      this.view.setVisible(true);
      return;
    }
    this.resting = setTimeout(() => {
      this.resting = null;
      if (!this.shown && !this.wc.isDestroyed()) this.view.setVisible(false);
    }, REST_MS);
  }

  place(b: BrowserBounds | null): void {
    if (b === null) {
      this.view.setBounds(PARKED);
      return;
    }
    const z = this.d.zoom();
    this.view.setBounds({ x: Math.round(b.x * z), y: Math.round(b.y * z), width: Math.round(b.w * z), height: Math.round(b.h * z) });
  }

  apply(t: Throttle): void {
    const wc = this.wc;
    if (wc.isDestroyed()) return;
    this.shown = t.visible;
    // The person sees the page at the size of the panel; an agent at a window's.
    if (t.visible) void this.emulate(false).catch(() => {});
    else this.view.setBounds(PARKED);
    this.view.setVisible(t.visible);
    wc.setBackgroundThrottling(t.throttling);
    wc.setAudioMuted(t.muted);
    try {
      // Only an offscreen page takes a frame rate; the others ignore it.
      wc.setFrameRate(t.frameRate);
    } catch {
      // Not offscreen.
    }
  }

  destroy(): void {
    const wc = this.wc;
    if (wc.isDestroyed()) return;
    try {
      if (wc.debugger.isAttached()) wc.debugger.detach();
    } catch {
      // Already gone.
    }
    const win = this.d.window();
    if (win !== null && !win.isDestroyed()) win.contentView.removeChildView(this.view);
    wc.close();
  }
}

// The user-agent of a Deck's tabs says Chrome, not the app: sites sniff it.
const stripApp = (ua: string): string => ua.replace(/ (Electron|Deck|deck-desktop)\/\S+/gi, "");

export function electronPages(d: ViewDeps): PageFactory {
  const locked = new WeakSet<Session>();
  return {
    create(tab, hooks) {
      const win = d.window();
      if (win === null || win.isDestroyed()) return null;
      const ses = d.sessionFor(tab.deck);
      if (!locked.has(ses)) {
        locked.add(ses);
        lockSession(ses);
        ses.setUserAgent(stripApp(ses.getUserAgent()));
      }
      const view = d.newView(pagePrefs(ses));
      win.contentView.addChildView(view);
      view.setBounds(PARKED);
      view.setVisible(false);
      return new ElectronPage(view, d, hooks);
    },
  };
}
