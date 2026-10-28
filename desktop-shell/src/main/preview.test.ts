import { EventEmitter } from "node:events";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import type { Session, WebContents } from "electron";
import { expect, test } from "vitest";
import { browserPartition, guardPage, judgeUrl, lockSession, openPorts, pagePrefs, parseWebUrl, type Who } from "./preview";

// A webContents with what the guard calls, and the events main listens to.
class FakePage extends EventEmitter {
  loads: string[] = [];
  openHandler: ((d: { url: string }) => { action: string }) | null = null;
  setWindowOpenHandler(fn: (d: { url: string }) => { action: string }) {
    this.openHandler = fn;
  }
  async loadURL(url: string) {
    this.loads.push(url);
  }
  // A navigation the page starts; true when main let it go on.
  navigate(event: "will-navigate" | "will-redirect", url: string): boolean {
    let stopped = false;
    this.emit(event, { url, preventDefault: () => (stopped = true) });
    return !stopped;
  }
}

const PERSON: Who = { kind: "person" };
const agent = (over: Partial<Extract<Who, { kind: "agent" }>> = {}): Who => ({ kind: "agent", at: "https://shop.example/cart", allowed: [], free: false, ...over });

test("the person's tab goes to any web page; anything else is refused", () => {
  for (const url of ["http://localhost:3000/", "https://example.com/a?b#c", "http://192.168.1.5:8080/", "http://[::1]:3000/", "https://sub.example.co.uk:8443/x"]) {
    expect(judgeUrl(url, PERSON), url).toBe("load");
  }
  for (const url of [
    "file:///etc/passwd",
    "app://deck/index.html",
    "javascript:alert(1)",
    "data:text/html,x",
    "chrome://gpu",
    "devtools://devtools/bundled/inspector.html",
    "view-source:https://example.com/",
    "blob:https://example.com/x",
    "ftp://example.com/",
    "https://user:pw@example.com/",
    "not a url",
    "",
    "example.com",
  ]) {
    expect(judgeUrl(url, PERSON), url).toBe("deny");
  }
});

test("an agent's tab goes to this computer's pages and to the site it is on; any other site asks", () => {
  const a = agent();
  expect(judgeUrl("http://localhost:5173/app", a)).toBe("load");
  expect(judgeUrl("http://127.0.0.1:3000/", a)).toBe("load");
  expect(judgeUrl("http://[::1]:3000/", a)).toBe("load");
  // The same site: a redirect to https, to or from www.
  expect(judgeUrl("https://shop.example/checkout", a)).toBe("load");
  expect(judgeUrl("https://www.shop.example/", a)).toBe("load");
  expect(judgeUrl("http://shop.example/", a)).toBe("load");
  expect(judgeUrl("https://pay.shop.example/", a)).toBe("ask");
  expect(judgeUrl("https://shop.example.evil.com/", a)).toBe("ask");
  expect(judgeUrl("https://other.org/", a)).toBe("ask");
  expect(judgeUrl("http://localhost.evil.com/", a)).toBe("ask");
  // A different port of the same host on the same scheme is another service.
  expect(judgeUrl("https://shop.example:8443/", a)).toBe("ask");
});

test("an agent's tab goes to the origins the person approved, and to every page in yolo", () => {
  const a = agent({ allowed: ["https://login.example", "https://docs.org"] });
  expect(judgeUrl("https://login.example/oauth?x=1", a)).toBe("load");
  expect(judgeUrl("https://docs.org:443/a", a)).toBe("load");
  expect(judgeUrl("http://login.example/", a)).toBe("ask");
  expect(judgeUrl("https://other.org/", a)).toBe("ask");
  const yolo = agent({ free: true });
  expect(judgeUrl("https://anything.example/", yolo)).toBe("load");
  // Even in yolo: not a web page, or one with a password in its address.
  for (const url of ["file:///etc/passwd", "chrome://settings", "javascript:alert(1)", "https://u:p@example.com/", "devtools://x"]) {
    expect(judgeUrl(url, yolo), url).toBe("deny");
  }
});

