import { describe, expect, test } from "vitest";
import type { BrowserOwner } from "../shared/bridge";
import { HIDDEN, LIMITS, type Limits, pickDiscards, type Page, SHOWN, type Tab, TabLimitError, TabManager, throttleFor } from "./tabs";

class FakePage implements Page {
  throttle = HIDDEN;
  destroyed = false;
  apply(t: typeof HIDDEN) {
    this.throttle = t;
  }
  destroy() {
    this.destroyed = true;
  }
}

const owner = (agent: string): BrowserOwner => ({ agent, name: agent.replace(/\d+$/, ""), kind: "claude" });

function setup(limits: Partial<Limits> = {}) {
  let t = 1_000_000;
  const made: FakePage[] = [];
  let changes = 0;
  const m = new TabManager<FakePage>(
    () => {
      const p = new FakePage();
      made.push(p);
      return p;
    },
    () => changes++,
    () => t,
    { ...LIMITS, ...limits },
  );
  return { m, made, tick: (ms: number) => (t += ms), changes: () => changes };
}

describe("ownership", () => {
  test("tabs belong to the agent that opened them, the person's have no owner", () => {
    const { m } = setup();
    const a = m.open("d1", owner("claude1"), "https://a.example/").tab;
    const b = m.open("d1", owner("codex1"), "https://b.example/").tab;
    const mine = m.open("d1", null, "http://localhost:3000/").tab;
    const other = m.open("d2", owner("claude1"), "https://c.example/").tab;
    expect(m.ownedBy("d1", "claude1")).toEqual([a]);
    expect(m.ownedBy("d1", "codex1")).toEqual([b]);
    expect(m.list("d1").map((t) => t.id)).toEqual([a.id, b.id, mine.id]);
    expect(mine.owner).toBeNull();
    // Another Deck's agent of the same id is another agent.
    expect(m.ownedBy("d1", "claude1")).not.toContain(other);
    expect(new Set([a.id, b.id, mine.id, other.id]).size).toBe(4);
  });

  test("the latest tab of an agent is the one it used last", () => {
    const { m, tick } = setup();
    const a = m.open("d1", owner("claude1"), "https://a.example/").tab;
    tick(10);
    const b = m.open("d1", owner("claude1"), "https://b.example/").tab;
    expect(m.latestOf("d1", "claude1")).toBe(b);
    tick(10);
    m.touch(a);
    expect(m.latestOf("d1", "claude1")).toBe(a);
  });
});

describe("limits", () => {
  test("an agent at its limit loses its least recently used tab, never a busy or shown one", () => {
    const { m, tick } = setup({ perAgent: 3 });
    const t1 = m.open("d1", owner("a1"), "https://1.example/").tab;
    tick(5);
    const t2 = m.open("d1", owner("a1"), "https://2.example/").tab;
    tick(5);
    m.open("d1", owner("a1"), "https://3.example/");
    tick(5);
    m.begin(t1, "clicking e1"); // t1 is the oldest but busy
    const r = m.open("d1", owner("a1"), "https://4.example/");
    expect(r.closed.map((t) => t.id)).toEqual([t2.id]);
    expect(m.get(t2.id)).toBeUndefined();
    expect(m.ownedBy("d1", "a1")).toHaveLength(3);
    expect(m.get(t1.id)).toBeDefined();
    // Other agents are not touched by it.
    const b = m.open("d1", owner("b1"), "https://b.example/");
    expect(b.closed).toEqual([]);
  });

  test("an agent whose tabs are all busy is refused, not made to wait", () => {
    const { m } = setup({ perAgent: 2 });
    for (const u of ["https://1.example/", "https://2.example/"]) m.begin(m.open("d1", owner("a1"), u).tab, "x");
    expect(() => m.open("d1", owner("a1"), "https://3.example/")).toThrow(TabLimitError);
  });

  test("the browser as a whole holds only so many tabs", () => {
    const { m } = setup({ total: 3, perAgent: 9 });
    for (const a of ["a1", "a2", "a3"]) m.open("d1", owner(a), "https://x.example/");
    expect(() => m.open("d1", owner("a4"), "https://x.example/")).toThrow(/3 tabs/);
    expect(() => m.open("d1", null, "http://localhost:1/")).toThrow(TabLimitError);
  });
});

