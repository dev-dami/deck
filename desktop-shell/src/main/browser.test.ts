import { EventEmitter } from "node:events";
import type { Session, WebContentsView, WebPreferences } from "electron";
import { expect, test } from "vitest";
import type { BrowserCommand, BrowserStateResponse, BrowserViewInput } from "../shared/bridge";
import { Browser, type BrowserDeps, browserUrl, loopback, type BrowserRequest } from "./browser";

class Page extends EventEmitter {
  url = "";
  closed = false;
  attached = false;
  calls: { method: string; params: unknown }[] = [];
  nodes: unknown[] = [
    { nodeId: "1", role: { value: "RootWebArea" }, childIds: ["2", "3"] },
    { nodeId: "2", parentId: "1", role: { value: "button" }, name: { value: "Submit" }, backendDOMNodeId: 10, childIds: [] },
    { nodeId: "3", parentId: "1", role: { value: "textbox" }, name: { value: "Password" }, value: { value: "private password" }, backendDOMNodeId: 11, childIds: [] },
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
      if (method === "DOM.getDocument") return { root: { nodeId: 1 } };
      if (method === "DOM.querySelectorAll") return { nodeIds: [] };
      if (method === "DOM.describeNode") return { node: { attributes: this.attrs } };
      if (method === "DOM.resolveNode") return { object: { objectId: "node" } };
      if (method === "Runtime.callFunctionOn")
        return { result: { value: !(params as { functionDeclaration: string }).functionDeclaration.includes("contains") } };
      if (method === "DOM.getNodeForLocation") return { backendNodeId: this.hit };
      if (method === "DOM.getContentQuads") return { quads: [[0, 0, 100, 0, 100, 40, 0, 40]] };
      if (method === "Page.getLayoutMetrics") return { cssVisualViewport: { clientWidth: 1280, clientHeight: 800 } };
      if (method === "Runtime.evaluate") return { result: { value: { y: 0, max: 0 } } };
      if (method === "Page.captureScreenshot") return { data: Buffer.from("jpeg bytes").toString("base64") };
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
  throttled: boolean | null = null;
  muted: boolean | null = null;
  setBackgroundThrottling = (on: boolean) => {
    this.throttled = on;
  };
  setAudioMuted = (on: boolean) => {
    this.muted = on;
  };
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
function fixture(approve: (origin: string) => Promise<boolean> = async () => true, extra: Partial<BrowserDeps> = {}) {
  const views: View[] = [],
    states: BrowserStateResponse[] = [],
    prefs: WebPreferences[] = [],
    origins: string[] = [],
    removed: View[] = [];
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
    remove: (v) => {
      removed.push(v as unknown as View);
    },
    session: () => ses as unknown as Session,
    zoom: () => 2,
    ...extra,
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
  return { browser, views, removed, prefs, origins, states, request, run };
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
  // A name that only starts like a loopback address is a site like any other.
  for (const raw of ["http://127.attacker.example", "http://127.0.0.1.attacker.example", "http://localhost.attacker.example"])
    expect(loopback(browserUrl(raw)!), raw).toBe(false);
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
  expect(snap.result?.text).toContain('button [e1] "Submit"');
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
  while (finish === undefined) await new Promise((resolve) => setTimeout(resolve, 0));
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

// ---- many agents, one Chromium ----

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));
async function until(ok: () => boolean): Promise<void> {
  for (let n = 0; n < 400 && !ok(); n++) await sleep(5);
  expect(ok()).toBe(true);
}
type Fixture = ReturnType<typeof fixture>;
const openTab = async (f: Fixture, owner: string, url = "http://localhost:3000") => {
  const out = await f.run({ action: "open", url }, owner);
  expect(out.error_code, out.result?.text).toBe("");
  return out.tab!;
};
const roster = (f: Fixture) => f.browser.state("deck", "room").tabs.map((t) => `${t.owner}${t.asleep ? ":asleep" : ""}`);
// Every tab shares one timestamp, as ops landing in one millisecond do.
const frozen = (limits: Partial<BrowserDeps["limits"] & object> = {}): Partial<BrowserDeps> => ({
  now: () => 1000,
  limits: { live: 8, perAgent: 6, idleMs: 60_000, ...limits },
});

test("pages past the live budget are let go oldest first, whatever the clock says, and come back at their address", async () => {
  // Repeated, so a chance ordering of equal timestamps would show.
  for (let round = 0; round < 20; round++) {
    const f = fixture(undefined, frozen({ live: 2 }));
    const first = await openTab(f, "claude1", "http://localhost:3001");
    await openTab(f, "codex1", "http://localhost:3002");
    await openTab(f, "pi1", "http://localhost:3003");
    expect(roster(f)).toEqual(["claude1:asleep", "codex1", "pi1"]);
    expect(f.removed).toHaveLength(1);
    expect(f.browser.state("deck", "room").tabs[0]).toMatchObject({ url: "http://localhost:3001/", asleep: true, loading: false });
    // Its agent returns: the page is made again at the same address, and the
    // oldest of the others is let go in turn.
    const back = await f.run({ action: "snapshot", tab_id: first.id }, "claude1");
    expect(back.error_code, back.result?.text).toBe("");
    expect(back.result?.text).toContain('button [e1] "Submit"');
    expect(f.views).toHaveLength(4);
    expect(f.views[3]!.webContents.url).toBe("http://localhost:3001/");
    expect(roster(f)).toEqual(["claude1", "codex1:asleep", "pi1"]);
    expect(f.views.filter((v) => !v.webContents.closed)).toHaveLength(2);
    f.browser.close();
  }
});

test("a page that was let go has no handles: only a snapshot brings it back", async () => {
  const f = fixture(undefined, frozen({ live: 1 }));
  const a = await openTab(f, "codex1");
  const snap = await f.run({ action: "snapshot", tab_id: a.id }, "codex1");
  await openTab(f, "claude1");
  expect(roster(f)).toEqual(["codex1:asleep", "claude1"]);
  for (const command of [
    { action: "click", tab_id: a.id, element_ref: "e1", snapshot_version: snap.tab!.snapshot_version },
    { action: "press", tab_id: a.id, key: "Enter" },
    { action: "scroll", tab_id: a.id, dy: 100 },
  ] as const) {
    const out = await f.run(command, "codex1");
    expect(out.error_code, command.action).toBe("stale_snapshot");
  }
  expect(f.views).toHaveLength(2);
  f.browser.close();
});

test("the sites a tab was allowed stay allowed when its page comes back", async () => {
  const f = fixture(async () => true, frozen({ live: 1 }));
  const a = await openTab(f, "codex1", "https://example.test/start");
  await openTab(f, "claude1");
  expect(roster(f)).toEqual(["codex1:asleep", "claude1"]);
  expect((await f.run({ action: "snapshot", tab_id: a.id }, "codex1")).error_code).toBe("");
  expect(f.origins).toEqual(["https://example.test"]);
  f.browser.close();
});

test("a page nobody touched for the idle time is let go; the person's own tab is not", async () => {
  let clock = 1000;
  const f = fixture(undefined, { now: () => clock, limits: { live: 8, perAgent: 6, idleMs: 60_000 } });
  await openTab(f, "codex1");
  await openTab(f, "claude1");
  await openTab(f, "");
  clock += 59_000;
  expect(f.browser.sweep()).toEqual([]);
  clock += 2_000;
  expect(f.browser.sweep()).toHaveLength(2);
  expect(roster(f)).toEqual(["codex1:asleep", "claude1:asleep", ""]);
  f.browser.close();
});

test("an agent keeps at most its share of tabs: another opens by closing its least recently used", async () => {
  const f = fixture(undefined, frozen({ perAgent: 2 }));
  const a = await openTab(f, "codex1");
  const b = await openTab(f, "codex1");
  const other = await openTab(f, "claude1");
  await f.run({ action: "snapshot", tab_id: a.id }, "codex1");
  const c = await f.run({ action: "open", url: "http://localhost:3000" }, "codex1");
  expect(c.error_code, c.result?.text).toBe("");
  expect(c.result?.text).toContain(`${b.id} was closed to make room`);
  expect(f.browser.state("deck", "room").tabs.map((t) => t.id)).toEqual([a.id, other.id, c.tab!.id]);
  f.browser.close();
});

test("only two snapshots run at once across all tabs", async () => {
  const f = fixture();
  const tabs = [];
  for (const owner of ["a1", "a2", "a3"]) tabs.push(await openTab(f, owner));
  let started = 0;
  const release: (() => void)[] = [];
  f.views.forEach((v, i) => {
    v.webContents.pause = () =>
      new Promise((resolve) => {
        started++;
        release.push(() => resolve({ nodes: v.webContents.nodes }));
      });
    void i;
  });
  const runs = tabs.map((t, i) => f.run({ action: "snapshot", tab_id: t.id }, `a${i + 1}`));
  await until(() => started === 2);
  await sleep(40);
  expect(started).toBe(2);
  release[0]!();
  await until(() => started === 3);
  release[1]!();
  release[2]!();
  for (const out of await Promise.all(runs)) expect(out.error_code).toBe("");
  f.browser.close();
});

test("a hung snapshot times out, gives up its permit and its tab, and the recovery ops run", async () => {
  const f = fixture();
  const tabs = [];
  for (const owner of ["a1", "a2", "a3"]) tabs.push(await openTab(f, owner));
  for (const v of f.views.slice(0, 2)) v.webContents.pause = () => new Promise(() => {});
  // Two hung snapshots would hold both permits for good.
  for (const [i, t] of tabs.slice(0, 2).entries()) {
    const request = f.request({ action: "snapshot", tab_id: t.id }, `a${i + 1}`);
    request.deadline_at = Date.now() + 60;
    expect((await f.browser.execute("deck", request)).error_code).toBe("timeout");
  }
  // Another tab still gets its snapshot, and the hung tab takes its recovery ops.
  expect((await f.run({ action: "snapshot", tab_id: tabs[2]!.id }, "a3")).error_code).toBe("");
  f.views[0]!.webContents.pause = null;
  expect((await f.run({ action: "reload", tab_id: tabs[0]!.id }, "a1")).error_code).toBe("");
  expect((await f.run({ action: "snapshot", tab_id: tabs[0]!.id }, "a1")).error_code).toBe("");
  expect((await f.run({ action: "close", tab_id: tabs[1]!.id }, "a2")).error_code).toBe("");
  f.browser.close();
});

test("a tab closed while its page is being made again does not keep a page", async () => {
  const f = fixture(undefined, frozen({ live: 2 }));
  const a = await openTab(f, "codex1");
  await openTab(f, "claude1");
  await openTab(f, "pi1");
  expect(roster(f)).toEqual(["codex1:asleep", "claude1", "pi1"]);
  f.browser.present({ deck_id: "deck", session_id: "room", tab_id: a.id, bounds: { x: 0, y: 0, w: 300, h: 300, zoom: 1 } });
  expect((await f.run({ action: "close", tab_id: a.id }, "codex1")).error_code).toBe("");
  await sleep(30);
  const left = f.browser.state("deck", "room").tabs;
  expect(left.map((t) => t.owner)).toEqual(["claude1", "pi1"]);
  // The closed tab left no page behind: what lives is what the tabs hold.
  expect(f.views.filter((v) => !v.webContents.closed)).toHaveLength(left.filter((t) => !t.asleep).length);
  f.browser.close();
});

// ---- what a page does, what an agent can do to it ----

test("a page nobody looks at is one hidden pixel, visible only while its agent works, in a window-sized viewport", async () => {
  const f = fixture(undefined, { restMs: 30 });
  const tab = await openTab(f, "codex1");
  const view = f.views[0]!;
  expect(view.bounds).toEqual({ x: 0, y: 0, width: 1, height: 1 });
  expect([view.webContents.throttled, view.webContents.muted]).toEqual([true, true]);
  // It rests a moment after its agent's last op, then is hidden again.
  await until(() => !view.visible);
  let hold!: () => void;
  view.webContents.pause = () =>
    new Promise((resolve) => {
      hold = () => resolve({ nodes: view.webContents.nodes });
    });
  const reading = f.run({ action: "snapshot", tab_id: tab.id }, "codex1");
  await until(() => hold !== undefined);
  expect(view.visible).toBe(true);
  expect(view.bounds).toEqual({ x: 0, y: 0, width: 1, height: 1 });
  expect(view.webContents.calls.find((c) => c.method === "Emulation.setDeviceMetricsOverride")?.params).toMatchObject({ width: 1280, height: 800 });
  hold();
  expect((await reading).error_code).toBe("");
  // The person looks at it: the panel's size, a viewport of its own, and no throttling.
  f.browser.present({ deck_id: "deck", session_id: "room", tab_id: tab.id, bounds: { x: 10, y: 20, w: 300, h: 400, zoom: 1 } });
  await sleep(10);
  expect([view.visible, view.webContents.throttled, view.webContents.muted]).toEqual([true, false, false]);
  expect(view.webContents.calls.some((c) => c.method === "Emulation.clearDeviceMetricsOverride")).toBe(true);
  f.browser.present({ deck_id: "deck", session_id: "room", tab_id: "", bounds: null });
  await until(() => !view.visible);
  expect([view.bounds, view.webContents.throttled]).toEqual([{ x: 0, y: 0, width: 1, height: 1 }, true]);
  f.browser.close();
});

test("an agent's tab says what it is doing, and then stops saying it", async () => {
  const f = fixture();
  const tab = await openTab(f, "codex1");
  const snap = await f.run({ action: "snapshot", tab_id: tab.id }, "codex1");
  let hold!: () => void;
  f.views[0]!.webContents.pause = () =>
    new Promise((resolve) => {
      hold = () => resolve({ nodes: f.views[0]!.webContents.nodes });
    });
  const working = f.run({ action: "snapshot", tab_id: tab.id }, "codex1");
  await until(() => hold !== undefined);
  expect(f.browser.state("deck", "room").tabs[0]!.activity).toBe("reading the page");
  hold();
  await working;
  void snap;
  f.states.length = 0;
  await f.run({ action: "click", tab_id: tab.id, element_ref: "e1", snapshot_version: "stale" }, "codex1");
  expect(f.states.some((s) => s.tabs[0]?.activity === "clicking e1")).toBe(true);
  f.browser.close();
});

test("keys, the wheel and clicks are let through only while a command sends them", async () => {
  const f = fixture();
  const tab = await openTab(f, "codex1");
  const w = f.views[0]!.webContents;
  const snap = await f.run({ action: "snapshot", tab_id: tab.id }, "codex1");
  const verdicts: string[] = [];
  const poke = (event: string, input: object) => {
    let prevented = false;
    w.emit(event, { preventDefault: () => (prevented = true) }, input);
    return prevented;
  };
  const send = w.debugger.sendCommand;
  w.debugger.sendCommand = async (method: string, params?: unknown) => {
    const p = params as { type?: string };
    if (method === "Input.dispatchKeyEvent") verdicts.push(`key:${poke("before-input-event", { type: p.type })}`);
    if (method === "Input.dispatchMouseEvent") {
      const asNative = { mousePressed: "mouseDown", mouseReleased: "mouseUp", mouseWheel: "mouseWheel" }[p.type ?? ""] ?? "";
      verdicts.push(`${asNative}:${poke("before-mouse-event", { type: asNative, x: 50, y: 20 })}`);
      // Something else reaching the page at that moment is stopped.
      verdicts.push(`other:${poke("before-mouse-event", { type: "mouseMove", x: 1, y: 1 })}`);
    }
    return send(method, params);
  };
  expect((await f.run({ action: "press", tab_id: tab.id, key: "Enter" }, "codex1")).error_code).toBe("");
  expect((await f.run({ action: "scroll", tab_id: tab.id, dy: 300 }, "codex1")).error_code).toBe("");
  // A key may change the page, so the click needs a snapshot of its own.
  const fresh = await f.run({ action: "snapshot", tab_id: tab.id }, "codex1");
  expect(snap.tab!.snapshot_version).not.toBe(fresh.tab!.snapshot_version);
  expect((await f.run({ action: "click", tab_id: tab.id, element_ref: "e1", snapshot_version: fresh.tab!.snapshot_version }, "codex1")).error_code).toBe("");
  expect(verdicts).toEqual(["key:false", "key:false", "mouseWheel:false", "other:true", "mouseDown:false", "other:true", "mouseUp:false", "other:true"]);
  // Between commands nothing is let through.
  expect(poke("before-input-event", { type: "keyDown" })).toBe(true);
  expect(poke("before-mouse-event", { type: "mouseDown", x: 50, y: 20 })).toBe(true);
  f.browser.close();
});

test("handles survive typing and scrolling, and are spent by a click or a navigation", async () => {
  const f = fixture();
  const tab = await openTab(f, "codex1");
  const snap = await f.run({ action: "snapshot", tab_id: tab.id }, "codex1");
  const v = snap.tab!.snapshot_version;
  expect((await f.run({ action: "type", tab_id: tab.id, element_ref: "e2", snapshot_version: v, text: "hi" }, "codex1")).error_code).toBe("");
  expect((await f.run({ action: "scroll", tab_id: tab.id, dy: 100 }, "codex1")).error_code).toBe("");
  expect((await f.run({ action: "select", tab_id: tab.id, element_ref: "e1", snapshot_version: v, text: "x" }, "codex1")).error_code).toBe("");
  expect((await f.run({ action: "click", tab_id: tab.id, element_ref: "e1", snapshot_version: v }, "codex1")).error_code).toBe("stale_snapshot");
  f.browser.close();
});

test("a screenshot comes back downscaled, as an image", async () => {
  const f = fixture();
  const tab = await openTab(f, "codex1");
  const shot = await f.run({ action: "screenshot", tab_id: tab.id }, "codex1");
  expect(shot.error_code, shot.result?.text).toBe("");
  expect(shot.result?.images[0]).toMatchObject({ mime_type: "image/jpeg" });
  expect(Buffer.from(shot.result!.images[0]!.data).toString()).toBe("jpeg bytes");
  f.browser.close();
});

test("a dialog the page opens is closed for the agent, who hears of it", async () => {
  const f = fixture();
  const tab = await openTab(f, "codex1");
  const w = f.views[0]!.webContents;
  w.debugger.emit("message", {}, "Page.javascriptDialogOpening", { type: "confirm", message: "Delete everything?" });
  const handled = w.calls.find((c) => c.method === "Page.handleJavaScriptDialog");
  expect(handled?.params).toEqual({ accept: false });
  const next = await f.run({ action: "snapshot", tab_id: tab.id }, "codex1");
  expect(next.result?.text).toContain('note: the page opened a dialog (confirm): "Delete everything?"');
  f.browser.close();
});

test("fields the engine sends as zero values ask for nothing", async () => {
  const f = fixture();
  const tab = await openTab(f, "codex1");
  // The engine's commands carry every field; the ones not given are zero.
  const zero = { interactive: false, max_lines: 0, dy: 0, to: "", text: "", load: false, ms: 0, timeout_s: 0, element_ref: "", snapshot_version: "" };
  const snap = await f.run({ ...zero, action: "snapshot", tab_id: tab.id } as BrowserCommand, "codex1");
  expect(snap.result?.text).toContain('button [e1] "Submit"');
  expect((await f.run({ ...zero, action: "scroll", tab_id: tab.id, to: "bottom" } as BrowserCommand, "codex1")).error_code).toBe("");
  expect((await f.run({ ...zero, action: "wait", tab_id: tab.id, load: true } as BrowserCommand, "codex1")).error_code).toBe("");
  expect((await f.run({ ...zero, action: "screenshot", tab_id: tab.id } as BrowserCommand, "codex1")).error_code).toBe("");
  f.browser.close();
});
