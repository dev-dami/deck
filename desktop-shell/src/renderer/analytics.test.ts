import { describe, expect, test } from "vitest";
import { HOUR, DAY, bucketStart, buckets, burnRate, byAgent, isReset, limitSeries, nowState, project, range, recorded, ticks, tokenRate, totals, type WindowSeries } from "./analytics";
import type { UsageHour, UsageLimit } from "./project";

const T0 = new Date(2026, 9, 20, 9, 0, 0).getTime(); // a local 9:00

const row = (at: number, usedPct: number, resetsAt: number, over: Partial<UsageLimit> = {}): UsageLimit => ({
  agentKind: "claude",
  windowId: "five_hour",
  kind: "session",
  label: "5-hour",
  usedPct,
  resetsAt,
  durationMins: 300,
  plan: "max",
  at,
  ...over,
});

const series = (pts: [number, number, number][], durationMins = 300): WindowSeries => ({
  kind: "claude",
  windowId: "five_hour",
  label: "5-hour",
  windowKind: "session",
  durationMins,
  plan: "max",
  points: pts.map(([at, used, resetsAt]) => ({ at, used, resetsAt })),
});

describe("limitSeries", () => {
  test("groups by kind and window, oldest first, merging the newest rows", () => {
    const r1 = T0 + 5 * HOUR;
    const out = limitSeries(
      [row(T0 + HOUR, 20, r1), row(T0, 10, r1), row(T0, 3, 0, { agentKind: "codex", windowId: "primary", kind: "session" }), row(T0, 40, T0 + 7 * DAY, { windowId: "seven_day", kind: "weekly", label: "Weekly", durationMins: 10080 })],
      [row(T0 + HOUR, 20, r1), row(T0 + 2 * HOUR, 30, r1)],
    );
    expect(out.map((s) => `${s.kind}/${s.windowId}`)).toEqual(["claude/five_hour", "claude/seven_day", "codex/primary"]);
    expect(out[0]!.points.map((p) => p.used)).toEqual([10, 20, 30]);
    expect(out[1]!.label).toBe("Weekly");
  });

  test("drops rows with no kind, window or time", () => {
    expect(limitSeries([row(0, 1, 0), row(T0, 1, 0, { windowId: "" })])).toEqual([]);
  });
});

describe("recorded", () => {
  test("is the remaining quota, stepping up to 100% at a told reset", () => {
    const r1 = T0 + 2 * HOUR;
    const s = series([
      [T0, 40, r1],
      [T0 + HOUR, 70, r1],
      [T0 + 3 * HOUR, 10, r1 + 5 * HOUR],
    ]);
    const { points, resets } = recorded(s);
    expect(points).toEqual([
      { at: T0, remaining: 60 },
      { at: T0 + HOUR, remaining: 30 },
      { at: r1, remaining: 30 },
      { at: r1, remaining: 100 },
      { at: T0 + 3 * HOUR, remaining: 90 },
    ]);
    expect(resets).toEqual([{ at: r1, gain: 70 }]);
  });

  test("a drop in use is a reset when no times are told", () => {
    expect(isReset({ at: 0, used: 50, resetsAt: 0 }, { at: 1, used: 5, resetsAt: 0 })).toBe(true);
    expect(isReset({ at: 0, used: 50, resetsAt: 0 }, { at: 1, used: 49.5, resetsAt: 0 })).toBe(false);
    // Reset times a few seconds apart are the same reset.
    expect(isReset({ at: 0, used: 50, resetsAt: 1_000_000 }, { at: 1, used: 40, resetsAt: 1_020_000 })).toBe(false);
  });
});

describe("burnRate", () => {
  const r1 = T0 + 10 * HOUR;
  const s = series([
    [T0, 0, r1],
    [T0 + 2 * HOUR, 20, r1],
    [T0 + 3 * HOUR, 26, r1],
  ]);

  test("is the use per hour over the last hours, pro rata across reports", () => {
    const now = T0 + 3 * HOUR;
    expect(burnRate(s, now, 1)).toBeCloseTo(6);
    // 6 in the last hour and half of the 20 before it.
    expect(burnRate(s, now, 2)).toBeCloseTo((6 + 10) / 2);
    expect(burnRate(s, now, 3)).toBeCloseTo(26 / 3);
  });

  test("slows as the hours pass with no use reported", () => {
    expect(burnRate(s, T0 + 6 * HOUR, 3)).toBe(0);
    expect(burnRate(s, T0 + 4 * HOUR, 3)).toBeCloseTo((6 + 10) / 3);
  });

  test("counts only the use after a reset", () => {
    const rs = series([
      [T0, 90, T0 + HOUR],
      [T0 + 2 * HOUR, 12, T0 + 6 * HOUR],
    ]);
    expect(burnRate(rs, T0 + 2 * HOUR, 2)).toBeCloseTo(6);
  });

  test("is null with nothing reported yet", () => {
    expect(burnRate(s, T0 - HOUR, 3)).toBeNull();
    expect(burnRate(series([]), T0, 3)).toBeNull();
  });
});

describe("nowState", () => {
  test("is the last report, or a full window once its reset has passed", () => {
    const s = series([[T0, 30, T0 + HOUR]], 300);
    expect(nowState(s, T0 + 30 * 60_000)).toEqual({ remaining: 70, at: T0, resetSince: false, resetsAt: T0 + HOUR });
    expect(nowState(s, T0 + 2 * HOUR)).toEqual({ remaining: 100, at: T0 + HOUR, resetSince: true, resetsAt: T0 + 6 * HOUR });
    expect(nowState(s, T0 - 1)).toBeNull();
  });
});

