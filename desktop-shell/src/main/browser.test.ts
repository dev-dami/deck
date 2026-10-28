import { describe, expect, test } from "vitest";
import type { BrowserBounds, BrowserState } from "../shared/bridge";
import { type AgentRequest, Browser, type BrowserPage, HEAVY_AT_ONCE, type PageFactory, type PageHooks } from "./browser";
import type { AxNode } from "./outline";
import type { Throttle } from "./tabs";

// A page the tests drive: an address, a scripted accessibility tree and a
// log of what the browser asked of it over the protocol.
class FakeCdp {
  calls: [string, Record<string, unknown>][] = [];
  removed = new Set<number>();
  delay = 0;
  inflight = 0;
  peak = 0;
  constructor(public nodes: AxNode[]) {}

  async send(method: string, params: Record<string, unknown> = {}): Promise<unknown> {
    this.calls.push([method, params]);
    this.inflight++;
    this.peak = Math.max(this.peak, this.inflight);
    if (this.delay > 0 && (method === "Accessibility.getFullAXTree" || method === "Page.captureScreenshot")) await new Promise((r) => setTimeout(r, this.delay));
    this.inflight--;
    switch (method) {
      case "Accessibility.getFullAXTree":
        return { nodes: this.nodes };
      case "DOM.getDocument":
        return { root: { nodeId: 1 } };
      case "DOM.querySelectorAll":
        return { nodeIds: [] };
      case "DOM.resolveNode":
        if (this.removed.has(params["backendNodeId"] as number)) throw new Error("No node with given id found");
        return { object: { objectId: `o${params["backendNodeId"]}` } };
      case "Runtime.callFunctionOn": {
        const decl = String(params["functionDeclaration"]);
        const id = Number(String(params["objectId"]).slice(1));
        if (decl.includes("isConnected")) return { result: { value: !this.removed.has(id) } };
        if (decl.includes("elementFromPoint")) return { result: { value: "" } };
        return { result: { value: true } };
      }
      case "DOM.getContentQuads":
        return { quads: [[10, 10, 90, 10, 90, 40, 10, 40]] };
      case "Page.getLayoutMetrics":
        return { cssVisualViewport: { clientWidth: 1280, clientHeight: 800 } };
      case "Page.captureScreenshot":
        return { data: Buffer.from("jpegbytes").toString("base64") };
      case "Runtime.evaluate":
        return { result: { value: String(params["expression"]).includes("innerText") ? true : { y: 0, max: 0 } } };
      default:
        return {};
    }
  }
}

const tree = (...buttons: [id: number, name: string][]): AxNode[] => [
  { nodeId: "1", role: { value: "RootWebArea" }, name: { value: "Fixture" }, childIds: buttons.map((_, i) => String(i + 2)) },
  ...buttons.map(
    ([id, name], i): AxNode => ({ nodeId: String(i + 2), parentId: "1", role: { value: "button" }, name: { value: name }, backendDOMNodeId: id, childIds: [] }),
  ),
];

class FakePage implements BrowserPage {
  cdp: FakeCdp;
  url_ = "";
  title_ = "";
  n = 0;
  loads: string[] = [];
  awake_: boolean[] = [];
  placed: (BrowserBounds | null)[] = [];
  throttle: Throttle | null = null;
  destroyed = false;
  canBack = false;
  failNext: string | null = null;
  constructor(
    readonly hooks: PageHooks,
    nodes: AxNode[],
  ) {
    this.cdp = new FakeCdp(nodes);
  }
  info() {
    return { url: this.url_, title: this.title_, loading: false, canBack: this.canBack, canForward: false };
  }
  url = () => this.url_;
  title = () => this.title_;
  loading = () => false;
  epoch = () => this.n;
  async navigate(url: string) {
    if (this.failNext !== null) {
      const error = this.failNext;
      this.failNext = null;
      return { ok: false, error };
    }
    this.loads.push(url);
    this.url_ = url;
    this.title_ = `Page ${this.loads.length}`;
    this.n++;
    this.hooks.changed();
    return { ok: true };
  }
  async history(dir: string) {
    if (dir === "back" && !this.canBack) return false;
    this.n++;
    return true;
  }
  async whenLoaded() {
    return true;
  }
  place(b: BrowserBounds | null) {
    this.placed.push(b);
  }
  async awake(on: boolean) {
    this.awake_.push(on);
  }
  apply(t: Throttle) {
    this.throttle = t;
  }
  destroy() {
    this.destroyed = true;
  }
}

