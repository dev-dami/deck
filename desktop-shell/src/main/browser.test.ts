import { EventEmitter } from "node:events";
import type { Session, WebContentsView, WebPreferences } from "electron";
import { expect, test } from "vitest";
import type { BrowserCommand, BrowserStateResponse, BrowserViewInput } from "../shared/bridge";
import { Browser, browserUrl, loopback, type BrowserRequest } from "./browser";

class Page extends EventEmitter {
  url = "";
  closed = false;
  attached = false;
  calls: { method: string; params: unknown }[] = [];
  nodes: unknown[] = [
    { role: { value: "button" }, name: { value: "Submit" }, backendDOMNodeId: 10 },
    { role: { value: "textbox" }, name: { value: "Password" }, value: { value: "private password" }, backendDOMNodeId: 11 },
  ];
  pause: (() => Promise<unknown>) | null = null;
  attrs = ["type", "text"];
  hit = 10;
  popup: ((input: { url: string }) => { action: string }) | null = null;
  redirectTo = "";
  fetchEnabled = false;
  fetchNext = 0;
  fetched = new Map<string, { resolve: () => void; reject: (e: Error) => void }>();
  navigationHistory = { canGoBack: () => false, canGoForward: () => false, goBack: () => {}, goForward: () => {} };
  debugger = Object.assign(new EventEmitter(), {
    isAttached: () => this.attached,
    attach: () => {
      this.attached = true;
    },
    sendCommand: async (method: string, params?: unknown): Promise<unknown> => {
      this.calls.push({ method, params });
      if (method === "Fetch.enable") {
        this.fetchEnabled = true;
        return {};
      }
      if (method === "Fetch.continueRequest" || method === "Fetch.failRequest") {
        const id = (params as { requestId: string }).requestId;
        const request = this.fetched.get(id);
        this.fetched.delete(id);
        if (method === "Fetch.continueRequest") request?.resolve();
        else request?.reject(new Error("Blocked by client"));
        return {};
      }
      if (method === "Accessibility.getFullAXTree") return this.pause === null ? { nodes: this.nodes } : this.pause();
      if (method === "DOM.describeNode") return { node: { attributes: this.attrs } };
      if (method === "DOM.resolveNode") return { object: { objectId: "node" } };
      if (method === "Runtime.callFunctionOn")
        return { result: { value: !(params as { functionDeclaration: string }).functionDeclaration.includes("contains") } };
      if (method === "DOM.getNodeForLocation") return { backendNodeId: this.hit };
      if (method === "DOM.getContentQuads") return { quads: [[0, 0, 100, 0, 100, 40, 0, 40]] };
      return {};
    },
  });
  async loadURL(url: string) {
    if (this.redirectTo !== "") {
      const target = this.redirectTo;
      this.redirectTo = "";
      let prevented = false;
      this.emit("will-redirect", {
        url: target,
        isMainFrame: true,
        preventDefault: () => {
          prevented = true;
        },
      });
      if (prevented) throw new Error("ERR_ABORTED");
      url = target;
    }
    if (this.fetchEnabled)
      await new Promise<void>((resolve, reject) => {
        const requestId = `fetch-${++this.fetchNext}`;
        this.fetched.set(requestId, { resolve, reject });
        this.debugger.emit("message", {}, "Fetch.requestPaused", { requestId, request: { url } });
      });
    this.url = url;
    this.emit("did-navigate");
  }
  getURL = () => this.url;
  getTitle = () => "Fixture";
  isLoading = () => false;
  isDestroyed = () => this.closed;
  close = () => {
    this.closed = true;
  };
  stop = () => {};
  reload = () => {
    this.emit("did-navigate");
  };
  setZoomFactor = (_z: number) => {};
  setWindowOpenHandler = (f: (input: { url: string }) => { action: string }) => {
    this.popup = f;
  };
  async capturePage() {
    return {
      getSize: () => ({ width: 100, height: 40 }),
      toPNG: () => Buffer.from("png"),
      resize() {
        return this;
      },
    };
  }
}
class View {
  webContents = new Page();
  visible = false;
  bounds: unknown;
  setVisible(v: boolean) {
    this.visible = v;
  }
  setBounds(b: unknown) {
    this.bounds = b;
  }
}
function fixture(approve: (origin: string) => Promise<boolean> = async () => true) {
  const views: View[] = [],
    states: BrowserStateResponse[] = [],
    prefs: WebPreferences[] = [],
    origins: string[] = [];
  const ses = Object.assign(new EventEmitter(), {
    setPermissionRequestHandler: () => {},
    setPermissionCheckHandler: () => {},
    setDevicePermissionHandler: () => {},
  });
  const browser = new Browser({
    create: (p) => {
      prefs.push(p);
      const v = new View();
      views.push(v);
      return v as unknown as WebContentsView;
    },
    remove: () => {},
    session: () => ses as unknown as Session,
    zoom: () => 2,
    changed: (_deck, state) => states.push(state),
    approve: async (_deck, _request, origin) => {
      origins.push(origin);
      return approve(origin);
    },
  });
  let next = 0;
  const request = (command: BrowserCommand, owner = "codex"): BrowserRequest => ({
    id: `r${++next}`,
    session_id: "room",
    controller_id: "controller",
    controller_generation: "v1",
    command,
    owner,
    deadline_at: Date.now() + 30_000,
  });
  const run = (command: BrowserCommand, owner = "codex") => browser.execute("deck", request(command, owner));
  return { browser, views, prefs, origins, states, request, run };
}

