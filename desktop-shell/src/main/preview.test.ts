import { EventEmitter } from "node:events";
import type { Session, WebContents, WebContentsView, WebPreferences } from "electron";
import { describe, expect, test } from "vitest";
import type { PreviewState } from "../shared/bridge";
import { EXTERNAL_GAP_MS, guardPage, judgeUrl, lockSession, Preview, PREVIEW_PARTITION, previewPrefs } from "./preview";

// A webContents with what the preview calls, and the events main listens to.
class FakePage extends EventEmitter {
  url = "";
  loads: string[] = [];
  zoom = 1;
  closed = false;
  reloads = 0;
  openHandler: ((d: { url: string }) => { action: string }) | null = null;
  back = false;
  navigationHistory = {
    canGoBack: () => this.back,
    canGoForward: () => false,
    goBack: () => {
      this.url = "http://localhost:3000/before";
    },
    goForward: () => {},
  };
  setWindowOpenHandler(fn: (d: { url: string }) => { action: string }) {
    this.openHandler = fn;
  }
  async loadURL(url: string) {
    this.loads.push(url);
    this.url = url;
    this.emit("did-navigate");
  }
  getURL = () => this.url;
  getTitle = () => "Fixture";
  isLoading = () => false;
  reload() {
    this.reloads++;
  }
  setZoomFactor(z: number) {
    this.zoom = z;
  }
  isDestroyed = () => this.closed;
  close() {
    this.closed = true;
  }
  // A navigation the page starts; true when main let it go on.
  navigate(event: "will-navigate" | "will-redirect", url: string): boolean {
    let stopped = false;
    this.emit(event, { url, preventDefault: () => (stopped = true) });
    return !stopped;
  }
}

class FakeView {
  webContents = new FakePage();
  bounds: unknown = null;
  visible = true;
  setBounds(b: unknown) {
    this.bounds = b;
  }
  setVisible(v: boolean) {
    this.visible = v;
  }
}

const OPEN = new Set([3000]);

test("only a loopback http page of an open port loads; other web pages go to the browser; the rest is refused", () => {
  expect(judgeUrl("http://localhost:3000/", OPEN)).toBe("load");
  expect(judgeUrl("http://127.0.0.1:3000/a?b#c", OPEN)).toBe("load");
  expect(judgeUrl("http://localhost:4000/", OPEN)).toBe("external");
  expect(judgeUrl("https://localhost:3000/", OPEN)).toBe("external");
  expect(judgeUrl("http://user:pw@localhost:3000/", OPEN)).toBe("external");
  expect(judgeUrl("http://example.com:3000/", OPEN)).toBe("external");
  expect(judgeUrl("http://localhost.example.com:3000/", OPEN)).toBe("external");
  expect(judgeUrl("http://[::1]:3000/", OPEN)).toBe("external");
  expect(judgeUrl("https://example.com/", OPEN)).toBe("external");
  for (const url of ["file:///etc/passwd", "app://deck/index.html", "javascript:alert(1)", "data:text/html,x", "chrome://gpu", "not a url"]) {
    expect(judgeUrl(url, OPEN), url).toBe("deny");
  }
});

test("the page runs in its own in-memory session, sandboxed, with no preload and no Node", () => {
  expect(PREVIEW_PARTITION.startsWith("persist:")).toBe(false);
  const ses = {} as Session;
  const prefs = previewPrefs(ses);
  expect(prefs).toEqual({
    session: ses,
    sandbox: true,
    contextIsolation: true,
    nodeIntegration: false,
    nodeIntegrationInSubFrames: false,
    webSecurity: true,
    webviewTag: false,
  });
  expect("preload" in prefs).toBe(false);
});

test("the session denies every permission and cancels every download", () => {
  const got: Record<string, (...a: never[]) => unknown> = {};
  const ses = {
    setPermissionRequestHandler: (fn: never) => (got["request"] = fn),
    setPermissionCheckHandler: (fn: never) => (got["check"] = fn),
    setDevicePermissionHandler: (fn: never) => (got["device"] = fn),
    on: (ev: string, fn: never) => (got[ev] = fn),
  } as unknown as Session;
  lockSession(ses);
  const answers: boolean[] = [];
  (got["request"] as (wc: unknown, p: string, cb: (ok: boolean) => void) => void)({}, "media", (ok) => answers.push(ok));
  (got["request"] as (wc: unknown, p: string, cb: (ok: boolean) => void) => void)({}, "clipboard-read", (ok) => answers.push(ok));
  expect(answers).toEqual([false, false]);
  expect((got["check"] as () => boolean)()).toBe(false);
  expect((got["device"] as () => boolean)()).toBe(false);
  let cancelled = false;
  (got["will-download"] as (e: { preventDefault: () => void }) => void)({ preventDefault: () => (cancelled = true) });
  expect(cancelled).toBe(true);
});