function setup(opts: { limits?: Partial<import("./tabs").Limits>; nodes?: AxNode[] } = {}) {
  const pages: FakePage[] = [];
  const states: BrowserState[] = [];
  let now = 1_000_000;
  const factory: PageFactory = {
    create(_tab, hooks) {
      const p = new FakePage(hooks, opts.nodes ?? tree([11, "Go"], [12, "Stop"]));
      pages.push(p);
      return p;
    },
  };
  const b = new Browser({ factory, send: (s) => states.push(s), now: () => now, ...(opts.limits === undefined ? {} : { limits: { live: 8, perAgent: 6, total: 40, idleMs: 300_000, ...opts.limits } }) });
  let id = 0;
  const call = (agent: string, op: string, args: Record<string, unknown> = {}, extra: Partial<AgentRequest> = {}) =>
    b.handle("d1", {
      id: String(++id),
      agent,
      name: agent.replace(/\d+$/, ""),
      kind: "claude",
      op,
      args: JSON.stringify(args),
      free: false,
      allowed: [],
      timeout_ms: 5000,
      ...extra,
    });
  return { b, pages, states, call, tick: (ms: number) => (now += ms) };
}

const URL_A = "https://a.example/start";

describe("opening", () => {
  test("an agent's open makes its own tab and loads the page", async () => {
    const { b, pages, call } = setup();
    const r = await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    expect(r).toMatchObject({ isError: false });
    expect(r.text).toContain("opened t1: Page 1  https://a.example/start");
    expect(pages).toHaveLength(1);
    expect(pages[0]!.loads).toEqual([URL_A]);
    expect(b.state().tabs).toMatchObject([{ id: "t1", deck: "d1", owner: { agent: "claude1", name: "claude", kind: "claude" }, live: true, url: URL_A }]);
  });

  test("open again reuses the agent's tab, new_tab makes another, and a second agent gets its own", async () => {
    const { b, call } = setup();
    const ok = { allowed: ["https://a.example", "https://b.example"] };
    await call("claude1", "open", { url: URL_A }, ok);
    await call("claude1", "open", { url: "https://a.example/two" }, ok);
    expect(b.state().tabs.map((t) => t.id)).toEqual(["t1"]);
    await call("claude1", "open", { url: "https://b.example/", new_tab: true }, ok);
    await call("codex1", "open", { url: URL_A }, ok);
    expect(b.state().tabs.map((t) => [t.id, t.owner?.agent])).toEqual([
      ["t1", "claude1"],
      ["t2", "claude1"],
      ["t3", "codex1"],
    ]);
  });

  test("only http(s) pages open, and a site the person did not allow is refused again here", async () => {
    const { call } = setup();
    for (const url of ["file:///etc/passwd", "chrome://settings", "javascript:alert(1)", "https://u:p@a.example/", "devtools://x"]) {
      const r = await call("claude1", "open", { url }, { free: true });
      expect(r.isError, url).toBe(true);
    }
    const no = await call("claude1", "open", { url: "https://evil.example/" });
    expect(no).toMatchObject({ isError: true });
    expect(no.text).toContain("not a site the user has allowed");
    // This computer's own pages need no word; yolo opens any.
    expect((await call("claude1", "open", { url: "http://localhost:5173/" })).isError).toBe(false);
    expect((await call("claude2", "open", { url: "https://anywhere.example/" }, { free: true })).isError).toBe(false);
  });

  test("a page that fails to load is reported with the reason", async () => {
    const { pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    pages[0]!.failNext = "net::ERR_NAME_NOT_RESOLVED";
    const r = await call("claude1", "open", { url: "https://a.example/gone" }, { allowed: ["https://a.example"] });
    expect(r).toMatchObject({ isError: true });
    expect(r.text).toContain("net::ERR_NAME_NOT_RESOLVED");
  });

  test("the person's open uses their latest tab and refuses what is not a web page", () => {
    const { b } = setup();
    expect(b.open("d1", "localhost:3000/app")).toEqual({ ok: true, tab: "t1" });
    expect(b.open("d1", "https://example.com/")).toEqual({ ok: true, tab: "t1" });
    expect(b.open("d1", "javascript:alert(1)")).toMatchObject({ ok: false });
    expect(b.open("d1", "file:///etc/passwd")).toMatchObject({ ok: false });
    expect(b.open("d1", "https://x.example/", "t99")).toMatchObject({ ok: false });
    expect(b.state().tabs).toMatchObject([{ id: "t1", owner: null, url: "https://example.com/" }]);
  });
});

describe("ownership", () => {
  test("an agent acts only on its own tabs", async () => {
    const { b, call } = setup();
    const ok = { allowed: ["https://a.example"] };
    await call("claude1", "open", { url: URL_A }, ok);
    await call("codex1", "open", { url: URL_A }, ok);
    const mine = b.open("d1", "http://localhost:3000/");
    expect(mine).toMatchObject({ ok: true });
    for (const tab of ["t1", mine.ok ? mine.tab : ""]) {
      const r = await call("codex1", "snapshot", { tab });
      expect(r.isError).toBe(true);
      expect(r.text).toContain("you can act only on your own");
    }
    expect((await call("codex1", "snapshot", { tab: "t9" })).text).toContain("There is no tab t9");
    expect((await call("codex1", "snapshot", { tab: "t2" })).isError).toBe(false);
    // Closing someone else's tab is the same refusal.
    expect((await call("codex1", "close", { tab: "t1" })).isError).toBe(true);
    expect(b.state().tabs).toHaveLength(3);
  });

  test("an agent with no tab is told to open one", async () => {
    const { call } = setup();
    const r = await call("claude1", "snapshot");
    expect(r).toMatchObject({ isError: true });
    expect(r.text).toContain("browser.open");
  });

  test("tabs lists everyone's tabs, saying whose", async () => {
    const { b, call } = setup();
    const ok = { allowed: ["https://a.example"] };
    await call("claude1", "open", { url: URL_A }, ok);
    await call("codex1", "open", { url: URL_A }, ok);
    b.open("d1", "http://localhost:3000/");
    const text = (await call("claude1", "tabs")).text;
    expect(text.split("\n").map((l) => l.split(")")[0])).toEqual(["t1 (you", "t2 (@codex", "t3 (user"]);
    expect(text).toContain("t1 (you) Page 1  https://a.example/start");
  });
});

describe("taking over", () => {
  test("a tab the person took over refuses its agent until it is handed back", async () => {
    const { b, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    b.pause("t1", true);
    expect(b.state().tabs[0]!.paused).toBe(true);
    for (const op of ["snapshot", "open"]) {
      const r = await call("claude1", op, { url: URL_A }, { allowed: ["https://a.example"] });
      expect(r.isError, op).toBe(true);
      expect(r.text).toContain("took over t1");
    }
    b.pause("t1", false);
    expect((await call("claude1", "snapshot")).isError).toBe(false);
    // The person's own tab cannot be paused (it is not an agent's).
    const mine = b.open("d1", "http://localhost:3000/");
    if (mine.ok) b.pause(mine.tab, true);
    expect(b.state().tabs.find((t) => t.owner === null)!.paused).toBe(false);
  });

  test("an op queued behind a running one stops if the tab is taken over meanwhile", async () => {
    const { b, pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    pages[0]!.cdp.delay = 40;
    const first = call("claude1", "snapshot");
    const second = call("claude1", "snapshot");
    await new Promise((r) => setTimeout(r, 10));
    b.pause("t1", true);
    expect((await first).isError).toBe(false);
    expect((await second).text).toContain("took over");
  });
});

describe("limits", () => {
  test("an agent at its tab limit gets its oldest tab closed, and is told", async () => {
    const { b, call, tick } = setup({ limits: { perAgent: 2 } });
    const ok = { allowed: ["https://a.example"] };
    await call("claude1", "open", { url: "https://a.example/1" }, ok);
    tick(10);
    await call("claude1", "open", { url: "https://a.example/2", new_tab: true }, ok);
    tick(10);
    const r = await call("claude1", "open", { url: "https://a.example/3", new_tab: true }, ok);
    expect(r.text).toContain("t1 was closed to make room");
    expect(b.state().tabs.map((t) => t.id)).toEqual(["t2", "t3"]);
  });

  test("with more agents than live pages the oldest idle pages are discarded and come back on use", async () => {
    const { b, pages, call, tick } = setup({ limits: { live: 3 } });
    const ok = { allowed: ["https://a.example"] };
    for (let i = 1; i <= 6; i++) {
      tick(1000);
      await call(`agent${i}`, "open", { url: `https://a.example/${i}` }, ok);
    }
    expect(b.state().tabs.filter((t) => t.live)).toHaveLength(3);
    expect(b.state().tabs.map((t) => t.live)).toEqual([false, false, false, true, true, true]);
    expect(pages.filter((p) => p.destroyed)).toHaveLength(3);
    // A discarded tab keeps its address, and loads again when its agent returns.
    expect(b.state().tabs[0]).toMatchObject({ url: "https://a.example/1", live: false });
    const before = pages.length;
    const r = await call("agent1", "snapshot");
    expect(r.isError).toBe(false);
    expect(pages.length).toBe(before + 1);
    expect(pages.at(-1)!.loads).toEqual(["https://a.example/1"]);
    expect(b.state().tabs.filter((t) => t.live)).toHaveLength(3);
  });

  test("idle pages are discarded by the sweep", async () => {
    const { b, call, tick } = setup({ limits: { idleMs: 60_000 } });
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    tick(30_000);
    expect(b.sweep()).toEqual([]);
    tick(40_000);
    expect(b.sweep()).toEqual(["t1"]);
    expect(b.state().tabs[0]).toMatchObject({ live: false, url: URL_A });
  });

  test("snapshots and pictures take turns across tabs; one tab's ops run one at a time", async () => {
    const { b, pages, call } = setup();
    const ok = { allowed: ["https://a.example"] };
    for (let i = 1; i <= 6; i++) await call(`agent${i}`, "open", { url: URL_A }, ok);
    for (const p of pages) p.cdp.delay = 25;
    // Six agents snapshot at once: at most HEAVY_AT_ONCE reach the pages together.
    let live = 0;
    let peak = 0;
    for (const p of pages) {
      const send = p.cdp.send.bind(p.cdp);
      p.cdp.send = async (m, params) => {
        if (m === "Accessibility.getFullAXTree") {
          peak = Math.max(peak, ++live);
          try {
            return await send(m, params);
          } finally {
            live--;
          }
        }
        return send(m, params);
      };
    }
    const results = await Promise.all([1, 2, 3, 4, 5, 6].map((i) => call(`agent${i}`, "snapshot")));
    expect(results.every((r) => !r.isError)).toBe(true);
    expect(peak).toBeLessThanOrEqual(HEAVY_AT_ONCE);
    expect(peak).toBeGreaterThan(0);
    // Ops on one tab run in order, one at a time.
    const p = pages[0]!;
    p.cdp.calls.length = 0;
    await Promise.all([call("agent1", "snapshot"), call("agent1", "snapshot"), call("agent1", "snapshot")]);
    expect(p.cdp.peak).toBe(1);
    expect(b.state().tabs).toHaveLength(6);
  });

  test("a hung page times the op out and the next op still runs", async () => {
    const { pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    pages[0]!.cdp.delay = 400;
    const slow = await call("claude1", "snapshot", {}, { timeout_ms: 1000 });
    expect(slow.isError).toBe(false);
    pages[0]!.cdp.delay = 2500;
    const hung = await call("claude1", "snapshot", {}, { timeout_ms: 1000 });
    expect(hung).toMatchObject({ isError: true });
    expect(hung.text).toContain("timed out");
  }, 10_000);
});

describe("refs", () => {
  test("a ref from the snapshot works, and fails clearly once stale", async () => {
    const { pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    const snap = await call("claude1", "snapshot");
    expect(snap.text).toBe(`[t1] Page 1  ${URL_A}\nbutton [e1] "Go"\nbutton [e2] "Stop"`);
    const ok = await call("claude1", "click", { ref: "e1" });
    expect(ok).toMatchObject({ isError: false, text: "clicked e1." });
    const types = pages[0]!.cdp.calls.filter(([m]) => m === "Input.dispatchMouseEvent").map(([, p]) => p["type"]);
    expect(types).toEqual(["mouseMoved", "mousePressed", "mouseReleased"]);

    // A ref the snapshot never gave.
    expect((await call("claude1", "click", { ref: "e9" })).text).toContain("not in the latest snapshot");

    // The element left the page.
    pages[0]!.cdp.removed.add(12);
    const gone = await call("claude1", "click", { ref: "e2" });
    expect(gone).toMatchObject({ isError: true });
    expect(gone.text).toContain("e2 is stale");

    // The page got a new document: every ref is gone, and says why.
    pages[0]!.n++;
    const moved = await call("claude1", "click", { ref: "e1" });
    expect(moved.isError).toBe(true);
    expect(moved.text).toContain("loaded a new document");
    expect(moved.text).toContain("browser.snapshot");
    // A new snapshot starts the refs again.
    pages[0]!.cdp.removed.clear();
    expect((await call("claude1", "snapshot")).text).toContain("button [e1] \"Go\"");
    expect((await call("claude1", "click", { ref: "e1" })).isError).toBe(false);
  });

  test("a ref keeps its number when the page changes around it", async () => {
    const { pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    await call("claude1", "snapshot");
    pages[0]!.cdp.nodes = tree([20, "New"], [11, "Go"], [12, "Stop"]);
    const text = (await call("claude1", "snapshot")).text;
    expect(text).toContain('button [e3] "New"');
    expect(text).toContain('button [e1] "Go"');
  });
});

describe("what the agent hears", () => {
  test("typing is never echoed", async () => {
    const { call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    await call("claude1", "snapshot");
    const r = await call("claude1", "type", { ref: "e1", text: "hunter2-secret", submit: true });
    expect(r.isError).toBe(false);
    expect(r.text).toBe("typed 14 characters into e1 and pressed Enter.");
    expect(r.text).not.toContain("hunter2");
  });

  test("a page kept from an unapproved site is reported with the next answer", async () => {
    const { pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    pages[0]!.hooks.blocked("https://tracker.example/x");
    const r = await call("claude1", "snapshot");
    expect(r.text).toContain("tried to go to https://tracker.example/x");
    expect(r.text).toContain("browser.open {url}");
    expect((await call("claude1", "snapshot")).text).not.toContain("tracker");
  });

  test("the page's own notes (a dialog) arrive once", async () => {
    const { pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    pages[0]!.hooks.note("the page opened a dialog (alert): \"hi\"; Deck closed it.");
    expect((await call("claude1", "snapshot")).text).toContain("note: the page opened a dialog");
    expect((await call("claude1", "snapshot")).text).not.toContain("dialog");
  });

  test("a screenshot comes back as an image", async () => {
    const { call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    const r = await call("claude1", "screenshot");
    expect(r.isError).toBe(false);
    expect(Buffer.from(r.image!).toString()).toBe("jpegbytes");
    expect(r.mime).toBe("image/jpeg");
    expect(r.text).toMatch(/^screenshot of t1 \(1024x640, 0 KB\)$/);
  });

  test("bad arguments and unknown ops are answered, not thrown", async () => {
    const { b, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    expect((await call("claude1", "frobnicate")).text).toContain("not an op");
    const bad = await b.handle("d1", { id: "x", agent: "claude1", name: "claude", kind: "claude", op: "tabs", args: "[1]", free: false, allowed: [], timeout_ms: 1000 });
    expect(bad).toMatchObject({ isError: true });
    expect((await call("claude1", "press", { key: "Hyper+Q" })).text).toContain("not a key");
  });

  test("an agent that has the page wake it for the op and let it rest after", async () => {
    const { pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    await call("claude1", "snapshot");
    expect(pages[0]!.awake_).toEqual([true, false, true, false]);
  });
});

describe("the strip", () => {
  test("shows what an agent is doing for a moment, and the window hears of every change", async () => {
    const { b, call, states, tick } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    await call("claude1", "snapshot");
    await new Promise((r) => setTimeout(r, 60));
    expect(states.at(-1)!.tabs[0]!.doing).toBe("reading the page");
    tick(5000);
    b.sweep();
    await new Promise((r) => setTimeout(r, 60));
    expect(states.at(-1)!.tabs[0]!.doing).toBeNull();
  });

  test("showing a tab lays its page over the window; hiding parks it; closing a Deck closes its tabs", async () => {
    const { b, pages, call } = setup();
    await call("claude1", "open", { url: URL_A }, { allowed: ["https://a.example"] });
    b.show("t1", { x: 1, y: 2, w: 3, h: 4 });
    expect(pages[0]!.placed.at(-1)).toEqual({ x: 1, y: 2, w: 3, h: 4 });
    expect(pages[0]!.throttle).toMatchObject({ visible: true });
    expect(b.state().shown).toBe("t1");
    b.hide();
    expect(pages[0]!.placed.at(-1)).toBeNull();
    expect(pages[0]!.throttle).toMatchObject({ visible: false });
    b.closeDeck("d1");
    expect(b.state().tabs).toEqual([]);
    expect(pages[0]!.destroyed).toBe(true);
  });
});
