import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { type ReactNode, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import {
  type Bucket,
  type Projection,
  type RemainingPoint,
  type ResetMark,
  type SpanId,
  type WindowSeries,
  DAY,
  HOUR,
  SPANS,
  buckets,
  burnRate,
  byAgent,
  limitSeries,
  nowState,
  project,
  range,
  recorded,
  ticks,
  tokenRate,
  totals,
} from "../analytics";
import { cn } from "../lib/cn";
import { useLimits } from "../limitsState";
import type { ProjectApi, Usage } from "../project";
import { ChevronRight } from "./icons";
import { Button } from "./kit/button";
import { KindLimitControls } from "./Limits";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { compact, dollars, niceCeil } from "./Usage";
import { AgentGlyph } from "./Welcome";

// The analytics side of the Usage page, after a quota dashboard: for each
// agent kind, how much of its plan window is left, a chart of what was
// recorded with a dashed projection to the next resets at the recent burn
// rate, and the tokens beneath on the same time axis. An agent that does
// not report plan limits shows its usage alone: no quota axis, no
// projection, nothing made up.

// How many days of record the page asks for; the navigator goes no
// further back.
export const HISTORY_DAYS = 60;

const BURNS = [1, 3, 6] as const;
type Burn = (typeof BURNS)[number];

// The view's settings, kept per viewer.
const KEY = "deck.usage.analytics";
interface Settings {
  tab: string;
  burn: Burn;
  span: SpanId;
}
function loadSettings(): Settings {
  const d: Settings = { tab: "all", burn: 3, span: "7d" };
  try {
    const v = JSON.parse(window.localStorage.getItem(KEY) ?? "{}") as Partial<Settings>;
    return {
      tab: typeof v.tab === "string" ? v.tab : d.tab,
      burn: BURNS.includes(v.burn as Burn) ? (v.burn as Burn) : d.burn,
      span: SPANS.some((s) => s.id === v.span) ? (v.span as SpanId) : d.span,
    };
  } catch {
    return d;
  }
}

const kindName = (k: string) => k.charAt(0).toUpperCase() + k.slice(1);

const sameDay = (a: number, b: number) => new Date(a).toDateString() === new Date(b).toDateString();
const clock = (t: number) => new Date(t).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
const shortDate = (t: number) => new Date(t).toLocaleDateString([], { month: "short", day: "numeric" });
const dayHour = (t: number) => `${new Date(t).toLocaleDateString([], { weekday: "short" })} ${new Date(t).toLocaleTimeString([], { hour: "numeric" })}`;

// A time near now, as briefly as it reads: "4:40 PM", "Tue 4:40 PM" within
// the week, else "Nov 3, 4:40 PM".
export function when(t: number, now: number): string {
  if (sameDay(t, now)) return clock(t);
  if (Math.abs(t - now) < 6 * DAY) return `${new Date(t).toLocaleDateString([], { weekday: "short" })} ${clock(t)}`;
  return `${shortDate(t)}, ${clock(t)}`;
}

// How long until t: "in 2h 10m", "in 3d 4h".
export function until(t: number, now: number): string {
  const m = Math.max(0, Math.round((t - now) / 60_000));
  if (m < 60) return `in ${m}m`;
  const h = Math.floor(m / 60);
  if (h < 48) return `in ${h}h ${m % 60}m`;
  return `in ${Math.floor(h / 24)}d ${h % 24}h`;
}

const pct = (v: number) => `${Math.round(v)}%`;

function useWidth<T extends HTMLElement>() {
  const ref = useRef<T>(null);
  const [w, setW] = useState(0);
  useLayoutEffect(() => {
    const el = ref.current;
    if (el === null) return;
    setW(el.clientWidth);
    const ro = new ResizeObserver(([e]) => e !== undefined && setW(e.contentRect.width));
    ro.observe(el);
    return () => ro.disconnect();
  }, []);
  return [ref, w] as const;
}

// The value of a drawn line at t, or null outside it. At a step (two
// points at one time) the later one wins.
function valueAt(points: RemainingPoint[], t: number): number | null {
  const first = points[0];
  const last = points[points.length - 1];
  if (first === undefined || last === undefined || t < first.at || t > last.at) return null;
  for (let i = points.length - 1; i > 0; i--) {
    const a = points[i - 1]!;
    const b = points[i]!;
    if (a.at <= t && t <= b.at) return b.at === a.at ? b.remaining : a.remaining + ((b.remaining - a.remaining) * (t - a.at)) / (b.at - a.at);
  }
  return first.remaining;
}

interface Quota {
  line: RemainingPoint[];
  resets: ResetMark[];
  projection: Projection | null;
  lastAt: number;
}

const PAD = { l: 40, r: 12, top: 22, x: 22 };
const GAP = 24;

// The chart: remaining quota (when there is one) over the tokens per
// bucket, on one time axis, with Now, the resets and the empty point
// marked. One unit per plot: percent above, tokens below.
function TimeChart({
  kind,
  from,
  to,
  now,
  spanMs,
  quota,
  bars,
  barMs,
  barLabel,
}: {
  kind: string;
  from: number;
  to: number;
  now: number;
  spanMs: number;
  quota: Quota | null;
  bars: Bucket[];
  barMs: number;
  barLabel: string;
}) {
  const [ref, w] = useWidth<HTMLDivElement>();
  const clip = useId();
  const fade = useId();
  const [hover, setHover] = useState<number | null>(null);
  const colour = kindColour(kind);
  const qh = quota === null ? 0 : 150;
  const bh = quota === null ? 132 : 44;
  const h = PAD.top + qh + (quota === null ? 0 : GAP) + bh + PAD.x;
  const plotW = Math.max(1, w - PAD.l - PAD.r);
  const x = (t: number) => PAD.l + ((t - from) / (to - from)) * plotW;
  const yq = (r: number) => PAD.top + (1 - r / 100) * qh;
  const by0 = h - PAD.x;
  const btop = niceCeil(Math.max(0, ...bars.map((b) => b.tokens)));
  const yb = (v: number) => by0 - (v / btop) * bh;
  const path = (pts: RemainingPoint[]) => pts.map((p, i) => `${i === 0 ? "M" : "L"}${x(p.at).toFixed(1)},${yq(p.remaining).toFixed(1)}`).join("");
  const xTicks = ticks(from, to, spanMs);
  const tickLabel = (t: number) =>
    spanMs <= DAY ? new Date(t).toLocaleTimeString([], { hour: "numeric" }) : new Date(t).toLocaleDateString([], { weekday: "short", day: "numeric" });
  const nowIn = now >= from && now <= to;

  // Reset labels: the gain over the step, skipped where one would crowd
  // the last, and only for a real step. Past resets are labelled while
  // there are few in the range; the projected one always is.
  const resetLabels: { at: number; gain: number; projected: boolean }[] = [];
  if (quota !== null) {
    let lastX = -Infinity;
    const past = quota.resets.filter((r) => r.at >= from && r.at <= to);
    const all = [...(past.length <= 6 ? past : []).map((r) => ({ ...r, projected: false })), ...(quota.projection?.resets ?? []).map((r) => ({ ...r, projected: true }))];
    for (const r of all.sort((a, b) => a.at - b.at)) {
      const rx = x(r.at);
      if (r.at < from || r.at > to || r.gain < 1 || rx - lastX < 40) continue;
      resetLabels.push(r);
      lastX = rx;
    }
  }
  const empty = quota?.projection?.empty ?? null;
  const emptyIn = empty !== null && empty >= from && empty <= to;

  const hoverBar = hover === null ? undefined : bars.find((b) => b.at <= hover && hover < b.at + barMs);
  const hoverRec = hover === null || quota === null ? null : valueAt(quota.line, hover);
  const hoverProj = hover === null || quota?.projection == null || hoverRec !== null ? null : valueAt(quota.projection.points, hover);

  const onMove = (e: React.MouseEvent<SVGSVGElement>) => {
    const r = e.currentTarget.getBoundingClientRect();
    const px = e.clientX - r.left;
    if (px < PAD.l || px > w - PAD.r) return setHover(null);
    setHover(from + ((px - PAD.l) / plotW) * (to - from));
  };

  return (
    <div ref={ref} className="relative w-full" data-chart={kind}>
      {w > 0 && (
        <svg width={w} height={h} className="block overflow-visible" role="img" aria-label={`${kindName(kind)}: ${quota === null ? "" : "remaining quota and "}${barLabel.toLowerCase()}`} onMouseMove={onMove} onMouseLeave={() => setHover(null)}>
          <defs>
            <clipPath id={clip}>
              <rect x={PAD.l} y={0} width={plotW} height={h} />
            </clipPath>
            <linearGradient id={fade} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0" style={{ stopColor: colour, stopOpacity: 0.22 }} />
              <stop offset="1" style={{ stopColor: colour, stopOpacity: 0.02 }} />
            </linearGradient>
          </defs>

          {/* The quota plot: three round ticks, hairline grid. */}
          {quota !== null &&
            [100, 50, 0].map((v) => (
              <g key={v}>
                <line x1={PAD.l} x2={w - PAD.r} y1={yq(v)} y2={yq(v)} style={{ stroke: v === 0 ? "var(--hairline-strong)" : "var(--hairline)" }} />
                <text x={PAD.l - 8} y={yq(v)} dy="0.32em" textAnchor="end" className="fill-faint text-3xs tabular-nums">
                  {v}%
                </text>
              </g>
            ))}
          {/* The tokens plot: its top and its baseline. */}
          <line x1={PAD.l} x2={w - PAD.r} y1={by0 - bh} y2={by0 - bh} style={{ stroke: "var(--hairline)" }} />
          <line x1={PAD.l} x2={w - PAD.r} y1={by0} y2={by0} style={{ stroke: "var(--hairline-strong)" }} />
          <text x={PAD.l - 8} y={by0 - bh} dy="0.32em" textAnchor="end" className="fill-faint text-3xs tabular-nums">
            {compact(btop).replace(/\.0(?=[kMB]?$)/, "")}
          </text>
          <text x={PAD.l - 8} y={by0} dy="0.32em" textAnchor="end" className="fill-faint text-3xs tabular-nums">
            0
          </text>

          {xTicks.map((t) => (
            <text key={t} x={x(t)} y={h - 6} textAnchor="middle" className="fill-faint text-3xs tabular-nums">
              {tickLabel(t)}
            </text>
          ))}

          <g clipPath={`url(#${clip})`}>
            {bars.map((b) => {
              if (b.tokens <= 0) return null;
              const x0 = x(b.at) + 1;
              const bw = Math.max(1, x(b.at + barMs) - x(b.at) - 2);
              const y0 = Math.min(by0 - 1.5, yb(b.tokens));
              return (
                <rect
                  key={b.at}
                  x={x0}
                  y={y0}
                  width={bw}
                  height={by0 - y0}
                  rx={Math.min(2, bw / 2)}
                  style={{ fill: colour, opacity: hoverBar === undefined || hoverBar === b ? 0.55 : 0.25 }}
                />
              );
            })}
            {quota !== null && quota.line.length > 0 && (
              <>
                <path d={`${path(quota.line)}L${x(quota.line[quota.line.length - 1]!.at).toFixed(1)},${yq(0)}L${x(quota.line[0]!.at).toFixed(1)},${yq(0)}Z`} fill={`url(#${fade})`} />
                <path d={path(quota.line)} fill="none" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" style={{ stroke: colour }} />
              </>
            )}
            {quota?.projection != null && (
              <path d={path(quota.projection.points)} fill="none" strokeWidth={1.5} strokeDasharray="4 4" strokeLinejoin="round" style={{ stroke: colour, opacity: 0.75 }} data-projected="" />
            )}
          </g>

          {resetLabels.map((r) => (
            <text key={`${r.at}-${r.projected}`} x={x(r.at)} y={PAD.top - 8} textAnchor="middle" className={cn("text-3xs tabular-nums", r.projected ? "fill-faint" : "fill-muted-foreground")}>
              +{Math.round(r.gain)}%
            </text>
          ))}

          {nowIn && (
            <g>
              <line x1={x(now)} x2={x(now)} y1={PAD.top - 4} y2={by0} style={{ stroke: "var(--hairline-strong)" }} />
              {!resetLabels.some((r) => Math.abs(x(r.at) - x(now)) < 36) && (
                <text x={x(now)} y={PAD.top - 8} textAnchor="middle" className="fill-muted-foreground font-medium text-3xs">
                  Now
                </text>
              )}
            </g>
          )}
          {quota !== null && quota.line.length > 0 && quota.lastAt >= from && quota.lastAt <= to && (
            <circle cx={x(quota.lastAt)} cy={yq(quota.line[quota.line.length - 1]!.remaining)} r={3.5} strokeWidth={2} style={{ fill: "var(--background)", stroke: colour }} />
          )}
          {emptyIn && (
            <g>
              <circle cx={x(empty!)} cy={yq(0)} r={3.5} strokeWidth={1.5} style={{ fill: "var(--background)", stroke: "var(--foreground)" }} />
              <text x={x(empty!)} y={yq(0) - 9} textAnchor={x(empty!) > w - 120 ? "end" : "middle"} className="fill-foreground font-medium text-3xs">
                Empty {when(empty!, now)}
              </text>
            </g>
          )}

          {hover !== null && <line x1={x(hover)} x2={x(hover)} y1={PAD.top} y2={by0} style={{ stroke: "var(--muted-foreground)", opacity: 0.5 }} pointerEvents="none" />}
        </svg>
      )}
      {hover !== null && (
        <div
          className="material pointer-events-none absolute top-0 z-10 w-max -translate-x-1/2 rounded-[10px] px-3 py-2 text-xs"
          style={{ left: `clamp(5rem, ${x(hover)}px, calc(100% - 5rem))` }}
          role="status"
        >
          <div className="font-medium">{when(hover, now)}</div>
          {hoverRec !== null && <div className="text-muted-foreground tabular-nums">{pct(hoverRec)} left</div>}
          {hoverProj !== null && <div className="text-muted-foreground tabular-nums">{pct(hoverProj)} left, projected</div>}
          {hoverBar !== undefined && (
            <div className="text-muted-foreground tabular-nums">
              {compact(hoverBar.tokens)} tokens · {hoverBar.turns} turns{hoverBar.costUsd > 0 ? ` · ${dollars(hoverBar.costUsd)}` : ""}
            </div>
          )}
        </div>
      )}
    </div>
  );
}

// The chart's key: what each mark is.
function Legend({ kind, quota, barLabel }: { kind: string; quota: boolean; barLabel: string }) {
  const c = kindColour(kind);
  return (
    <div className="flex flex-wrap items-center gap-x-5 gap-y-1 pl-10 text-2xs text-faint" aria-hidden>
      {quota && (
        <>
          <span className="flex items-center gap-1.5">
            <svg width="16" height="4">
              <line x1="1" x2="15" y1="2" y2="2" strokeWidth="2" strokeLinecap="round" style={{ stroke: c }} />
            </svg>
            Recorded
          </span>
          <span className="flex items-center gap-1.5">
            <svg width="16" height="4">
              <line x1="0" x2="16" y1="2" y2="2" strokeWidth="1.5" strokeDasharray="4 4" style={{ stroke: c, opacity: 0.75 }} />
            </svg>
            Projected at the burn rate
          </span>
        </>
      )}
      <span className="flex items-center gap-1.5">
        <span className="size-2 rounded-[2px]" style={{ background: c, opacity: 0.55 }} />
        {barLabel}
      </span>
    </div>
  );
}

function Stat({ label, value, detail }: { label: string; value: ReactNode; detail?: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-0.5" data-stat={label}>
      <dt className="text-2xs text-faint">{label}</dt>
      <dd className="truncate text-[13px] tabular-nums">{value}</dd>
      {detail !== undefined && <dd className="truncate text-2xs text-faint tabular-nums">{detail}</dd>}
    </div>
  );
}

interface View {
  from: number;
  to: number;
  now: number;
  spanMs: number;
  bucket: (typeof SPANS)[number]["bucket"];
  burn: Burn;
}

const BUCKET_MS = { hour: HOUR, "6h": 6 * HOUR, day: DAY } as const;
const BUCKET_LABEL = { hour: "Tokens per hour", "6h": "Tokens per 6 hours", day: "Tokens per day" } as const;

// One agent kind: its headline, its stats and its chart.
function KindSection({
  kind,
  theme,
  data,
  windows,
  view,
  detail,
  limits,
}: {
  kind: string;
  theme: Theme | null;
  data: Usage;
  windows: WindowSeries[];
  view: View;
  detail: boolean;
  // The kind's check and start-window controls (ui/Limits.tsx).
  limits?: ReactNode;
}) {
  const { now, from, to, burn } = view;
  // The window shown: the person's pick, else the one with least left.
  const states = windows.map((w) => ({ w, s: nowState(w, now) }));
  const tightest = [...states].sort((a, b) => (a.s?.remaining ?? 101) - (b.s?.remaining ?? 101))[0]?.w.windowId;
  const [pick, setPick] = useState<string | null>(null);
  const win = windows.find((w) => w.windowId === (pick ?? tightest));
  const state = win === undefined ? null : nowState(win, now);

  const midnight = new Date(now).setHours(0, 0, 0, 0);
  const weekAgo = midnight - 6 * DAY;
  const f = { kind };
  const today = totals(data.hours, midnight, now + 1, f);
  const week = totals(data.hours, weekAgo, now + 1, f);
  const bars = buckets(data.hours, from, to, view.bucket, f);
  const agents = detail ? byAgent(data.hours, from, Math.min(to, now + 1), f) : [];
  const agentTotal = Math.max(1, agents.reduce((a, b) => a + b.tokens, 0));

  let quota: Quota | null = null;
  let rate: number | null = null;
  if (win !== undefined && state !== null) {
    const rec = recorded(win);
    rate = burnRate(win, now, burn);
    const last = win.points.filter((p) => p.at <= now).at(-1)!;
    // The projection runs from the last report to the next reset (past
    // that, the rate says little), only when the range reaches past now.
    const projection =
      to > now && rate !== null
        ? project({ at: last.at, remaining: 100 - last.used, resetsAt: last.resetsAt }, rate, win.durationMins, to, state.resetSince ? 2 : 1)
        : null;
    quota = { line: rec.points.filter((p) => p.at <= now), resets: rec.resets, projection, lastAt: last.at };
  }
  const proj =
    win !== undefined && state !== null && rate !== null ? project({ at: state.at, remaining: state.remaining, resetsAt: state.resetsAt }, rate, win.durationMins, now + 30 * DAY, 1) : null;
  const nextReset = proj?.resets[0];
  const runsOut = proj?.empty ?? null;
  const beforeReset = runsOut !== null && (nextReset === undefined || runsOut < nextReset.at);
  const tokensPerHour = tokenRate(data.hours, now, burn, f);
  const lastHour = data.hours.findLast((h) => h.kind === kind && h.at <= now)?.at;

  return (
    <section aria-label={kindName(kind)} className="flex flex-col gap-5 rounded-(--sheet-radius) border border-hairline bg-surface-1 px-5 pt-4 pb-4" data-kind-section={kind}>
      <header className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <AgentGlyph kind={kind} mark={kindMark(theme, kind)} className="text-[15px]" />
        <h2 className="font-semibold text-[13px]">{kindName(kind)}</h2>
        {win?.plan !== undefined && win.plan !== "" && <span className="text-faint text-xs capitalize">{win.plan} plan</span>}
        {limits !== undefined && <div className="ml-auto">{limits}</div>}
        {windows.length > 1 && (
          <div className={cn(segmentTrack(), limits === undefined && "ml-auto")} role="radiogroup" aria-label={`${kindName(kind)} window`}>
            {states.map(({ w, s }) => (
              <button key={w.windowId} type="button" role="radio" aria-checked={w === win} onClick={() => setPick(w.windowId)} className={segmentItem({ on: w === win })}>
                {w.label}
                {s !== null && <span className="text-faint tabular-nums">{pct(s.remaining)}</span>}
              </button>
            ))}
          </div>
        )}
      </header>

      <div className="flex flex-wrap items-end gap-x-10 gap-y-4">
        {state !== null && win !== undefined ? (
          <div className="flex min-w-44 flex-col gap-0.5" data-headline="quota">
            <span className="font-semibold text-[2.5rem] leading-none tracking-[-0.03em]">{pct(state.remaining)}</span>
            <span className="text-muted-foreground text-xs">
              left of the {win.label} window
              <span className="text-faint"> · {state.resetSince ? `reset ${when(state.at, now)}` : `as of ${when(state.at, now)}`}</span>
            </span>
          </div>
        ) : (
          <div className="flex min-w-44 flex-col gap-0.5" data-headline="usage">
            <span className="font-semibold text-[2.5rem] leading-none tracking-[-0.03em]">{compact(week.tokens)}</span>
            <span className="text-muted-foreground text-xs">
              tokens in 7 days<span className="text-faint"> · reports no plan limits</span>
            </span>
          </div>
        )}
        <dl className="grid min-w-[min(100%,32rem)] flex-1 grid-cols-[repeat(auto-fit,minmax(6.5rem,1fr))] gap-x-6 gap-y-3">
          {quota !== null && win !== undefined ? (
            <>
              <Stat
                label={`Burn, last ${burn}h`}
                value={rate === null ? "—" : `${rate.toFixed(1)}%/h`}
                detail={tokensPerHour > 0 ? `${compact(tokensPerHour)} tokens/h` : undefined}
              />
              <Stat
                label="Runs out, projected"
                value={beforeReset && runsOut !== null ? when(runsOut, now) : nextReset === undefined ? "Not at this rate" : "Not before reset"}
                detail={beforeReset && runsOut !== null ? until(runsOut, now) : nextReset === undefined ? undefined : `${pct(100 - nextReset.gain)} left at reset`}
              />
              <Stat
                label="Next reset"
                value={nextReset === undefined ? "Not reported" : when(nextReset.at, now)}
                detail={nextReset === undefined ? undefined : `+${Math.round(nextReset.gain)}% · ${until(nextReset.at, now)}`}
              />
            </>
          ) : (
            <>
              <Stat label={`Burn, last ${burn}h`} value={`${compact(tokensPerHour)} tokens/h`} />
              <Stat label="Last active" value={lastHour === undefined ? "—" : when(lastHour, now)} detail="to the hour" />
              <Stat
                label="7 days"
                value={week.costUsd > 0 ? dollars(week.costUsd) : `${week.turns} turns`}
                detail={week.costUsd > 0 ? `${week.turns} turns` : "no cost reported"}
              />
            </>
          )}
          <Stat label="Today" value={`${compact(today.tokens)} tokens`} detail={`${today.turns} turns${today.costUsd > 0 ? ` · ${dollars(today.costUsd)}` : ""}`} />
          {quota !== null && (
            <Stat label="This week" value={`${compact(week.tokens)} tokens`} detail={`${week.turns} turns${week.costUsd > 0 ? ` · ${dollars(week.costUsd)}` : ""}`} />
          )}
        </dl>
      </div>

      <div className="flex flex-col gap-2">
        <TimeChart kind={kind} from={from} to={to} now={now} spanMs={view.spanMs} quota={quota} bars={bars} barMs={BUCKET_MS[view.bucket]} barLabel={BUCKET_LABEL[view.bucket]} />
        <Legend kind={kind} quota={quota !== null} barLabel={BUCKET_LABEL[view.bucket]} />
      </div>

      {detail && agents.length > 0 && (
        <div className="flex flex-col gap-2 border-hairline border-t pt-4">
          <h3 className="text-2xs text-faint">By agent, in this range</h3>
          {agents.map((a) => (
            <div key={a.agentId} className="flex items-center gap-3 text-[13px]">
              <span className="w-32 shrink-0 truncate">{a.agentId}</span>
              <span className="h-1.5 min-w-0 flex-1 overflow-hidden rounded-full" style={{ background: `color-mix(in srgb, ${kindColour(kind)} 14%, transparent)` }}>
                <span className="block h-full rounded-full" style={{ width: `${(a.tokens / agentTotal) * 100}%`, minWidth: 6, background: kindColour(kind) }} />
              </span>
              <span className="w-48 shrink-0 text-right text-muted-foreground text-xs tabular-nums">
                {compact(a.tokens)} · {a.turns} turns{a.costUsd > 0 ? ` · ${dollars(a.costUsd)}` : ""}
              </span>
            </div>
          ))}
        </div>
      )}
    </section>
  );
}

export function Analytics({
  data,
  theme,
  api,
  onLimitsDone,
}: {
  data: Usage;
  theme: Theme | null;
  // The project's Deck, for checking limits and starting windows; what a
  // check or start said goes to onLimitsDone.
  api?: ProjectApi | undefined;
  onLimitsDone?: (text: string, error: boolean) => void;
}) {
  const installed = useLimits(api).data?.kinds;
  const [settings, setSettings] = useState(loadSettings);
  const [offset, setOffset] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(id);
  }, []);
  const set = (p: Partial<Settings>) =>
    setSettings((s) => {
      const n = { ...s, ...p };
      try {
        window.localStorage.setItem(KEY, JSON.stringify(n));
      } catch {
        // A viewer convenience only.
      }
      return n;
    });

  const windows = useMemo(() => limitSeries(data.limitHistory, data.limits), [data]);
  // Kinds: the most tokens over the last week first, then any that only
  // reported limits.
  const kinds = useMemo(() => {
    const t = new Map<string, number>();
    for (const h of data.hours) t.set(h.kind, (t.get(h.kind) ?? 0) + (h.at >= now - 7 * DAY ? h.tokens : 0));
    for (const w of windows) if (!t.has(w.kind)) t.set(w.kind, -1);
    // Installed agents with no record yet: their limits can be checked.
    for (const k of installed ?? []) if (!t.has(k)) t.set(k, -2);
    return [...t.entries()].sort((a, b) => b[1] - a[1] || a[0].localeCompare(b[0])).map(([k]) => k);
  }, [data, windows, now, installed]);

  const span = SPANS.find((s) => s.id === settings.span) ?? SPANS[1];
  const { from, to } = range(span.ms, offset, now);
  const oldest = now - HISTORY_DAYS * DAY;
  const tab = kinds.includes(settings.tab) ? settings.tab : "all";
  const shown = tab === "all" ? kinds : [tab];
  const view: View = { from, to, now, spanMs: span.ms, bucket: span.bucket, burn: settings.burn };

  if (kinds.length === 0) {
    return (
      <p className="py-20 text-center text-pretty text-muted-foreground text-sm" data-analytics-empty="">
        Nothing recorded yet: usage shows up here once agents finish turns, and plan limits once an agent reports them.
      </p>
    );
  }

  return (
    <div className="flex flex-col gap-6" data-analytics="">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <div className={segmentTrack()} role="tablist" aria-label="Agents">
          {["all", ...kinds].map((k) => (
            <button key={k} type="button" role="tab" aria-selected={tab === k} onClick={() => set({ tab: k })} className={segmentItem({ on: tab === k })}>
              {k !== "all" && <AgentGlyph kind={k} mark={kindMark(theme, k)} className="w-auto text-xs" />}
              {k === "all" ? "All" : kindName(k)}
            </button>
          ))}
        </div>
        <div className="flex items-center gap-1.5">
          <span className="text-2xs text-faint">Burn rate</span>
          <div className={segmentTrack()} role="radiogroup" aria-label="Burn rate over">
            {BURNS.map((b) => (
              <button key={b} type="button" role="radio" aria-checked={settings.burn === b} onClick={() => set({ burn: b })} className={segmentItem({ on: settings.burn === b })}>
                {b}h
              </button>
            ))}
          </div>
        </div>
        <div className="ml-auto flex items-center gap-1.5">
          <div className={segmentTrack()} role="radiogroup" aria-label="Span">
            {SPANS.map((s) => (
              <button
                key={s.id}
                type="button"
                role="radio"
                aria-checked={span.id === s.id}
                onClick={() => {
                  set({ span: s.id });
                  setOffset(0);
                }}
                className={segmentItem({ on: span.id === s.id })}
              >
                {s.label}
              </button>
            ))}
          </div>
          <span className="min-w-28 text-center text-muted-foreground text-xs tabular-nums" data-range="">
            {span.ms <= DAY ? `${dayHour(from)} – ${dayHour(to)}` : `${shortDate(from)} – ${shortDate(to)}`}
          </span>
          <Button variant="ghost-muted" size="icon-sm" aria-label="Earlier" title="Earlier" disabled={from <= oldest} onClick={() => setOffset((o) => o + 1)}>
            <ChevronRight className="rotate-180" />
          </Button>
          <Button variant="outline" size="xs" disabled={offset === 0} onClick={() => setOffset(0)}>
            Today
          </Button>
          <Button variant="ghost-muted" size="icon-sm" aria-label="Later" title="Later" disabled={offset === 0} onClick={() => setOffset((o) => Math.max(0, o - 1))}>
            <ChevronRight />
          </Button>
        </div>
      </div>

      {shown.map((k) => (
        <KindSection
          key={k}
          kind={k}
          theme={theme}
          data={data}
          windows={windows.filter((w) => w.kind === k)}
          view={view}
          detail={tab !== "all"}
          {...(api === undefined ? {} : { limits: <KindLimitControls api={api} kind={k} onDone={onLimitsDone ?? (() => {})} /> })}
        />
      ))}
      <p className="text-pretty text-2xs text-faint">
        Remaining quota is what each agent's CLI reported; Deck stores a point when it moves. Projections assume the burn rate of the last {settings.burn}h holds and are
        drawn dashed. Agents that report no plan limits show their usage only.
      </p>
    </div>
  );
}