test("URL policy accepts web URLs, rejects privileged schemes and embedded credentials", () => {
  for (const raw of [
    "file:///secret",
    "chrome://settings",
    "devtools://x",
    "javascript:alert(1)",
    "https://user:pass@example.com",
    "data:text/html,x",
  ])
    expect(browserUrl(raw), raw).toBeNull();
  for (const raw of ["http://localhost:3000", "http://127.0.0.2:8000", "http://[::1]:3000"]) expect(loopback(browserUrl(raw)!)).toBe(true);
  expect(loopback(browserUrl("https://example.com")!)).toBe(false);
});

test("sandboxed tabs have no preload and publish a session id even after the final close", async () => {
  const f = fixture();
  const opened = await f.run({ action: "open", url: "http://localhost:3000" });
  expect(opened.error_code).toBe("");
  expect(f.prefs[0]).toMatchObject({
    sandbox: true,
    contextIsolation: true,
    nodeIntegration: false,
    nodeIntegrationInSubFrames: false,
    webSecurity: true,
    webviewTag: false,
  });
  expect(f.prefs[0]?.preload).toBeUndefined();
  expect(f.origins).toEqual([]);
  await f.run({ action: "close", tab_id: opened.tab!.id });
  expect(f.states.at(-1)).toEqual({ available: true, session_id: "room", tabs: [] });
  expect(f.views[0]?.webContents.closed).toBe(true);
});

test("snapshots exclude password values, produce versioned handles and reject stale clicks", async () => {
  const f = fixture();
  const tab = (await f.run({ action: "open", url: "http://localhost:3000" })).tab!;
  const snap = await f.run({ action: "snapshot", tab_id: tab.id });
  expect(snap.result?.text).toContain("UNTRUSTED PAGE CONTENT");
  expect(snap.result?.text).toContain("[e1] button: Submit");
  expect(snap.result?.text).not.toContain("private password");
  const click = { action: "click", tab_id: tab.id, element_ref: "e1", snapshot_version: snap.tab!.snapshot_version } as const;
  expect((await f.run(click)).error_code).toBe("");
  expect(f.views[0]!.webContents.calls.filter((c) => c.method === "Input.dispatchMouseEvent")).toHaveLength(2);
  expect((await f.run(click)).error_code).toBe("stale_snapshot");
  f.browser.close();
});

test("typing passwords requires user control; other agents read but cannot drive a tab", async () => {
  const f = fixture();
  const tab = (await f.run({ action: "open", url: "http://localhost:3000" })).tab!;
  const snap = await f.run({ action: "snapshot", tab_id: tab.id }, "claude");
  expect(snap.error_code).toBe("");
  expect((await f.run({ action: "close", tab_id: tab.id }, "claude")).error_code).toBe("not_owner");
  f.views[0]!.webContents.attrs = ["type", "password"];
  expect(
    (await f.run({ action: "type", tab_id: tab.id, element_ref: "e2", snapshot_version: snap.tab!.snapshot_version, text: "secret" }))
      .error_code,
  ).toBe("user_in_control");
  expect(f.views[0]!.webContents.calls.some((c) => c.method === "Input.insertText")).toBe(false);
  f.browser.close();
});

