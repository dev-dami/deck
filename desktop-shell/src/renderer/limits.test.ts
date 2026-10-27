import { describe, expect, test } from "vitest";
import { HOUR } from "./analytics";
import {
  canStartWindow,
  checkedAgo,
  DEFAULT_THRESHOLDS,
  levelOf,
  limitAlerts,
  nextReset,
  quotaOf,
  span,
  toLimits,
  waitLabel,
} from "./limits";
import type { UsageLimit } from "./project";

const T0 = new Date(2026, 9, 27, 7, 47, 0).getTime(); // a local 7:47
const MIN = 60_000;

const row = (at: number, usedPct: number, resetsAt: number, over: Partial<UsageLimit> = {}): UsageLimit => ({
  agentKind: "claude",
  windowId: "five_hour",
  kind: "session",
  label: "5h",
  usedPct,
  resetsAt,
  durationMins: 300,
  plan: "max",
  at,
  ...over,
});

describe("levels and the quota chip", () => {
  test("thresholds", () => {
    expect(levelOf(10, DEFAULT_THRESHOLDS)).toBe("ok");
    expect(levelOf(75, DEFAULT_THRESHOLDS)).toBe("warn");
    expect(levelOf(92, DEFAULT_THRESHOLDS)).toBe("crit");
    expect(levelOf(100, DEFAULT_THRESHOLDS)).toBe("out");
    expect(levelOf(60, { warn: 50, crit: 80 })).toBe("warn");
  });

  test("the fullest window counts; one whose reset passed is empty again", () => {
    const reset = T0 + 2 * HOUR;
    const limits = [
      row(T0, 40, reset),
      row(T0, 82, T0 + 72 * HOUR, { windowId: "seven_day", kind: "weekly", label: "Weekly", durationMins: 10080 }),
      row(T0, 99, T0 - MIN, { windowId: "old" }),
      row(T0, 95, reset, { agentKind: "codex", windowId: "primary" }),
    ];
    const q = quotaOf(limits, "claude", T0, DEFAULT_THRESHOLDS);
    expect(q).toMatchObject({ windowId: "seven_day", used: 82, level: "warn", label: "Weekly" });
    expect(quotaOf(limits, "codex", T0, DEFAULT_THRESHOLDS)?.level).toBe("crit");
    expect(quotaOf(limits, "opencode", T0, DEFAULT_THRESHOLDS)).toBeNull();
  });
});

describe("limit alerts", () => {
  const reset = T0 + 3 * HOUR;

  test("the first look is a starting point, not news", () => {
    const { memo, alerts } = limitAlerts(null, [], [row(T0, 95, reset)], T0, DEFAULT_THRESHOLDS);
    expect(alerts).toEqual([]);
    expect(Object.values(memo)[0]?.level).toBe(2);
  });

  test("each threshold is told once per period, the highest crossed at once", () => {
    let r = limitAlerts(null, [], [row(T0, 50, reset)], T0, { ...DEFAULT_THRESHOLDS, projection: false });
    r = limitAlerts(r.memo, [], [row(T0 + MIN, 76, reset)], T0 + MIN, { ...DEFAULT_THRESHOLDS, projection: false });
    expect(r.alerts.map((a) => a.level)).toEqual(["warn"]);
    expect(r.alerts[0]!.text).toMatch(/^Claude has used 76% of its 5h window · resets /);
    r = limitAlerts(r.memo, [], [row(T0 + 2 * MIN, 80, reset)], T0 + 2 * MIN, { ...DEFAULT_THRESHOLDS, projection: false });
    expect(r.alerts).toEqual([]);
    // Straight past 90 to 100: only the limit hit is told.
    r = limitAlerts(r.memo, [], [row(T0 + 3 * MIN, 100, reset)], T0 + 3 * MIN, { ...DEFAULT_THRESHOLDS, projection: false });
    expect(r.alerts.map((a) => a.level)).toEqual(["out"]);
    expect(r.alerts[0]!.text).toMatch(/^Claude hit its 5h limit · resets /);
    // A new period starts afresh.
    const next = reset + 5 * HOUR;
    r = limitAlerts(r.memo, [], [row(reset + MIN, 10, next)], reset + MIN, { ...DEFAULT_THRESHOLDS, projection: false });
    expect(r.alerts).toEqual([]);
    r = limitAlerts(r.memo, [], [row(reset + 2 * MIN, 91, next)], reset + 2 * MIN, { ...DEFAULT_THRESHOLDS, projection: false });
    expect(r.alerts.map((a) => a.level)).toEqual(["crit"]);
  });

  test("a reset time a few seconds off is the same period", () => {
    let r = limitAlerts(null, [], [row(T0, 50, reset)], T0, DEFAULT_THRESHOLDS);
    r = limitAlerts(r.memo, [], [row(T0 + MIN, 80, reset)], T0 + MIN, DEFAULT_THRESHOLDS);
    r = limitAlerts(r.memo, [], [row(T0 + 2 * MIN, 81, reset + 20_000)], T0 + 2 * MIN, DEFAULT_THRESHOLDS);
    expect(r.alerts).toEqual([]);
  });

  test("a run-out projected before the reset, and soon, is told once", () => {
    // 30 points an hour over the last 2h with 60% used: empty in 80
    // minutes, reset in 3h.
    const hist = [row(T0 - 2 * HOUR, 0, reset), row(T0 - HOUR, 30, reset)];
    let r = limitAlerts(null, hist, [row(T0 - HOUR, 30, reset)], T0 - HOUR, DEFAULT_THRESHOLDS, 2);
    r = limitAlerts(r.memo, [...hist, row(T0, 60, reset)], [row(T0, 60, reset)], T0, DEFAULT_THRESHOLDS, 2);
    expect(r.alerts.map((a) => a.level)).toEqual(["projected"]);
    expect(r.alerts[0]!.text).toMatch(/^Claude will run out in ~1h 20m · resets /);
    r = limitAlerts(r.memo, [...hist, row(T0, 60, reset), row(T0 + MIN, 61, reset)], [row(T0 + MIN, 61, reset)], T0 + MIN, DEFAULT_THRESHOLDS, 2);
    expect(r.alerts).toEqual([]);
  });

  test("a slow burn, or one that ends after the reset, is not news", () => {
    const hist = [row(T0 - 2 * HOUR, 0, reset), row(T0 - HOUR, 5, reset)];
    let r = limitAlerts(null, hist, [], T0 - HOUR, DEFAULT_THRESHOLDS);
    r = limitAlerts(r.memo, [...hist, row(T0, 10, reset)], [], T0, DEFAULT_THRESHOLDS);
    expect(r.alerts).toEqual([]);
  });

  test("off, the projection says nothing", () => {
    const hist = [row(T0 - 2 * HOUR, 0, reset), row(T0 - HOUR, 30, reset)];
    let r = limitAlerts(null, hist, [], T0 - HOUR, { ...DEFAULT_THRESHOLDS, projection: false });
    r = limitAlerts(r.memo, [...hist, row(T0, 60, reset)], [], T0, { ...DEFAULT_THRESHOLDS, projection: false });
    expect(r.alerts).toEqual([]);
  });
});

