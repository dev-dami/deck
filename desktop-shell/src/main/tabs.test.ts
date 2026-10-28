import { describe, expect, test } from "vitest";
import { type Candidate, Gate, type Limits, pickDiscards, pickEviction } from "./tabs";

const L: Limits = { live: 2, perAgent: 3, idleMs: 60_000 };
const tab = (id: string, seq: number, over: Partial<Candidate> = {}): Candidate => ({ id, alive: true, lastUsed: 1000, seq, shown: false, busy: false, ...over });

describe("pickDiscards", () => {
  test("keeps pages within the live budget by letting go the least recently used", () => {
    const tabs = [tab("a", 1), tab("b", 2), tab("c", 3), tab("d", 4)];
    expect(pickDiscards(tabs, 1000, L).sort()).toEqual(["a", "b"]);
    expect(pickDiscards(tabs.slice(0, 2), 1000, L)).toEqual([]);
  });

  test("goes by the order of use, never by the clock: tabs used in one millisecond are not a tie", () => {
    // Every tab shares one timestamp, as ops landing in one millisecond do.
    const used = [tab("claude1", 5), tab("codex1", 6), tab("pi1", 7)];
    for (let n = 0; n < 50; n++) {
      const shuffled = [...used].sort(() => Math.random() - 0.5);
      expect(pickDiscards(shuffled, 1000, L)).toEqual(["claude1"]);
    }
    // And the one used again is the one that stays.
    const again = [tab("claude1", 8), tab("codex1", 6), tab("pi1", 7)];
    expect(pickDiscards(again, 1000, L)).toEqual(["codex1"]);
  });

  test("lets go of a page nobody touched for the idle time, whatever the budget", () => {
    const tabs = [tab("old", 1, { lastUsed: 0 }), tab("new", 2, { lastUsed: 100_000 })];
    expect(pickDiscards(tabs, 100_001, L)).toEqual(["old"]);
  });

  test("never lets go of the page being looked at, or one at work, or one already gone", () => {
    const tabs = [tab("shown", 1, { shown: true, lastUsed: 0 }), tab("busy", 2, { busy: true, lastUsed: 0 }), tab("gone", 3, { alive: false, lastUsed: 0 }), tab("x", 4), tab("y", 5), tab("z", 6)];
    const out = pickDiscards(tabs, 1_000_000, L);
    expect(out).not.toContain("shown");
    expect(out).not.toContain("busy");
    expect(out).not.toContain("gone");
  });
});

describe("pickEviction", () => {
  test("is the least recently used tab that is neither busy nor shown", () => {
    expect(pickEviction([tab("a", 3), tab("b", 1, { busy: true }), tab("c", 2, { shown: true }), tab("d", 4)])).toBe("a");
    expect(pickEviction([tab("b", 1, { busy: true }), tab("c", 2, { shown: true })])).toBeNull();
    expect(pickEviction([])).toBeNull();
  });
});

describe("Gate", () => {
  test("lets n through at once and the rest in order", async () => {
    const gate = new Gate(2);
    const order: string[] = [];
    const r1 = await gate.acquire();
    const r2 = await gate.acquire();
    const third = gate.acquire().then((r) => (order.push("third"), r));
    const fourth = gate.acquire().then((r) => (order.push("fourth"), r));
    await Promise.resolve();
    expect(order).toEqual([]);
    r1();
    r1(); // a release counts once
    expect(await third).toBeTypeOf("function");
    expect(order).toEqual(["third"]);
    r2();
    (await fourth)();
    expect(order).toEqual(["third", "fourth"]);
  });

  test("a waiter that is aborted leaves the line and gives nothing up", async () => {
    const gate = new Gate(1);
    const held = await gate.acquire();
    const ac = new AbortController();
    const waiting = gate.acquire(ac.signal);
    const later = gate.acquire();
    ac.abort(new Error("timed out"));
    await expect(waiting).rejects.toThrow("timed out");
    held();
    // The permit went to the next in line, not to the aborted waiter.
    const release = await later;
    const next = gate.acquire();
    release();
    (await next)();
  });

  test("an already aborted signal never waits", async () => {
    const gate = new Gate(1);
    await gate.acquire();
    const ac = new AbortController();
    ac.abort(new Error("gone"));
    await expect(gate.acquire(ac.signal)).rejects.toThrow("gone");
  });
});
