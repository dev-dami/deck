import type { UsageHour, UsageLimit } from "./project";

// The maths behind the Usage page's analytics: the plan windows' recorded
// remaining quota, the burn rate over the last hours, a projection to the
// next resets at that rate, and the hourly usage gathered into buckets.
// Nothing here makes a number up: a window the agent never reported has no
// series, a projection is only ever drawn as one, and a reset is placed
// where the agent said it would be.

export const HOUR = 3_600_000;
export const DAY = 24 * HOUR;

// Two reset times of a window this close are the same reset: the CLIs
// round them differently from one report to the next (as the Go side).
const RESET_SLACK = 60_000;

export interface LimitPoint {
  at: number;
  used: number; // 0-100
  resetsAt: number; // 0 when not known
}

// One plan window of one agent kind, as recorded, oldest first.
export interface WindowSeries {
  kind: string;
  windowId: string;
  label: string;
  windowKind: string; // "session", "weekly", "monthly", "other"
  durationMins: number;
  plan: string;
  points: LimitPoint[];
}

const WINDOW_ORDER: Record<string, number> = { session: 0, weekly: 1, monthly: 2 };

// limitSeries groups the stored rows (and the newest row of each window)
// into one series per window, by kind, the shortest window first.
export function limitSeries(history: UsageLimit[], latest: UsageLimit[] = []): WindowSeries[] {
  const by = new Map<string, { rows: UsageLimit[] }>();
  for (const l of [...history, ...latest]) {
    if (l.agentKind === "" || l.windowId === "" || l.at <= 0) continue;
    const key = `${l.agentKind}\u0000${l.windowId}`;
    let g = by.get(key);
    if (g === undefined) by.set(key, (g = { rows: [] }));
    g.rows.push(l);
  }
  const out: WindowSeries[] = [];
  for (const { rows } of by.values()) {
    rows.sort((a, b) => a.at - b.at);
    const points: LimitPoint[] = [];
    for (const r of rows) {
      const p = { at: r.at, used: clamp(r.usedPct, 0, 100), resetsAt: r.resetsAt };
      const last = points[points.length - 1];
      if (last !== undefined && last.at === p.at) points[points.length - 1] = p;
      else points.push(p);
    }
    const head = rows[rows.length - 1]!;
    out.push({
      kind: head.agentKind,
      windowId: head.windowId,
      label: head.label || head.kind || head.windowId,
      windowKind: head.kind,
      durationMins: head.durationMins,
      plan: head.plan,
      points,
    });
  }
  return out.sort(
    (a, b) =>
      a.kind.localeCompare(b.kind) ||
      (WINDOW_ORDER[a.windowKind] ?? 3) - (WINDOW_ORDER[b.windowKind] ?? 3) ||
      a.durationMins - b.durationMins ||
      a.windowId.localeCompare(b.windowId),
  );
}

const clamp = (v: number, lo: number, hi: number) => Math.min(hi, Math.max(lo, v));

// isReset reports whether the window reset between two reports: a later
// reset time, or (when the times are not known) a drop in use, which
// otherwise never happens.
export function isReset(a: LimitPoint, b: LimitPoint): boolean {
  if (a.resetsAt > 0 && b.resetsAt > 0) return b.resetsAt - a.resetsAt > RESET_SLACK;
  return b.used < a.used - 1;
}

// When a reset between a and b happened: the time a was told, if it falls
// between them, else when b saw it.
function resetTime(a: LimitPoint, b: LimitPoint): number {
  return a.resetsAt > a.at && a.resetsAt <= b.at ? a.resetsAt : b.at;
}

export interface RemainingPoint {
  at: number;
  remaining: number; // 0-100
}

export interface ResetMark {
  at: number;
  gain: number; // points of quota it gave back
}

// recorded is the remaining quota as reported: straight between reports
// of one period, and at a reset a step up to 100% at the reset time, with
// the step as a mark. It ends at the last report.
export function recorded(s: WindowSeries): { points: RemainingPoint[]; resets: ResetMark[] } {
  const points: RemainingPoint[] = [];
  const resets: ResetMark[] = [];
  s.points.forEach((b, i) => {
    const a = s.points[i - 1];
    if (a !== undefined && isReset(a, b)) {
      const t = resetTime(a, b);
      points.push({ at: t, remaining: 100 - a.used }, { at: t, remaining: 100 });
      resets.push({ at: t, gain: a.used });
    }
    points.push({ at: b.at, remaining: 100 - b.used });
  });
  return { points, resets };
}

