import { burnRate, HOUR, limitSeries, nowState, project, type WindowSeries } from "./analytics";
import { bool, list, num, type Obj, obj, str, strs } from "./guards";
import { toLimit, type UsageLimit } from "./project";

// The agents' plan limits as the window acts on them (deck.v1 Limits): how
// full each agent's plan is now (the quota chip), when to tell the person
// (the "limit" notifications), the agents that wait for a reset, the
// messages scheduled for one, and whether a rolling window can be started.
// Pure, so it is tested; limitsState.ts polls the Deck and feeds it.

export interface LimitCheck {
  kind: string;
  checkedAt: number;
  error: string;
  unsupported: boolean;
}

// An agent waiting for its plan limit to reset. until is 0 while not known.
export interface AgentWait {
  sessionId: string;
  agent: string;
  kind: string;
  since: number;
  until: number;
  window: string;
}

export interface ScheduledSend {
  id: string;
  sessionId: string;
  text: string;
  at: number;
  created: number;
}

export interface Limits {
  limits: UsageLimit[];
  history: UsageLimit[];
  checks: LimitCheck[];
  waits: AgentWait[];
  scheduled: ScheduledSend[];
  kinds: string[];
}

export const toCheck = (c: Obj): LimitCheck => ({
  kind: str(c, "kind"),
  checkedAt: num(c, "checked_at"),
  error: str(c, "error"),
  unsupported: bool(c, "unsupported"),
});

export function toLimits(v: unknown): Limits {
  const o = obj(v);
  return {
    limits: list(o, "limits", toLimit),
    history: list(o, "history", toLimit),
    checks: list(o, "checks", toCheck),
    waits: list(o, "waits", (w) => ({
      sessionId: str(w, "session_id"),
      agent: str(w, "agent"),
      kind: str(w, "kind"),
      since: num(w, "since"),
      until: num(w, "until"),
      window: str(w, "window"),
    })),
    scheduled: list(o, "scheduled", (s) => ({
      id: str(s, "id"),
      sessionId: str(s, "session_id"),
      text: str(s, "text"),
      at: num(s, "at"),
      created: num(s, "created"),
    })),
    kinds: strs(o, "kinds"),
  };
}

export const kindName = (k: string): string => k.charAt(0).toUpperCase() + k.slice(1);

// ---- how full -------------------------------------------------------------

export interface Thresholds {
  // Percent used at which the person is warned, then warned harder.
  warn: number;
  crit: number;
  // Warn when the burn rate says the plan runs out before its reset.
  projection: boolean;
}

export const DEFAULT_THRESHOLDS: Thresholds = { warn: 75, crit: 90, projection: true };

export type Level = "ok" | "warn" | "crit" | "out";
const LEVELS: readonly Level[] = ["ok", "warn", "crit", "out"];

export function levelOf(used: number, th: Pick<Thresholds, "warn" | "crit">): Level {
  if (used >= 100) return "out";
  if (used >= th.crit) return "crit";
  if (used >= th.warn) return "warn";
  return "ok";
}

export interface Quota {
  kind: string;
  windowId: string;
  label: string;
  used: number;
  resetsAt: number;
  level: Level;
}

// The fullest window of kind now: a window whose reset has passed since its
// report counts as empty again. null when kind reported none.
export function quotaOf(limits: readonly UsageLimit[], kind: string, now: number, th: Pick<Thresholds, "warn" | "crit">): Quota | null {
  let best: Quota | null = null;
  for (const l of limits) {
    if (l.agentKind !== kind) continue;
    const reset = l.resetsAt > 0 && l.resetsAt <= now;
    const used = reset ? 0 : Math.min(100, Math.max(0, l.usedPct));
    if (best === null || used > best.used) {
      best = { kind, windowId: l.windowId, label: l.label || l.kind, used, resetsAt: reset ? 0 : l.resetsAt, level: levelOf(used, th) };
    }
  }
  return best;
}

// ---- times ----------------------------------------------------------------

export const clockOf = (t: number, h12 = true): string => new Date(t).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", hour12: h12 });

// "4:40 PM", or "Tue 4:40 PM" when it is not today.
export function whenOf(t: number, now: number, h12 = true): string {
  const c = clockOf(t, h12);
  return new Date(t).toDateString() === new Date(now).toDateString() ? c : `${new Date(t).toLocaleDateString([], { weekday: "short" })} ${c}`;
}