describe("live pages", () => {
  test("only `live` pages stay alive; the least recently used are discarded, their address kept", () => {
    const { m, made, tick } = setup({ live: 3 });
    const tabs: Tab<FakePage>[] = [];
    for (let i = 0; i < 6; i++) {
      tick(100);
      const t = m.open("d1", owner(`a${i}`), `https://${i}.example/`).tab;
      t.title = `Tab ${i}`;
      tabs.push(t);
    }
    const alive = tabs.filter((t) => t.page !== null).map((t) => t.url);
    expect(alive).toEqual(["https://3.example/", "https://4.example/", "https://5.example/"]);
    expect(made.filter((p) => p.destroyed)).toHaveLength(3);
    // The discarded keep what the strip shows.
    expect(tabs[0]).toMatchObject({ url: "https://0.example/", title: "Tab 0", page: null });
    expect(m.list()).toHaveLength(6);
  });

  test("a discarded tab gets a page again on use, and pushes out the oldest live one", () => {
    const { m, tick } = setup({ live: 2 });
    const a = m.open("d1", owner("a1"), "https://a.example/").tab;
    tick(10);
    const b = m.open("d1", owner("a2"), "https://b.example/").tab;
    tick(10);
    const c = m.open("d1", owner("a3"), "https://c.example/").tab;
    expect(a.page).toBeNull();
    tick(10);
    expect(m.live(a)).not.toBeNull();
    expect(b.page).toBeNull(); // the oldest of the live ones
    expect(c.page).not.toBeNull();
    expect(a.page).not.toBeNull();
  });

  test("a tab with an op running or on show is never discarded", () => {
    const { m, tick } = setup({ live: 1 });
    const a = m.open("d1", owner("a1"), "https://a.example/").tab;
    tick(10);
    m.begin(a, "reading the page");
    const b = m.open("d1", owner("a2"), "https://b.example/").tab;
    // a is busy, b is the one being made: both stay though the limit is 1.
    expect(a.page).not.toBeNull();
    expect(b.page).not.toBeNull();
    m.end(a);
    tick(10);
    m.show(b.id);
    expect(m.sweep()).toEqual([a.id]);
    expect(b.page).not.toBeNull();
  });

  test("a tab nobody touched for the idle time is discarded by the sweep", () => {
    const { m, tick } = setup({ idleMs: 60_000 });
    const a = m.open("d1", owner("a1"), "https://a.example/").tab;
    tick(30_000);
    const b = m.open("d1", owner("a2"), "https://b.example/").tab;
    tick(40_000);
    expect(m.sweep()).toEqual([a.id]);
    expect(a.page).toBeNull();
    expect(b.page).not.toBeNull();
    m.touch(b);
    tick(59_000);
    expect(m.sweep()).toEqual([]);
    tick(2_000);
    expect(m.sweep()).toEqual([b.id]);
  });

  test("closing a tab destroys its page and forgets it", () => {
    const { m, made } = setup();
    const a = m.open("d1", owner("a1"), "https://a.example/").tab;
    m.show(a.id);
    m.close(a.id);
    expect(made[0]!.destroyed).toBe(true);
    expect(m.get(a.id)).toBeUndefined();
    expect(m.shown).toBeNull();
  });

  test("a page that cannot be made (no window) leaves the tab asleep", () => {
    const m = new TabManager<FakePage>(() => null);
    const t = m.open("d1", owner("a1"), "https://a.example/").tab;
    expect(t.page).toBeNull();
    expect(m.live(t)).toBeNull();
  });
});

describe("throttling", () => {
  test("only the tab being looked at runs free; the rest are hidden, throttled and muted", () => {
    expect(throttleFor(true)).toEqual({ visible: true, throttling: false, muted: false, frameRate: 60 });
    expect(throttleFor(false)).toEqual({ visible: false, throttling: true, muted: true, frameRate: 1 });
    const { m, made } = setup();
    const a = m.open("d1", owner("a1"), "https://a.example/").tab;
    const b = m.open("d1", owner("a2"), "https://b.example/").tab;
    expect(made.map((p) => p.throttle)).toEqual([HIDDEN, HIDDEN]);
    m.show(a.id);
    expect(a.page!.throttle).toBe(SHOWN);
    expect(b.page!.throttle).toBe(HIDDEN);
    m.show(b.id);
    expect(a.page!.throttle).toBe(HIDDEN);
    expect(b.page!.throttle).toBe(SHOWN);
    m.show(null);
    expect(b.page!.throttle).toBe(HIDDEN);
  });

  test("a page made for a shown tab starts shown", () => {
    const { m } = setup({ live: 1 });
    const a = m.open("d1", owner("a1"), "https://a.example/").tab;
    m.show(a.id);
    m.discard(a.id);
    expect(m.live(a)!.throttle).toBe(SHOWN);
  });
});

describe("what an agent is doing", () => {
  test("shows while an op runs and for a moment after", () => {
    const { m, tick } = setup();
    const a = m.open("d1", owner("a1"), "https://a.example/").tab;
    m.begin(a, "clicking e12");
    m.begin(a, "reading the page");
    m.end(a);
    expect(a.doing).toBe("reading the page");
    tick(10_000);
    expect(m.settle()).toBe(false); // one op still runs
    m.end(a);
    tick(2_000);
    expect(m.settle()).toBe(false);
    expect(a.doing).not.toBeNull();
    tick(1_000);
    expect(m.settle()).toBe(true);
    expect(a.doing).toBeNull();
  });
});

describe("pickDiscards", () => {
  const c = (id: string, lastUsed: number, over: Partial<{ alive: boolean; shown: boolean; busy: boolean }> = {}) => ({
    id,
    alive: true,
    lastUsed,
    shown: false,
    busy: false,
    ...over,
  });
  const l = { live: 2, perAgent: 6, total: 40, idleMs: 1000 };

  test("picks idle pages, then the oldest past the limit", () => {
    expect(pickDiscards([c("a", 0), c("b", 2500), c("c", 2600), c("d", 2700)], 3000, l)).toEqual(["a", "b"]);
    expect(pickDiscards([c("a", 2500), c("b", 2600), c("c", 2700)], 3000, l)).toEqual(["a"]);
    expect(pickDiscards([c("a", 2500), c("b", 2600)], 3000, l)).toEqual([]);
  });

  test("leaves shown and busy pages and the asleep alone", () => {
    expect(pickDiscards([c("a", 0, { shown: true }), c("b", 0, { busy: true }), c("c", 0, { alive: false }), c("d", 0)], 9000, l)).toEqual(["d"]);
    // Over the limit with nothing free to pick: nothing is picked.
    expect(pickDiscards([c("a", 2900, { shown: true }), c("b", 2900, { busy: true }), c("c", 2900, { busy: true })], 3000, l)).toEqual([]);
  });
});
