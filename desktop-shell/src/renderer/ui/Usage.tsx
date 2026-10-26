import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { BarChart3, Loader2, RefreshCw, Table2 } from "./icons";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import { cn } from "../lib/cn";
import type { ProjectApi, Usage, UsageDay } from "../project";
import { formatDuration } from "../work";
import { Button } from "./kit/button";
import { EmptyPlate } from "./kit/plate";
import { PLATES } from "../plates";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { AgentGlyph } from "./Welcome";

// Deck's local record of what the agents used and how you work, after T3
// Code's usage page. Nothing here leaves the machine.
//
// The charts keep to a few rules (the dataviz method): one series wears the
// accent and one axis; an agent wears its own hue wherever it shows; counts
// in a grid are one hue, light to dark, with a scale; text keeps the text
// colours, and a coloured mark beside it carries identity. Marks are thin,
// with 4px rounded ends on a hairline baseline, and every chart has its
// numbers on hover or in a table.

const RANGES = [
  { days: 1, label: "Today" },
  { days: 7, label: "7 days" },
  { days: 30, label: "30 days" },
  { days: 0, label: "All time" },
] as const;

export function compact(n: number): string {
  if (n >= 1e9) return `${(n / 1e9).toFixed(1)}B`;
  if (n >= 1e6) return `${(n / 1e6).toFixed(1)}M`;
  if (n >= 1e3) return `${(n / 1e3).toFixed(1)}k`;
  return String(Math.round(n));
}

export function dollars(n: number): string {
  return n >= 100 ? `$${n.toFixed(0)}` : `$${n.toFixed(2)}`;
}

// An axis's top: the next clean number at or above max (1, 2, 2.5 or 5 by
// a power of ten), so the ticks between read round.
export function niceCeil(max: number): number {
  if (max <= 0) return 1;
  const p = 10 ** Math.floor(Math.log10(max));
  for (const m of [1, 2, 2.5, 5, 10]) if (m * p >= max) return m * p;
  return 10 * p;
}

// A tick's label: compact, without a needless ".0".
const tick = (n: number) => compact(n).replace(/\.0(?=[kMB]?$)/, "");

const dayLabel = (ms: number) => new Date(ms).toLocaleDateString([], { month: "short", day: "numeric" });

// A part of the page: its title, a line under it, and an action at the
// trailing edge, then what it shows. No box: the parts stand apart by
// space, and the title leads.
function Card({
  title,
  sub,
  action,
  className,
  children,
}: {
  title: string;
  sub?: ReactNode;
  action?: ReactNode;
  className?: string;
  children: ReactNode;
}) {
  return (
    <section aria-label={title} className={cn("flex min-w-0 flex-col gap-4", className)}>
      <header className="flex items-end gap-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 className="font-semibold text-[13px]">{title}</h2>
          {sub !== undefined && <p className="text-2xs text-faint">{sub}</p>}
        </div>
        {action}
      </header>
      <div className="flex-1">{children}</div>
    </section>
  );
}

// A trend in a tile: the days as a thin line in the quiet ink, the last
// day as a dot of the accent. The line stretches to the tile; the dot is
// drawn over it, so it stays round.
function Spark({ values }: { values: number[] }) {
  if (values.length < 2) return null;
  const max = Math.max(1, ...values);
  const h = 24;
  const y = (v: number) => h - 3 - (v / max) * (h - 6);
  const pts = values.map((v, i) => `${(i / (values.length - 1)) * 100},${y(v)}`).join(" ");
  return (
    <div aria-hidden className="relative h-6">
      <svg viewBox={`0 0 100 ${h}`} preserveAspectRatio="none" className="size-full overflow-visible">
        <polyline
          points={pts}
          fill="none"
          stroke="currentColor"
          strokeWidth="1.5"
          strokeLinejoin="round"
          strokeLinecap="round"
          vectorEffect="non-scaling-stroke"
          className="text-faint/70"
        />
      </svg>
      <span
        className="absolute right-0 size-2 translate-x-1/2 -translate-y-1/2 rounded-full bg-primary ring-2 ring-background"
        style={{ top: `${(y(values[values.length - 1]!) / h) * 100}%` }}
      />
    </div>
  );
}