// "2h 13m", "40m", "3d 4h".
export function span(ms: number): string {
  const m = Math.max(0, Math.round(ms / 60_000));
  if (m < 60) return `${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return m % 60 === 0 ? `${h}h` : `${h}h ${m % 60}m`;
  return `${Math.floor(h / 24)}d ${h % 24}h`;
}

// "checked just now", "checked 2m ago", "checked 3h ago".
export function checkedAgo(at: number, now: number): string {
  if (at <= 0) return "not checked yet";
  const m = Math.floor((now - at) / 60_000);
  if (m < 1) return "checked just now";
  if (m < 60) return `checked ${m}m ago`;
  const h = Math.floor(m / 60);
  return h < 48 ? `checked ${h}h ago` : `checked ${Math.floor(h / 24)}d ago`;
}

// "Waiting for reset · 10:00 AM (in 2h 13m)".
export function waitLabel(w: Pick<AgentWait, "until">, now: number, h12 = true): string {
  if (w.until <= 0) return "Waiting for reset · time not known yet";
  if (w.until <= now) return "Limit reset · resuming";
  return `Waiting for reset · ${whenOf(w.until, now, h12)} (in ${span(w.until - now)})`;
}

// When kind can work again, for "Send at next reset": the latest reset of
// its full windows, else the soonest reset of any. null when none is known.
export function nextReset(limits: readonly UsageLimit[], kind: string, now: number): number | null {
  const mine = limits.filter((l) => l.agentKind === kind && l.resetsAt > now);
  const full = mine.filter((l) => l.usedPct >= 99);
  if (full.length > 0) return Math.max(...full.map((l) => l.resetsAt));
  return mine.length === 0 ? null : Math.min(...mine.map((l) => l.resetsAt));
}

// ---- starting a window ----------------------------------------------------

// Kinds whose short window rolls: it starts at the first prompt after the
// last one ended (Claude's 5 hours; Codex's primary window).
const ROLLING = new Set(["claude", "codex"]);

// Whether kind's rolling window can be started early: it has one, and none
// runs now (no report, or the last one's reset has passed).
export function canStartWindow(limits: readonly UsageLimit[], kind: string, now: number): boolean {
  const session = limits.filter((l) => l.agentKind === kind && l.kind === "session");
  // Claude's 5 hours roll whether or not they were reported yet.
  if (session.length === 0) return kind === "claude";
  if (!ROLLING.has(kind)) return false;
  return session.every((l) => l.resetsAt <= now || (l.resetsAt === 0 && l.usedPct <= 0));
}

// ---- alerts ---------------------------------------------------------------

// What has been told of one window in its current period: the highest
// level, and whether its projected run-out was.
export interface Told {
  period: number;
  level: number;
  projected: boolean;
}

export type AlertMemo = Record<string, Told>;

export interface LimitAlert {
  kind: string;
  windowId: string;
  level: Exclude<Level, "ok"> | "projected";
  text: string;
}

// How far ahead a projected run-out is news: further than this, the rate
// will likely change first.
const PROJECT_AHEAD = 2 * HOUR;

// limitAlerts compares each window now with what was told of it, and says
// what is news: a threshold crossed (only the highest at once), the limit
// hit, or a run-out projected before the reset. A new period (the window
// reset) starts afresh, as does a window not seen before. memo null is the
// first look: what is there is the starting point, not news.
export function limitAlerts(
  memo: AlertMemo | null,
  history: readonly UsageLimit[],
  latest: readonly UsageLimit[],
  now: number,
  th: Thresholds,
  burnHours = 3,
  h12 = true,
): { memo: AlertMemo; alerts: LimitAlert[] } {
  const next: AlertMemo = { ...(memo ?? {}) };
  const alerts: LimitAlert[] = [];
  for (const s of limitSeries([...history], [...latest])) {
    const st = nowState(s, now);
    if (st === null) continue;
    const key = `${s.kind}\u0000${s.windowId}`;
    const used = 100 - st.remaining;
    const lvl = LEVELS.indexOf(levelOf(used, th));
    const period = st.resetsAt;
    const prior = next[key];
    // The CLIs round reset times: a minute apart is the same reset.
    const newPeriod = prior !== undefined && period > 0 && prior.period > 0 && Math.abs(prior.period - period) > 60_000;
    const told: Told = prior === undefined || newPeriod ? { period, level: 0, projected: false } : { ...prior, period: period || prior.period };
    if (memo === null) {
      next[key] = { period, level: lvl, projected: projectedOut(s, st, now, burnHours) !== null };
      continue;
    }
    const reset = st.resetsAt > 0 ? ` · resets ${whenOf(st.resetsAt, now, h12)}` : "";
    const who = kindName(s.kind);
    if (lvl > told.level) {
      const level = LEVELS[lvl] as LimitAlert["level"];
      alerts.push({
        kind: s.kind,
        windowId: s.windowId,
        level,
        text: level === "out" ? `${who} hit its ${s.label} limit${reset}` : `${who} has used ${Math.round(used)}% of its ${s.label} window${reset}`,
      });
    }
    told.level = Math.max(told.level, lvl);
    if (th.projection && lvl < 3 && !told.projected) {
      const out = projectedOut(s, st, now, burnHours);
      if (out !== null && out - now <= PROJECT_AHEAD) {
        alerts.push({ kind: s.kind, windowId: s.windowId, level: "projected", text: `${who} will run out in ~${span(out - now)}${reset}` });
        told.projected = true;
      }
    }
    next[key] = told;
  }
  return { memo: next, alerts };
}

// When s runs out at its recent burn rate, if before its next reset.
function projectedOut(s: WindowSeries, st: NonNullable<ReturnType<typeof nowState>>, now: number, burnHours: number): number | null {
  const rate = burnRate(s, now, burnHours);
  if (rate === null || rate <= 0) return null;
  const p = project({ at: now, remaining: st.remaining, resetsAt: st.resetsAt }, rate, s.durationMins, now + 30 * 24 * HOUR, 1);
  const reset = p.resets[0]?.at;
  return p.empty !== null && (reset === undefined || p.empty < reset) ? p.empty : null;
}