test("takeover cancels an in-flight snapshot, blocks agent captures and needs explicit release", async () => {
  const f = fixture();
  const tab = (await f.run({ action: "open", url: "http://localhost:3000" })).tab!;
  const w = f.views[0]!.webContents;
  let finish!: (value: unknown) => void;
  w.pause = () =>
    new Promise((resolve) => {
      finish = resolve;
    });
  const pending = f.run({ action: "snapshot", tab_id: tab.id });
  await Promise.resolve();
  expect((await f.run({ action: "take_control", tab_id: tab.id }, "")).tab?.user_in_control).toBe(true);
  expect((await pending).error_code).toBe("cancelled");
  finish({ nodes: w.nodes });
  expect((await f.run({ action: "screenshot", tab_id: tab.id })).error_code).toBe("user_in_control");
  expect((await f.run({ action: "release_control", tab_id: tab.id }, "")).tab?.user_in_control).toBe(false);
  f.browser.close();
});

test("origin approval precedes loading, including a redirect to another site", async () => {
  const f = fixture(async (origin) => origin !== "https://denied.test");
  expect((await f.run({ action: "open", url: "https://denied.test" })).error_code).toBe("origin_denied");
  expect(f.views).toHaveLength(0);
  const tab = (await f.run({ action: "open", url: "http://localhost:3000" })).tab!;
  f.views[0]!.webContents.redirectTo = "https://identity.test/login";
  const out = await f.run({ action: "navigate", tab_id: tab.id, url: "https://app.test/login" });
  expect(out.error_code).toBe("");
  expect(f.origins).toEqual(["https://denied.test", "https://app.test", "https://identity.test"]);
  expect(out.tab?.url).toBe("https://identity.test/login");
  f.browser.close();
});

test("native presentation rejects a tab from another room and native input requires Take control", async () => {
  const f = fixture();
  const tab = (await f.run({ action: "open", url: "http://localhost:3000" })).tab!;
  const b: BrowserViewInput = { deck_id: "deck", session_id: "room", tab_id: tab.id, bounds: { x: 10, y: 20, w: 300, h: 400, zoom: 1 } };
  expect(() => f.browser.present({ ...b, session_id: "other" })).toThrow("does not belong");
  f.browser.present(b);
  expect(f.views[0]!.bounds).toEqual({ x: 20, y: 40, width: 600, height: 800 });
  let prevented = false;
  f.views[0]!.webContents.emit("before-mouse-event", {
    preventDefault: () => {
      prevented = true;
    },
  });
  expect(prevented).toBe(true);
  await f.run({ action: "take_control", tab_id: tab.id }, "");
  prevented = false;
  f.views[0]!.webContents.emit("before-input-event", {
    preventDefault: () => {
      prevented = true;
    },
  });
  expect(prevented).toBe(false);
  f.browser.close();
});

test("closing a deck cancels an origin wait before any tab is created", async () => {
  let allow!: (value: boolean) => void;
  const f = fixture(
    () =>
      new Promise((resolve) => {
        allow = resolve;
      }),
  );
  const pending = f.run({ action: "open", url: "https://example.test" });
  await Promise.resolve();
  f.browser.closeDeck("deck");
  expect((await pending).error_code).toBe("cancelled");
  allow(true);
  await Promise.resolve();
  expect(f.browser.state("deck", "room").tabs).toEqual([]);
  expect(f.views).toHaveLength(0);
});

test("a command whose execution deadline passed never creates a view", async () => {
  const f = fixture();
  const request = f.request({ action: "open", url: "http://localhost:3000" });
  request.deadline_at = Date.now() - 1;
  expect((await f.browser.execute("deck", request)).error_code).toBe("timeout");
  expect(f.views).toHaveLength(0);
});

test("an overlay covering the target prevents a click", async () => {
  const f = fixture();
  const tab = (await f.run({ action: "open", url: "http://localhost:3000" })).tab!;
  const snapshot = await f.run({ action: "snapshot", tab_id: tab.id });
  f.views[0]!.webContents.hit = 99;
  const clicked = await f.run({ action: "click", tab_id: tab.id, element_ref: "e1", snapshot_version: snapshot.tab!.snapshot_version });
  expect(clicked.error_code).toBe("stale_snapshot");
  expect(f.views[0]!.webContents.calls.some((c) => c.method === "Input.dispatchMouseEvent")).toBe(false);
  f.browser.close();
});