// burnRate is the quota used per hour, in points, over the hours before
// now: each step of use between two reports counts pro rata for the part
// of it inside the window, and a reset counts only the use after it. It is
// null when nothing was reported before now.
export function burnRate(s: WindowSeries, now: number, hours: number): number | null {
  const pts = s.points.filter((p) => p.at <= now);
  if (pts.length === 0 || hours <= 0) return null;
  const t0 = now - hours * HOUR;
  let used = 0;
  for (let i = 1; i < pts.length; i++) {
    const a = pts[i - 1]!;
    const b = pts[i]!;
    if (b.at <= t0) continue;
    const delta = isReset(a, b) ? b.used : Math.max(0, b.used - a.used);
    const span = b.at - a.at;
    const overlap = b.at - Math.max(a.at, t0);
    used += span > 0 ? (delta * overlap) / span : delta;
  }
  return used / hours;
}

// nowState is what is known of a window now: the last report's remaining
// quota, or 100% when its reset has passed since (the window's own rule,
// not a guess), with when that was.
export function nowState(s: WindowSeries, now: number): { remaining: number; at: number; resetSince: boolean; resetsAt: number } | null {
  const last = [...s.points].reverse().find((p) => p.at <= now);
  if (last === undefined) return null;
  if (last.resetsAt > last.at && last.resetsAt <= now) {
    const period = s.durationMins * 60_000;
    let next = 0;
    if (period > 0) {
      next = last.resetsAt;
      while (next <= now) next += period;
    }
    return { remaining: 100, at: last.resetsAt, resetSince: true, resetsAt: next };
  }
  return { remaining: 100 - last.used, at: last.at, resetSince: false, resetsAt: last.resetsAt };
}

export interface Projection {
  points: RemainingPoint[];
  resets: ResetMark[];
  // empty is when the quota first runs out at this rate, or null if it
  // does not before until.
  empty: number | null;
}

// project runs a window forward from its last report at a steady burn (in
// points an hour): down to 0 at the most, back to 100 at each reset (the
// told one, then every period after it while the period is known). It
// stops at until, or at the step of the maxResets-th reset.
export function project(
  start: { at: number; remaining: number; resetsAt: number },
  burn: number,
  durationMins: number,
  until: number,
  maxResets = Infinity,
): Projection {
  const period = durationMins * 60_000;
  let t = start.at;
  let r = clamp(start.remaining, 0, 100);
  let next: number | null = start.resetsAt > 0 ? start.resetsAt : null;
  const points: RemainingPoint[] = [{ at: t, remaining: r }];
  const resets: ResetMark[] = [];
  let empty: number | null = null;
  const reset = () => {
    resets.push({ at: t, gain: 100 - r });
    r = 100;
    points.push({ at: t, remaining: r });
    next = period > 0 ? next! + period : null;
  };
  while (next !== null && next <= t && resets.length < maxResets) reset();
  const rate = Math.max(0, burn);
  while (t < until && resets.length < maxResets) {
    const end = next !== null && next < until ? next : until;
    if (rate > 0 && r > 0) {
      const out = t + (r / rate) * HOUR;
      if (out < end) {
        t = out;
        r = 0;
        points.push({ at: t, remaining: 0 });
        empty ??= t;
      }
    }
    if (r > 0) r = Math.max(0, r - (rate * (end - t)) / HOUR);
    t = end;
    points.push({ at: t, remaining: r });
    if (next !== null && t === next) reset();
  }
  return { points, resets, empty };
}

// The usage side: hours from the record, gathered into buckets.

export type BucketSize = "hour" | "6h" | "day";

export interface Bucket {
  at: number;
  tokens: number;
  turns: number;
  costUsd: number;
}

export interface Filter {
  kind?: string;
  agentId?: string;
}

const keep = (h: UsageHour, f: Filter) => (f.kind === undefined || h.kind === f.kind) && (f.agentId === undefined || h.agentId === f.agentId);

// bucketStart is the local start of the bucket holding t.
export function bucketStart(t: number, size: BucketSize): number {
  const d = new Date(t);
  if (size === "day") d.setHours(0, 0, 0, 0);
  else d.setHours(size === "6h" ? d.getHours() - (d.getHours() % 6) : d.getHours(), 0, 0, 0);
  return d.getTime();
}