describe("waits, checks and resets", () => {
  test("the wait's line", () => {
    expect(waitLabel({ until: T0 + 2 * HOUR + 13 * MIN }, T0)).toMatch(/^Waiting for reset · 10:00\s?AM \(in 2h 13m\)$/);
    expect(waitLabel({ until: 0 }, T0)).toBe("Waiting for reset · time not known yet");
    expect(waitLabel({ until: T0 - MIN }, T0)).toBe("Limit reset · resuming");
  });

  test("spans and check ages", () => {
    expect(span(40 * MIN)).toBe("40m");
    expect(span(3 * HOUR)).toBe("3h");
    expect(span(50 * HOUR)).toBe("2d 2h");
    expect(checkedAgo(T0 - 20_000, T0)).toBe("checked just now");
    expect(checkedAgo(T0 - 2 * MIN, T0)).toBe("checked 2m ago");
    expect(checkedAgo(T0 - 3 * HOUR, T0)).toBe("checked 3h ago");
    expect(checkedAgo(0, T0)).toBe("not checked yet");
  });

  test("the next reset is when the agent can work again", () => {
    const five = T0 + 2 * HOUR;
    const week = T0 + 50 * HOUR;
    expect(nextReset([row(T0, 40, five), row(T0, 30, week, { windowId: "seven_day" })], "claude", T0)).toBe(five);
    expect(nextReset([row(T0, 40, five), row(T0, 100, week, { windowId: "seven_day" })], "claude", T0)).toBe(week);
    expect(nextReset([row(T0, 40, T0 - MIN)], "claude", T0)).toBeNull();
  });

  test("a rolling window can be started only when none runs", () => {
    expect(canStartWindow([], "claude", T0)).toBe(true);
    expect(canStartWindow([row(T0, 10, T0 + HOUR)], "claude", T0)).toBe(false);
    expect(canStartWindow([row(T0 - 6 * HOUR, 40, T0 - HOUR)], "claude", T0)).toBe(true);
    expect(canStartWindow([row(T0, 0, 0)], "claude", T0)).toBe(true);
    expect(canStartWindow([row(T0, 0, 0, { agentKind: "codex", windowId: "primary" })], "codex", T0)).toBe(true);
    expect(canStartWindow([], "codex", T0)).toBe(false);
    expect(canStartWindow([], "opencode", T0)).toBe(false);
  });

  test("the Limits answer is read defensively", () => {
    const l = toLimits({
      limits: [{ agent_kind: "claude", window_id: "five_hour", used_pct: 40, resets_at: 5 }],
      checks: [{ kind: "opencode", checked_at: 3, error: "it does not report plan limits", unsupported: true }],
      waits: [{ session_id: "s1", agent: "claude", kind: "claude", until: 9 }],
      scheduled: [{ id: "at-1", session_id: "s1", text: "go on", at: 7 }, "junk"],
      kinds: ["claude", 3],
    });
    expect(l.limits[0]).toMatchObject({ agentKind: "claude", usedPct: 40 });
    expect(l.checks[0]).toEqual({ kind: "opencode", checkedAt: 3, error: "it does not report plan limits", unsupported: true });
    expect(l.waits[0]).toMatchObject({ sessionId: "s1", until: 9, since: 0 });
    expect(l.scheduled).toHaveLength(2);
    expect(l.kinds).toEqual(["claude"]);
    expect(toLimits(null).waits).toEqual([]);
  });
});