// A stat tile: the label, the figure in proportional digits, what makes it
// up, and the days' trend.
function Tile({ label, value, detail, trend }: { label: string; value: string; detail?: string; trend?: ReactNode }) {
  return (
    <div className="flex min-w-0 flex-col gap-1" data-tile={label}>
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="font-semibold text-[2rem] leading-tight tracking-[-0.03em] [font-variant-numeric:normal]">{value}</span>
      {detail !== undefined && <span className="truncate text-2xs text-faint tabular-nums">{detail}</span>}
      {trend !== undefined && <div className="mt-1.5">{trend}</div>}
    </div>
  );
}

// Tokens per day: one series in the accent, on one axis with three round
// ticks. Hover a column for its day's numbers; the others step back.
function DailyBars({ days }: { days: UsageDay[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const top = niceCeil(Math.max(0, ...days.map((d) => d.tokens)));
  const h = hover === null ? undefined : days[hover];
  const ticks = [top, top / 2, 0];
  const mid = days[Math.floor((days.length - 1) / 2)];
  return (
    <div className="flex gap-3">
      <div className="flex h-44 w-9 shrink-0 flex-col justify-between text-right text-3xs text-faint tabular-nums" aria-hidden>
        {ticks.map((t) => (
          <span key={t} className="-translate-y-1/2 leading-none last:translate-y-1/2">
            {tick(t)}
          </span>
        ))}
      </div>
      <div className="relative min-w-0 flex-1">
        {/* Hairline gridlines at the ticks, solid and recessive. */}
        <div aria-hidden className="pointer-events-none absolute inset-x-0 top-0 flex h-44 flex-col justify-between">
          {ticks.map((t) => (
            <span key={t} className={cn("h-px", t === 0 ? "bg-hairline-strong" : "bg-hairline")} />
          ))}
        </div>
        <div className="relative flex h-44 items-end" role="img" aria-label="Tokens per day" onMouseLeave={() => setHover(null)}>
          {days.map((d, i) => (
            <div
              key={d.day}
              className="flex h-full min-w-0 flex-1 cursor-default items-end justify-center px-px"
              onMouseEnter={() => setHover(i)}
            >
              <div
                className={cn(
                  "w-full max-w-[18px] rounded-t-[4px] bg-primary transition-opacity duration-150",
                  hover !== null && hover !== i && "opacity-35",
                )}
                style={{ height: d.tokens === 0 ? 0 : `${Math.max(1.5, (d.tokens / top) * 100)}%` }}
              />
            </div>
          ))}
          {h !== undefined && hover !== null && (
            <div
              className="material pointer-events-none absolute bottom-full z-10 mb-2 w-max -translate-x-1/2 rounded-[10px] px-3 py-2 text-xs"
              style={{ left: `clamp(4.5rem, ${((hover + 0.5) / days.length) * 100}%, calc(100% - 4.5rem))` }}
              role="status"
            >
              <div className="font-medium">{dayLabel(h.day)}</div>
              <div className="text-muted-foreground tabular-nums">
                {compact(h.tokens)} tokens · {h.turns} turns · {dollars(h.costUsd)}
              </div>
            </div>
          )}
        </div>
        <div className="mt-2 flex justify-between text-2xs text-faint tabular-nums" aria-hidden>
          <span>{days[0] === undefined ? "" : dayLabel(days[0].day)}</span>
          {days.length > 2 && mid !== undefined && <span>{dayLabel(mid.day)}</span>}
          <span>{days.length > 1 ? dayLabel(days[days.length - 1]!.day) : ""}</span>
        </div>
      </div>
    </div>
  );
}

const th = "pb-2 font-normal text-2xs text-faint";
const td = "py-2";

function DailyTable({ days }: { days: UsageDay[] }) {
  return (
    <table className="w-full text-xs tabular-nums">
      <thead>
        <tr className="text-left">
          <th className={th}>Day</th>
          <th className={cn(th, "text-right")}>Turns</th>
          <th className={cn(th, "text-right")}>Tokens</th>
          <th className={cn(th, "text-right")}>Cost</th>
        </tr>
      </thead>
      <tbody>
        {[...days].reverse().map((d) => (
          <tr key={d.day} className="border-hairline border-t">
            <td className={td}>{dayLabel(d.day)}</td>
            <td className={cn(td, "text-right text-muted-foreground")}>{d.turns}</td>
            <td className={cn(td, "text-right text-muted-foreground")}>{compact(d.tokens)}</td>
            <td className={cn(td, "text-right")}>{dollars(d.costUsd)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// One hue, light to dark, for counts in a grid; an empty cell is a quiet
// step of the surface.
const HEAT = ["bg-selected", "bg-primary/25", "bg-primary/45", "bg-primary/70", "bg-primary"] as const;
function heatClass(n: number, max: number): string {
  if (n <= 0 || max <= 0) return HEAT[0];
  return HEAT[Math.min(4, Math.ceil((n / max) * 4))] ?? HEAT[4];
}

// The heat's scale, from none to the most.
function HeatScale() {
  return (
    <span className="flex items-center gap-1 text-2xs text-faint" aria-hidden>
      Less
      {HEAT.map((c) => (
        <span key={c} className={cn("size-2.5 rounded-[3px]", c)} />
      ))}
      More
    </span>
  );
}

function Heat({ heat }: { heat: UsageDay[] }) {
  const max = Math.max(0, ...heat.map((d) => d.turns));
  const weeks: UsageDay[][] = [];
  for (let i = 0; i < heat.length; i += 7) weeks.push(heat.slice(i, i + 7));
  return (
    <div className="flex gap-[3px] overflow-x-auto" role="img" aria-label="Turns per day, by week">
      {weeks.map((w, i) => (
        <div key={i} className="flex flex-col gap-[3px]">
          {w.map((d) => (
            <div
              key={d.day}
              className={cn("size-3.5 rounded-[3px]", heatClass(d.turns, max))}
              title={`${dayLabel(d.day)}: ${d.turns} turns, ${compact(d.tokens)} tokens`}
            />
          ))}
        </div>
      ))}
    </div>
  );
}

const WEEKDAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"];

function Hours({ hours }: { hours: number[] }) {
  const max = Math.max(0, ...hours);
  return (
    <div className="flex flex-col gap-[3px]" role="img" aria-label="Turns by weekday and hour">
      {WEEKDAYS.map((wd, d) => (
        <div key={wd} className="flex items-center gap-[3px]">
          <span className="w-8 shrink-0 text-2xs text-faint">{wd}</span>
          {Array.from({ length: 24 }, (_, h) => {
            const n = hours[d * 24 + h] ?? 0;
            return <div key={h} className={cn("h-3.5 min-w-0 flex-1 rounded-[3px]", heatClass(n, max))} title={`${wd} ${h}:00 · ${n} turns`} />;
          })}
        </div>
      ))}
      <div className="flex justify-between pt-1 pl-[2.2rem] text-2xs text-faint tabular-nums">
        <span>0:00</span>
        <span>6:00</span>
        <span>12:00</span>
        <span>18:00</span>
        <span>23:00</span>
      </div>
    </div>
  );
}

// A share of a whole: the fill in its colour over a lighter step of the
// same colour, with 4px rounded ends.
function Bar({ pct, colour }: { pct: number; colour: string }) {
  const p = Math.max(0, Math.min(100, pct));
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full" style={{ background: `color-mix(in srgb, ${colour} 16%, transparent)` }}>
      <div className="h-full rounded-full" style={{ width: `${p}%`, background: colour, minWidth: p > 0 ? 6 : 0 }} />
    </div>
  );
}

// A plan limit: the accent, then the warning past 70% and the destructive
// colour past 90%; the fill's severity carries over the whole bar.
function Meter({ pct }: { pct: number }) {
  return <Bar pct={pct} colour={pct >= 90 ? "var(--destructive)" : pct >= 70 ? "var(--warning)" : "var(--primary)"} />;
}

function Stat({ label, value }: { label: string; value: ReactNode }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-2 text-[13px]">
      <span className="text-muted-foreground">{label}</span>
      <span className="text-right tabular-nums">{value}</span>
    </div>
  );
}

export function UsagePage({
  project,
  theme,
  onOpenThread,
  onAddProject,
}: {
  project: ProjectApi | undefined;
  theme: Theme | null;
  onOpenThread: (id: string) => void;
  // The empty page's next step, when the shell gives it.
  onAddProject?: () => void;
}) {
  const [days, setDays] = useState<number>(30);
  const [data, setData] = useState<Usage | string | null>(null);
  const [table, setTable] = useState(false);
  const load = useCallback(async () => {
    if (project === undefined) return;
    setData(null);
    setData(await project.usage(days));
  }, [project, days]);
  useEffect(() => {
    void load();
  }, [load]);

  const header = (
    <header className="topbar drag flex h-13 shrink-0 items-center gap-2 px-5 text-sm">
      <BarChart3 className="size-4 text-muted-foreground" />
      <h1 className="title font-semibold">Usage</h1>
      <span className="hidden text-faint text-xs sm:inline">Kept on this computer</span>
      <div className="ml-auto flex items-center gap-1.5">
        <div className={segmentTrack()} role="radiogroup" aria-label="Range">
          {RANGES.map((r) => (
            <button key={r.days} type="button" role="radio" aria-checked={days === r.days} onClick={() => setDays(r.days)} className={segmentItem({ on: days === r.days })}>
              {r.label}
            </button>
          ))}
        </div>
        <Button variant="ghost-muted" size="icon-sm" aria-label="Refresh" title="Refresh" onClick={() => void load()}>
          <RefreshCw />
        </Button>
      </div>
    </header>
  );

  let body: ReactNode;
  if (project === undefined) {
    body = (
      <EmptyPlate
        art={PLATES.fuji}
        title="No usage here"
        hint="Usage is kept by a Deck on this computer. Open a project folder to see it."
        className="py-20"
        action={
          onAddProject !== undefined && (
            <Button variant="outline" size="sm" onClick={onAddProject}>
              Add project…
            </Button>
          )
        }
      />
    );
  } else if (data === null) {
    body = (
      <p className="flex items-center justify-center gap-2 py-24 text-muted-foreground text-sm">
        <Loader2 className="size-4 animate-spin" /> Reading the usage record…
      </p>
    );
  } else if (typeof data === "string") {
    body = <p className="py-24 text-center text-destructive-foreground text-sm">{data}</p>;
  } else if (!data.available) {
    body = <EmptyPlate art={PLATES.fuji} title="No usage record" hint="This Deck keeps no usage record." className="py-20" />;
  } else {
    const o = data.overview;
    const tokens = o.inputTokens + o.outputTokens;
    const kindTokens = data.kinds.map((k) => k.inputTokens + k.outputTokens);
    const allKinds = Math.max(1, kindTokens.reduce((a, b) => a + b, 0));
    const h = data.habits;
    const series = data.days.length > 1;
    const maxThread = Math.max(0.01, ...data.sessions.map((x) => x.costUsd));
    body = (
      <>
        {o.turns === 0 && (
          <p className="text-pretty text-muted-foreground text-sm">
            {o.allTurns === 0 ? "Nothing yet: usage shows up once agents finish turns." : "No turns in this range."}
          </p>
        )}
        <div className="grid grid-cols-2 gap-x-8 gap-y-6 border-hairline border-b pb-8 lg:grid-cols-4">
          <Tile
            label="Tokens"
            value={compact(tokens)}
            detail={`${compact(o.inputTokens)} in · ${compact(o.outputTokens)} out`}
            trend={series ? <Spark values={data.days.map((d) => d.tokens)} /> : undefined}
          />
          <Tile
            label="Cost"
            value={dollars(o.costUsd)}
            detail="as the agents' CLIs reported it"
            trend={series ? <Spark values={data.days.map((d) => d.costUsd)} /> : undefined}
          />
          <Tile
            label="Turns"
            value={String(o.turns)}
            detail={`${o.interrupted} stopped · ${o.failed} failed`}
            trend={series ? <Spark values={data.days.map((d) => d.turns)} /> : undefined}
          />
          <Tile
            label="Cache hits"
            value={`${Math.round(o.cacheHitPct)}%`}
            detail={`first token in ${formatDuration(o.ttftAvgMs)} on average`}
            trend={
              series ? (
                <div className="flex h-6 items-center">
                  <Bar pct={o.cacheHitPct} colour="var(--primary)" />
                </div>
              ) : undefined
            }
          />
        </div>
        <Card
          title="Tokens per day"
          sub={`${compact(tokens)} in this range`}
          action={
            <Button variant="ghost-muted" size="xs" aria-pressed={table} onClick={() => setTable((t) => !t)}>
              {table ? <BarChart3 /> : <Table2 />}
              {table ? "Chart" : "Table"}
            </Button>
          }
        >
          {table ? <DailyTable days={data.days} /> : <DailyBars days={data.days} />}
        </Card>
        <div className="grid gap-10 lg:grid-cols-2 lg:gap-12">
          <Card title="By agent" sub="Share of the tokens">
            <div className="flex flex-col gap-4">
              {data.kinds.length === 0 && <p className="text-muted-foreground text-sm">No agents in this range.</p>}
              {data.kinds.map((k, i) => {
                const t = kindTokens[i] ?? 0;
                return (
                  <div key={k.kind} className="flex items-center gap-3">
                    <AgentGlyph kind={k.kind} mark={kindMark(theme, k.kind)} className="text-[15px]" />
                    <div className="flex min-w-0 flex-1 flex-col gap-1.5">
                      <div className="flex items-baseline gap-2 text-[13px]">
                        <span className="font-medium">{k.kind}</span>
                        <span className="text-faint text-xs tabular-nums">{Math.round((t / allKinds) * 100)}%</span>
                        <span className="ml-auto text-muted-foreground text-xs tabular-nums">
                          {compact(t)} · {k.turns} turns · {dollars(k.costUsd)}
                        </span>
                      </div>
                      <Bar pct={(t / allKinds) * 100} colour={kindColour(k.kind)} />
                    </div>
                  </div>
                );
              })}
            </div>
          </Card>
          <Card title="Plan limits" sub="As each agent last reported them">
            {data.limits.length === 0 ? (
              <p className="text-pretty text-muted-foreground text-sm">No agent has reported its plan limits yet.</p>
            ) : (
              <div className="flex flex-col gap-4">
                {data.limits.map((l) => (
                  <div key={`${l.agentKind}/${l.windowId}`} className="flex flex-col gap-1.5">
                    <div className="flex items-baseline gap-2 text-[13px]">
                      <span className="flex-1">
                        <span className="font-medium">{l.agentKind}</span> <span className="text-muted-foreground">· {l.label || l.kind}</span>
                      </span>
                      <span className="text-xs tabular-nums">{Math.round(l.usedPct)}% used</span>
                    </div>
                    <Meter pct={l.usedPct} />
                    {l.resetsAt > 0 && (
                      <span className="text-2xs text-faint">
                        Resets {new Date(l.resetsAt).toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" })}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </Card>
        </div>
        <Card title="Models">
          <table className="w-full text-[13px] tabular-nums">
            <thead>
              <tr className="text-left">
                <th className={th}>Model</th>
                <th className={cn(th, "text-right")}>Turns</th>
                <th className={cn(th, "text-right")}>Input</th>
                <th className={cn(th, "text-right")}>Output</th>
                <th className={cn(th, "text-right")}>Cached</th>
                <th className={cn(th, "text-right")}>Cost</th>
              </tr>
            </thead>
            <tbody>
              {data.models.map((m) => (
                <tr key={`${m.kind}/${m.model}`} className="border-hairline border-t">
                  <td className={td}>
                    <span className="flex items-center gap-2">
                      <span aria-hidden className="size-2 shrink-0 rounded-full" style={{ background: kindColour(m.kind) }} />
                      {m.model || `${m.kind} (default)`}
                    </span>
                  </td>
                  <td className={cn(td, "text-right text-muted-foreground")}>{m.turns}</td>
                  <td className={cn(td, "text-right text-muted-foreground")}>{compact(m.inputTokens)}</td>
                  <td className={cn(td, "text-right text-muted-foreground")}>{compact(m.outputTokens)}</td>
                  <td className={cn(td, "text-right text-muted-foreground")}>{compact(m.cachedTokens)}</td>
                  <td className={cn(td, "text-right font-medium")}>{dollars(m.costUsd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Card>
        {data.sessions.length > 0 && (
          <Card title="Costliest threads">
            <ul className="-mx-2 flex flex-col [&>li+li]:border-hairline [&>li+li]:border-t">
              {data.sessions.map((x, i) => (
                <li key={x.sessionId}>
                  <button
                    type="button"
                    className="flex w-full cursor-pointer items-center gap-3 rounded-[10px] px-2 py-2 text-left text-[13px] outline-none transition-colors hover:bg-hover focus-visible:bg-selected disabled:cursor-default"
                    disabled={x.title === ""}
                    onClick={() => onOpenThread(x.sessionId)}
                  >
                    <span className="w-4 shrink-0 text-right text-2xs text-faint tabular-nums">{i + 1}</span>
                    <span className="flex min-w-0 flex-1 flex-col gap-1">
                      <span className={cn("min-w-0 truncate", x.title === "" && "text-faint")}>{x.title || "Deleted thread"}</span>
                      <span aria-hidden className="block h-1 rounded-full bg-primary/70" style={{ width: `${Math.max(2, (x.costUsd / maxThread) * 100)}%` }} />
                    </span>
                    <span className="shrink-0 text-faint text-xs tabular-nums">
                      {x.turns} turns · {compact(x.tokens)}
                    </span>
                    <span className="w-14 shrink-0 text-right font-medium tabular-nums">{dollars(x.costUsd)}</span>
                  </button>
                </li>
              ))}
            </ul>
          </Card>
        )}
        <div className="grid gap-10 lg:grid-cols-[auto_minmax(0,1fr)] lg:gap-12">
          <Card title="Activity" sub="Turns per day, the last 20 weeks">
            <div className="flex flex-col gap-3">
              <Heat heat={data.heat} />
              <HeatScale />
            </div>
          </Card>
          {h !== null && (
            <Card title="When you work" sub="Turns by weekday and hour">
              <Hours hours={h.hours} />
            </Card>
          )}
        </div>
        {h !== null && (
          <>
            <div className="grid gap-10 lg:grid-cols-2 lg:gap-12">
              <Card title="How you work">
                <div className="-mt-2 divide-y divide-hairline">
                  <Stat label="Active days" value={`${h.activeDays} · longest streak ${h.streak}`} />
                  <Stat label="Time to review a reply" value={`${formatDuration(h.reviewMedianMs)} median · ${formatDuration(h.reviewP90Ms)} p90`} />
                  <Stat label="Time to answer an approval" value={`${formatDuration(h.approvalMedianMs)} median · ${Math.round(h.approvalFastPct)}% under 1s`} />
                  <Stat label="Follow-ups" value={`${h.followUps} of ${h.userTurns} messages`} />
                  <Stat label="Redirects after a stop" value={String(h.redirects)} />
                  <Stat label="Typical message" value={`${h.promptMedian} characters`} />
                </div>
              </Card>
              <Card title="What the agents changed">
                <div className="-mt-2 divide-y divide-hairline">
                  <Stat label="Turns that edited files" value={String(h.editTurns)} />
                  <Stat label="Files changed" value={String(h.editFiles)} />
                  <Stat
                    label="Lines"
                    value={
                      <>
                        <span className="text-diff-add-fg">+{compact(h.linesAdded)}</span>{" "}
                        <span className="text-diff-del-fg">−{compact(h.linesRemoved)}</span>
                      </>
                    }
                  />
                  <Stat label="Changes taken back" value={String(h.reverts)} />
                  {h.fit.slice(0, 4).map((f) => (
                    <Stat key={`${f.kind}/${f.class}`} label={`${f.kind}: ${f.class} tools`} value={`${f.calls} calls`} />
                  ))}
                </div>
              </Card>
            </div>
          </>
        )}
      </>
    );
  }

  return (
    <div className="usage flex min-h-0 flex-1 flex-col" data-usage="">
      {header}
      <div className="min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-12 px-8 pt-4 pb-14">{body}</div>
      </div>
    </div>
  );
}