function nextBucket(t: number, size: BucketSize): number {
  const d = new Date(t);
  if (size === "day") d.setDate(d.getDate() + 1);
  else d.setHours(d.getHours() + (size === "6h" ? 6 : 1));
  return d.getTime();
}

// buckets lays out every bucket from from to to (no gaps), and sums the
// hours that fall in each.
export function buckets(hours: UsageHour[], from: number, to: number, size: BucketSize, f: Filter = {}): Bucket[] {
  const out: Bucket[] = [];
  const index = new Map<number, Bucket>();
  for (let t = bucketStart(from, size); t < to; t = nextBucket(t, size)) {
    const b = { at: t, tokens: 0, turns: 0, costUsd: 0 };
    out.push(b);
    index.set(t, b);
  }
  for (const h of hours) {
    if (!keep(h, f)) continue;
    const b = index.get(bucketStart(h.at, size));
    if (b === undefined) continue;
    b.tokens += h.tokens;
    b.turns += h.turns;
    b.costUsd += h.costUsd;
  }
  return out;
}

export interface Totals {
  tokens: number;
  turns: number;
  costUsd: number;
}

// totals sums the hours starting in [from, to).
export function totals(hours: UsageHour[], from: number, to: number, f: Filter = {}): Totals {
  const t = { tokens: 0, turns: 0, costUsd: 0 };
  for (const h of hours) {
    if (!keep(h, f) || h.at < from || h.at >= to) continue;
    t.tokens += h.tokens;
    t.turns += h.turns;
    t.costUsd += h.costUsd;
  }
  return t;
}

// tokenRate is tokens an hour over the hours before now. The record is by
// the hour, so the hour now is in counts whole.
export function tokenRate(hours: UsageHour[], now: number, n: number, f: Filter = {}): number {
  if (n <= 0) return 0;
  return totals(hours, bucketStart(now - n * HOUR, "hour") + HOUR, now + 1, f).tokens / n;
}

// byAgent sums the hours of [from, to) by agent, the most tokens first.
export function byAgent(hours: UsageHour[], from: number, to: number, f: Filter = {}): (Totals & { agentId: string; kind: string })[] {
  const m = new Map<string, Totals & { agentId: string; kind: string }>();
  for (const h of hours) {
    if (!keep(h, f) || h.at < from || h.at >= to) continue;
    const key = `${h.kind}\u0000${h.agentId}`;
    let a = m.get(key);
    if (a === undefined) m.set(key, (a = { agentId: h.agentId || h.kind, kind: h.kind, tokens: 0, turns: 0, costUsd: 0 }));
    a.tokens += h.tokens;
    a.turns += h.turns;
    a.costUsd += h.costUsd;
  }
  return [...m.values()].sort((a, b) => b.tokens - a.tokens || a.agentId.localeCompare(b.agentId));
}

// The time range: a span with now 70% of the way along, stepped back by
// whole spans.

export const SPANS = [
  { id: "1d", label: "Day", ms: DAY, bucket: "hour" },
  { id: "7d", label: "Week", ms: 7 * DAY, bucket: "6h" },
  { id: "30d", label: "Month", ms: 30 * DAY, bucket: "day" },
] as const satisfies readonly { id: string; label: string; ms: number; bucket: BucketSize }[];

export type SpanId = (typeof SPANS)[number]["id"];

export function range(spanMs: number, offset: number, now: number): { from: number; to: number } {
  const to = now + 0.3 * spanMs - offset * spanMs;
  return { from: to - spanMs, to };
}

// ticks are the x axis's labelled times: local midnights for a week or a
// month (every few days, so at most eight), every three hours for a day.
export function ticks(from: number, to: number, spanMs: number): number[] {
  const out: number[] = [];
  if (spanMs <= DAY) {
    const d = new Date(from);
    d.setHours(d.getHours() - (d.getHours() % 3) + 3, 0, 0, 0);
    for (; d.getTime() < to; d.setHours(d.getHours() + 3)) out.push(d.getTime());
    return out;
  }
  const every = Math.max(1, Math.ceil(spanMs / DAY / 8));
  const d = new Date(from);
  d.setHours(24, 0, 0, 0);
  for (let i = 0; d.getTime() < to; i++, d.setDate(d.getDate() + 1)) if (i % every === 0) out.push(d.getTime());
  return out;
}