test("an address typed or named is read as a web page", () => {
  expect(parseWebUrl("example.com/docs")?.href).toBe("https://example.com/docs");
  expect(parseWebUrl("localhost:3000/a")?.href).toBe("http://localhost:3000/a");
  expect(parseWebUrl("127.0.0.1:8080")?.href).toBe("http://127.0.0.1:8080/");
  expect(parseWebUrl("  https://example.com  ")?.href).toBe("https://example.com/");
  for (const s of ["", "two words", "javascript:alert(1)", "data:text/plain,x", "mailto:a@b.c", "file:///x", "ftp://x.example/", "http://:80/", "https://u:p@x.example/"]) {
    expect(parseWebUrl(s), s).toBeNull();
  }
});

test("a Deck's tabs share one session that lives in memory only, sandboxed, with no preload and no Node", () => {
  expect(browserPartition("local-1").startsWith("persist:")).toBe(false);
  expect(browserPartition("local-1")).not.toBe(browserPartition("local-2"));
  const ses = {} as Session;
  const prefs = pagePrefs(ses);
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
  for (const perm of ["media", "clipboard-read", "geolocation", "notifications"]) {
    (got["request"] as (wc: unknown, p: string, cb: (ok: boolean) => void) => void)({}, perm, (ok) => answers.push(ok));
  }
  expect(answers).toEqual([false, false, false, false]);
  expect((got["check"] as () => boolean)()).toBe(false);
  expect((got["device"] as () => boolean)()).toBe(false);
  let cancelled = false;
  (got["will-download"] as (e: { preventDefault: () => void }) => void)({ preventDefault: () => (cancelled = true) });
  expect(cancelled).toBe(true);
});

test("main checks each navigation, redirect and new window of the person's page", () => {
  const page = new FakePage();
  const blocked: string[] = [];
  guardPage(page as unknown as WebContents, { who: () => PERSON, blocked: (u) => blocked.push(u) });
  expect(page.navigate("will-navigate", "https://example.com/next")).toBe(true);
  expect(page.navigate("will-redirect", "http://localhost:4000/")).toBe(true);
  expect(page.navigate("will-navigate", "file:///etc/passwd")).toBe(false);
  expect(page.navigate("will-navigate", "chrome://settings")).toBe(false);
  // A new window never opens: where it asks, this tab goes.
  expect(page.openHandler?.({ url: "https://example.org/popup" })).toEqual({ action: "deny" });
  expect(page.openHandler?.({ url: "javascript:alert(1)" })).toEqual({ action: "deny" });
  expect(page.loads).toEqual(["https://example.org/popup"]);
  // Nothing to ask of the person: they own the tab.
  expect(blocked).toEqual([]);
});

test("an agent's page is kept from a site nobody approved, and the block is reported", () => {
  const page = new FakePage();
  const blocked: string[] = [];
  let who = agent({ at: "https://shop.example/cart" });
  guardPage(page as unknown as WebContents, { who: () => who, blocked: (u) => blocked.push(u) });
  expect(page.navigate("will-navigate", "https://shop.example/pay")).toBe(true);
  expect(page.navigate("will-redirect", "https://www.shop.example/pay")).toBe(true);
  expect(page.navigate("will-redirect", "https://tracker.example/hop?to=x")).toBe(false);
  expect(page.navigate("will-navigate", "file:///etc/passwd")).toBe(false);
  expect(page.openHandler?.({ url: "https://evil.example/popup" })).toEqual({ action: "deny" });
  expect(page.loads).toEqual([]);
  // Only a web page the agent could ask for is reported; a file: is not.
  expect(blocked).toEqual(["https://tracker.example/hop?to=x", "https://evil.example/popup"]);
  // Once the person approves the site, the same navigation goes through.
  who = agent({ at: "https://shop.example/cart", allowed: ["https://tracker.example"] });
  expect(page.navigate("will-redirect", "https://tracker.example/hop?to=x")).toBe(true);
  who = agent({ free: true });
  expect(page.navigate("will-navigate", "https://anywhere.example/")).toBe(true);
});

test("the ports that accept a connection are told apart from those that do not", async () => {
  const server = createServer((_q, r) => r.end("ok"));
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  const open = (server.address() as AddressInfo).port;
  // A port that was open a moment ago and is closed now.
  const closed = createServer();
  await new Promise<void>((done) => closed.listen(0, "127.0.0.1", done));
  const gone = (closed.address() as AddressInfo).port;
  await new Promise<void>((done) => closed.close(() => done()));
  try {
    expect(await openPorts([gone, open])).toEqual([open]);
  } finally {
    server.close();
  }
});