test("main checks each navigation, redirect and new window of the page", () => {
  const page = new FakePage();
  const opened: string[] = [];
  // Each check runs a second after the last, so the rate limit lets each through.
  let t = 0;
  guardPage(page as unknown as WebContents, OPEN, (u) => opened.push(u), () => (t += EXTERNAL_GAP_MS));
  expect(page.navigate("will-navigate", "http://localhost:3000/next")).toBe(true);
  expect(page.navigate("will-navigate", "https://example.com/")).toBe(false);
  expect(page.navigate("will-redirect", "http://localhost:4000/")).toBe(false);
  expect(page.navigate("will-navigate", "file:///etc/passwd")).toBe(false);
  expect(opened).toEqual(["https://example.com/", "http://localhost:4000/"]);
  // A new window never opens: a page of an open port loads in the view.
  expect(page.openHandler?.({ url: "http://localhost:3000/popup" })).toEqual({ action: "deny" });
  expect(page.openHandler?.({ url: "https://example.org/" })).toEqual({ action: "deny" });
  expect(page.openHandler?.({ url: "javascript:alert(1)" })).toEqual({ action: "deny" });
  expect(page.loads).toEqual(["http://localhost:3000/popup"]);
  expect(opened).toEqual(["https://example.com/", "http://localhost:4000/", "https://example.org/"]);
});

test("a page that navigates away in a loop opens one browser tab a second", () => {
  const page = new FakePage();
  const opened: string[] = [];
  let t = 0;
  guardPage(page as unknown as WebContents, OPEN, (u) => opened.push(u), () => t);
  for (let i = 0; i < 50; i++) expect(page.navigate("will-navigate", `https://example.com/${i}`)).toBe(false);
  expect(opened).toEqual(["https://example.com/0"]);
  t += EXTERNAL_GAP_MS;
  page.navigate("will-navigate", "https://example.com/later");
  expect(opened).toEqual(["https://example.com/0", "https://example.com/later"]);
});

describe("the view", () => {
  function setup() {
    const views: FakeView[] = [];
    const removed: FakeView[] = [];
    const states: PreviewState[] = [];
    const prefs: WebPreferences[] = [];
    const p = new Preview({
      create: (pr) => {
        prefs.push(pr);
        const v = new FakeView();
        views.push(v);
        return v as unknown as WebContentsView;
      },
      remove: (v) => removed.push(v as unknown as FakeView),
      zoom: () => 1.25,
      openExternal: () => {},
      send: (s) => states.push(s),
      session: () => ({}) as Session,
    });
    p.open.add(3000);
    return { p, views, removed, states, prefs };
  }

  test("loads only a URL of an open port, and makes one view for it", () => {
    const { p, views, states, prefs } = setup();
    expect(p.load("http://localhost:4000/")).toBe(false);
    expect(p.load("https://localhost:3000/")).toBe(false);
    expect(views).toHaveLength(0);
    expect(p.load("http://localhost:3000/")).toBe(true);
    expect(p.load("http://localhost:3000/docs")).toBe(true);
    expect(views).toHaveLength(1);
    expect(prefs[0]).not.toHaveProperty("preload");
    expect(views[0]!.webContents.loads).toEqual(["http://localhost:3000/", "http://localhost:3000/docs"]);
    // Hidden until the renderer says where it goes.
    expect(views[0]!.visible).toBe(false);
    expect(states.at(-1)).toEqual({ url: "http://localhost:3000/docs", canBack: false, canForward: false, loading: false, title: "Fixture" });
  });

  test("bounds scale with the window's zoom and show the view; hide and close", () => {
    const { p, views, removed } = setup();
    p.bounds({ x: 1, y: 2, w: 3, h: 4, zoom: 1 });
    p.load("http://localhost:3000/");
    const v = views[0]!;
    p.bounds({ x: 100, y: 48, w: 480, h: 600, zoom: 0.5 });
    expect(v.bounds).toEqual({ x: 125, y: 60, width: 600, height: 750 });
    expect(v.visible).toBe(true);
    expect(v.webContents.zoom).toBe(0.5);
    p.hide();
    expect(v.visible).toBe(false);
    p.close();
    expect(removed).toEqual([v]);
    expect(v.webContents.closed).toBe(true);
    p.close();
    expect(removed).toHaveLength(1);
  });

  test("a path stays on the page's origin; back and reload", () => {
    const { p, views } = setup();
    p.nav({ action: "reload" });
    p.load("http://localhost:3000/");
    const page = views[0]!.webContents;
    p.nav({ action: "path", path: "/docs?q=1" });
    p.nav({ action: "path", path: "//example.com/x" });
    expect(page.loads).toEqual(["http://localhost:3000/", "http://localhost:3000/docs?q=1"]);
    p.nav({ action: "back" });
    expect(page.url).toBe("http://localhost:3000/docs?q=1");
    page.back = true;
    p.nav({ action: "back" });
    expect(page.url).toBe("http://localhost:3000/before");
    p.nav({ action: "reload" });
    expect(page.reloads).toBe(1);
  });
});