describe("project", () => {
  test("drains at the rate, empties, then refills at each reset", () => {
    const p = project({ at: T0, remaining: 30, resetsAt: T0 + 5 * HOUR }, 10, 300, T0 + 12 * HOUR);
    expect(p.empty).toBe(T0 + 3 * HOUR);
    expect(p.resets).toEqual([
      { at: T0 + 5 * HOUR, gain: 100 },
      { at: T0 + 10 * HOUR, gain: 50 },
    ]);
    expect(p.points).toEqual([
      { at: T0, remaining: 30 },
      { at: T0 + 3 * HOUR, remaining: 0 },
      { at: T0 + 5 * HOUR, remaining: 0 },
      { at: T0 + 5 * HOUR, remaining: 100 },
      { at: T0 + 10 * HOUR, remaining: 50 },
      { at: T0 + 10 * HOUR, remaining: 100 },
      { at: T0 + 12 * HOUR, remaining: 80 },
    ]);
  });

  test("holds level with no burn, and stops resetting when the period is unknown", () => {
    const p = project({ at: T0, remaining: 40, resetsAt: T0 + HOUR }, 0, 0, T0 + 5 * HOUR);
    expect(p.empty).toBeNull();
    expect(p.resets).toEqual([{ at: T0 + HOUR, gain: 60 }]);
    expect(p.points.at(-1)).toEqual({ at: T0 + 5 * HOUR, remaining: 100 });
  });

  test("stops at the step of the last reset asked for", () => {
    const p = project({ at: T0, remaining: 30, resetsAt: T0 + 5 * HOUR }, 10, 300, T0 + 30 * HOUR, 1);
    expect(p.resets).toHaveLength(1);
    expect(p.points.at(-1)).toEqual({ at: T0 + 5 * HOUR, remaining: 100 });
  });

  test("a reset already past at the start applies first", () => {
    const p = project({ at: T0, remaining: 10, resetsAt: T0 - HOUR }, 5, 300, T0 + 2 * HOUR);
    expect(p.resets).toEqual([{ at: T0, gain: 90 }]);
    expect(p.points.at(-1)).toEqual({ at: T0 + 2 * HOUR, remaining: 90 });
  });
});

describe("usage buckets", () => {
  const h = (at: number, tokens: number, kind = "claude", agentId = "claude-1"): UsageHour => ({ at, kind, agentId, turns: 1, tokens, costUsd: tokens / 1000 });
  const hours = [h(T0, 100), h(T0 + HOUR, 50), h(T0 + HOUR, 7, "claude", "reviewer"), h(T0 + 7 * HOUR, 30, "codex", "codex-1"), h(T0 - 2 * DAY, 999)];

  test("lays out every bucket with no gaps and sums the hours in each", () => {
    const b = buckets(hours, T0, T0 + 4 * HOUR, "hour");
    expect(b.map((x) => x.tokens)).toEqual([100, 57, 0, 0]);
    expect(b[1]!.turns).toBe(2);
    expect(b[1]!.costUsd).toBeCloseTo(0.057);
    expect(buckets(hours, T0, T0 + 4 * HOUR, "hour", { agentId: "reviewer" }).map((x) => x.tokens)).toEqual([0, 7, 0, 0]);
  });

  test("six-hour and day buckets start on local boundaries", () => {
    expect(bucketStart(T0 + 30 * 60_000, "6h")).toBe(new Date(2026, 9, 20, 6).getTime());
    expect(bucketStart(T0, "day")).toBe(new Date(2026, 9, 20).getTime());
    const day = buckets(hours, T0 - 2 * DAY, T0 + HOUR, "day", { kind: "claude" });
    expect(day.map((x) => x.tokens)).toEqual([999, 0, 157]);
  });

  test("totals, rate and the split by agent", () => {
    expect(totals(hours, T0, T0 + DAY)).toEqual({ tokens: 187, turns: 4, costUsd: expect.closeTo(0.187) });
    expect(totals(hours, T0, T0 + DAY, { kind: "codex" }).tokens).toBe(30);
    // The last 2 hours before 10:30 count the 9:00 and 10:00 hours.
    expect(tokenRate(hours, T0 + 1.5 * HOUR, 2, { kind: "claude" })).toBeCloseTo(157 / 2);
    expect(byAgent(hours, T0, T0 + DAY, { kind: "claude" }).map((a) => [a.agentId, a.tokens])).toEqual([
      ["claude-1", 150],
      ["reviewer", 7],
    ]);
  });
});

describe("range and ticks", () => {
  test("now sits 70% along; an offset steps back whole spans", () => {
    expect(range(10 * HOUR, 0, T0)).toEqual({ from: T0 - 7 * HOUR, to: T0 + 3 * HOUR });
    expect(range(10 * HOUR, 1, T0)).toEqual({ from: T0 - 17 * HOUR, to: T0 - 7 * HOUR });
  });

  test("a day ticks every three hours, a month at most eight midnights", () => {
    const d = ticks(T0, T0 + DAY, DAY);
    expect(d.map((t) => new Date(t).getHours())).toEqual([12, 15, 18, 21, 0, 3, 6]);
    const m = ticks(T0, T0 + 30 * DAY, 30 * DAY);
    expect(m.length).toBeLessThanOrEqual(8);
    expect(m.every((t) => new Date(t).getHours() === 0)).toBe(true);
  });
});
