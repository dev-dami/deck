import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import { BarChart3, Loader2, RefreshCw, Table2 } from "lucide-react";
import { type ReactNode, useCallback, useEffect, useState } from "react";
import { cn } from "../lib/cn";
import type { ProjectApi, Usage, UsageDay } from "../project";
import { formatDuration } from "../work";
import { Button } from "./kit/button";

// Deck's local record of what the agents used and how you work, after T3
// Code's usage page. Nothing here leaves the machine.

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

const dayLabel = (ms: number) => new Date(ms).toLocaleDateString([], { month: "short", day: "numeric" });

function Section({ title, action, children }: { title: string; action?: ReactNode; children: ReactNode }) {
  return (
    <section className="space-y-2.5">
      <div className="flex min-h-7 items-center justify-between px-1">
        <h2 className="text-foreground/70 text-sm">{title}</h2>
        {action}
      </div>
      <div className="rounded-xl border border-border/60 bg-card/40 p-4">{children}</div>
    </section>
  );
}

function Tile({ label, value, detail }: { label: string; value: string; detail?: string }) {
  return (
    <div className="flex flex-col gap-1 rounded-xl border border-border/60 bg-card/40 px-4 py-3" data-tile={label}>
      <span className="text-muted-foreground text-xs">{label}</span>
      <span className="font-semibold text-2xl tabular-nums tracking-tight">{value}</span>
      {detail !== undefined && <span className="text-muted-foreground text-xs tabular-nums">{detail}</span>}
    </div>
  );
}

// Tokens per day: one series, one hue; hover a bar for its numbers.
function DailyBars({ days }: { days: UsageDay[] }) {
  const [hover, setHover] = useState<number | null>(null);
  const max = Math.max(1, ...days.map((d) => d.tokens));
  const h = days[hover ?? -1];
  return (
    <div className="relative">
      <div className="flex h-40 items-end gap-[2px]" role="img" aria-label="Tokens per day">
        {days.map((d, i) => (
          <div
            key={d.day}
            className="flex h-full min-w-0 flex-1 cursor-default items-end"
            onMouseEnter={() => setHover(i)}
            onMouseLeave={() => setHover(null)}
          >
            <div
              className={cn("w-full rounded-t-[4px] bg-primary transition-opacity", hover !== null && hover !== i && "opacity-50")}
              style={{ height: d.tokens === 0 ? 0 : `${Math.max(2, (d.tokens / max) * 100)}%` }}
            />
          </div>
        ))}
      </div>
      <div className="mt-2 flex justify-between text-2xs text-muted-foreground tabular-nums">
        <span>{days[0] === undefined ? "" : dayLabel(days[0].day)}</span>
        <span>{days.length > 1 ? dayLabel(days[days.length - 1]!.day) : ""}</span>
      </div>
      {h !== undefined && (
        <div className="pointer-events-none absolute top-0 right-0 rounded-lg border border-hairline-strong bg-surface-2 px-3 py-2 text-xs shadow-popover" role="status">
          <div className="font-medium">{dayLabel(h.day)}</div>
          <div className="text-muted-foreground tabular-nums">
            {compact(h.tokens)} tokens · {h.turns} turns · {dollars(h.costUsd)}
          </div>
        </div>
      )}
    </div>
  );
}

function DailyTable({ days }: { days: UsageDay[] }) {
  return (
    <table className="w-full text-xs tabular-nums">
      <thead className="text-muted-foreground">
        <tr className="text-left">
          <th className="py-1 font-normal">Day</th>
          <th className="py-1 text-right font-normal">Turns</th>
          <th className="py-1 text-right font-normal">Tokens</th>
          <th className="py-1 text-right font-normal">Cost</th>
        </tr>
      </thead>
      <tbody>
        {[...days].reverse().map((d) => (
          <tr key={d.day} className="border-border/50 border-t">
            <td className="py-1">{dayLabel(d.day)}</td>
            <td className="py-1 text-right">{d.turns}</td>
            <td className="py-1 text-right">{compact(d.tokens)}</td>
            <td className="py-1 text-right">{dollars(d.costUsd)}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

// One hue, light to dark, for counts in a grid; an empty cell is the muted
// surface.
function heatClass(n: number, max: number): string {
  if (n <= 0 || max <= 0) return "bg-muted";
  const step = Math.min(4, Math.ceil((n / max) * 4));
  return ["", "bg-primary/25", "bg-primary/45", "bg-primary/70", "bg-primary"][step] ?? "bg-primary";
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
              className={cn("size-3 rounded-[3px]", heatClass(d.turns, max))}
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
          <span className="w-8 text-2xs text-muted-foreground">{wd}</span>
          {Array.from({ length: 24 }, (_, h) => {
            const n = hours[d * 24 + h] ?? 0;
            return <div key={h} className={cn("h-3 min-w-0 flex-1 rounded-[3px]", heatClass(n, max))} title={`${wd} ${h}:00 · ${n} turns`} />;
          })}
        </div>
      ))}
      <div className="flex justify-between pl-9 text-2xs text-muted-foreground">
        <span>0:00</span>
        <span>12:00</span>
        <span>23:00</span>
      </div>
    </div>
  );
}

function Meter({ pct }: { pct: number }) {
  const p = Math.max(0, Math.min(100, pct));
  return (
    <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
      <div
        className={cn("h-full rounded-full", p >= 90 ? "bg-destructive" : p >= 70 ? "bg-warning" : "bg-primary")}
        style={{ width: `${p}%` }}
      />
    </div>
  );
}

function Stat({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-baseline justify-between gap-4 py-1.5 text-sm">
      <span className="text-muted-foreground">{label}</span>
      <span className="tabular-nums">{value}</span>
    </div>
  );
}

export function UsagePage({ project, theme, onOpenThread }: { project: ProjectApi | undefined; theme: Theme | null; onOpenThread: (id: string) => void }) {
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
    <header className="topbar drag flex h-12 shrink-0 items-center gap-2 border-b px-5 text-sm">
      <BarChart3 className="size-4 text-muted-foreground" />
      <h1 className="title font-medium">Usage</h1>
      <div className="ml-auto flex items-center gap-1">
        <div className="inline-flex rounded-lg border bg-background p-0.5" role="radiogroup" aria-label="Range">
          {RANGES.map((r) => (
            <button
              key={r.days}
              type="button"
              role="radio"
              aria-checked={days === r.days}
              onClick={() => setDays(r.days)}
              className={cn(
                "flex h-7 cursor-pointer items-center rounded-md px-2.5 text-xs",
                days === r.days ? "bg-accent font-medium text-foreground" : "text-muted-foreground hover:text-foreground",
              )}
            >
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
    body = <p className="py-20 text-center text-muted-foreground text-sm">Usage is kept by a Deck on this computer. Open a project folder to see it.</p>;
  } else if (data === null) {
    body = (
      <p className="flex items-center justify-center gap-2 py-20 text-muted-foreground text-sm">
        <Loader2 className="size-4 animate-spin" /> Reading the usage record…
      </p>
    );
  } else if (typeof data === "string") {
    body = <p className="py-20 text-center text-destructive-foreground text-sm">{data}</p>;
  } else if (!data.available) {
    body = <p className="py-20 text-center text-muted-foreground text-sm">This Deck keeps no usage record.</p>;
  } else {
    const o = data.overview;
    const tokens = o.inputTokens + o.outputTokens;
    const maxKind = Math.max(1, ...data.kinds.map((k) => k.inputTokens + k.outputTokens));
    const h = data.habits;
    body = (
      <>
        {o.turns === 0 && (
          <p className="rounded-xl border border-dashed px-4 py-3 text-muted-foreground text-sm">
            {o.allTurns === 0 ? "Nothing yet: usage shows up once agents finish turns." : "No turns in this range."}
          </p>
        )}
        <div className="grid grid-cols-2 gap-3 lg:grid-cols-4">
          <Tile label="Tokens" value={compact(tokens)} detail={`${compact(o.inputTokens)} in · ${compact(o.outputTokens)} out`} />
          <Tile label="Cost" value={dollars(o.costUsd)} detail="as the agents' CLIs reported it" />
          <Tile label="Turns" value={String(o.turns)} detail={`${o.interrupted} stopped · ${o.failed} failed`} />
          <Tile label="Cache hits" value={`${Math.round(o.cacheHitPct)}%`} detail={`first token in ${formatDuration(o.ttftAvgMs)} on average`} />
        </div>
        <Section
          title="Tokens per day"
          action={
            <Button variant="ghost-muted" size="xs" aria-pressed={table} onClick={() => setTable((t) => !t)}>
              {table ? <BarChart3 /> : <Table2 />}
              {table ? "Chart" : "Table"}
            </Button>
          }
        >
          {table ? <DailyTable days={data.days} /> : <DailyBars days={data.days} />}
        </Section>
        <div className="grid gap-6 lg:grid-cols-2">
          <Section title="By agent">
            <div className="flex flex-col gap-3">
              {data.kinds.length === 0 && <p className="text-muted-foreground text-sm">No agents in this range.</p>}
              {data.kinds.map((k) => {
                const t = k.inputTokens + k.outputTokens;
                return (
                  <div key={k.kind} className="flex flex-col gap-1">
                    <div className="flex items-center gap-2 text-sm">
                      <span className="font-mono text-xs" style={{ color: kindColour(k.kind) }}>
                        {kindMark(theme, k.kind) || "●"}
                      </span>
                      <span className="flex-1">{k.kind}</span>
                      <span className="text-muted-foreground text-xs tabular-nums">
                        {compact(t)} · {k.turns} turns · {dollars(k.costUsd)}
                      </span>
                    </div>
                    <div className="h-1.5 w-full overflow-hidden rounded-full bg-muted">
                      <div className="h-full rounded-full" style={{ width: `${(t / maxKind) * 100}%`, background: kindColour(k.kind) }} />
                    </div>
                  </div>
                );
              })}
            </div>
          </Section>
          <Section title="Plan limits">
            {data.limits.length === 0 ? (
              <p className="text-muted-foreground text-sm">No agent has reported its plan limits yet.</p>
            ) : (
              <div className="flex flex-col gap-3">
                {data.limits.map((l) => (
                  <div key={`${l.agentKind}/${l.windowId}`} className="flex flex-col gap-1">
                    <div className="flex items-center gap-2 text-sm">
                      <span className="flex-1">
                        {l.agentKind} <span className="text-muted-foreground">· {l.label || l.kind}</span>
                      </span>
                      <span className="text-xs tabular-nums">{Math.round(l.usedPct)}% used</span>
                    </div>
                    <Meter pct={l.usedPct} />
                    {l.resetsAt > 0 && (
                      <span className="text-2xs text-muted-foreground">
                        Resets {new Date(l.resetsAt).toLocaleString([], { weekday: "short", hour: "numeric", minute: "2-digit" })}
                      </span>
                    )}
                  </div>
                ))}
              </div>
            )}
          </Section>
        </div>
        <Section title="Models">
          <table className="w-full text-sm tabular-nums">
            <thead className="text-muted-foreground text-xs">
              <tr className="text-left">
                <th className="pb-2 font-normal">Model</th>
                <th className="pb-2 text-right font-normal">Turns</th>
                <th className="pb-2 text-right font-normal">Input</th>
                <th className="pb-2 text-right font-normal">Output</th>
                <th className="pb-2 text-right font-normal">Cached</th>
                <th className="pb-2 text-right font-normal">Cost</th>
              </tr>
            </thead>
            <tbody>
              {data.models.map((m) => (
                <tr key={`${m.kind}/${m.model}`} className="border-border/50 border-t">
                  <td className="py-1.5">
                    <span className="mr-1.5 font-mono text-xs" style={{ color: kindColour(m.kind) }}>
                      {kindMark(theme, m.kind) || "●"}
                    </span>
                    {m.model || `${m.kind} (default)`}
                  </td>
                  <td className="py-1.5 text-right">{m.turns}</td>
                  <td className="py-1.5 text-right">{compact(m.inputTokens)}</td>
                  <td className="py-1.5 text-right">{compact(m.outputTokens)}</td>
                  <td className="py-1.5 text-right">{compact(m.cachedTokens)}</td>
                  <td className="py-1.5 text-right">{dollars(m.costUsd)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </Section>
        {data.sessions.length > 0 && (
          <Section title="Costliest threads">
            <ul className="flex flex-col">
              {data.sessions.map((x) => (
                <li key={x.sessionId}>
                  <button
                    type="button"
                    className="flex w-full cursor-pointer items-center gap-3 rounded-md px-2 py-1.5 text-left text-sm hover:bg-accent disabled:cursor-default"
                    disabled={x.title === ""}
                    onClick={() => onOpenThread(x.sessionId)}
                  >
                    <span className="min-w-0 flex-1 truncate">{x.title || "Deleted thread"}</span>
                    <span className="text-muted-foreground text-xs tabular-nums">
                      {x.turns} turns · {compact(x.tokens)} · {dollars(x.costUsd)}
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          </Section>
        )}
        <Section title="Activity, last 20 weeks">
          <Heat heat={data.heat} />
        </Section>
        {h !== null && (
          <>
            <Section title="When you work">
              <Hours hours={h.hours} />
            </Section>
            <div className="grid gap-6 lg:grid-cols-2">
              <Section title="How you work">
                <div className="divide-y divide-border/50">
                  <Stat label="Active days" value={`${h.activeDays} · longest streak ${h.streak}`} />
                  <Stat label="Time to review a reply" value={`${formatDuration(h.reviewMedianMs)} median · ${formatDuration(h.reviewP90Ms)} p90`} />
                  <Stat label="Time to answer an approval" value={`${formatDuration(h.approvalMedianMs)} median · ${Math.round(h.approvalFastPct)}% under 1s`} />
                  <Stat label="Follow-ups" value={`${h.followUps} of ${h.userTurns} messages`} />
                  <Stat label="Redirects after a stop" value={String(h.redirects)} />
                  <Stat label="Typical message" value={`${h.promptMedian} characters`} />
                </div>
              </Section>
              <Section title="What the agents changed">
                <div className="divide-y divide-border/50">
                  <Stat label="Turns that edited files" value={String(h.editTurns)} />
                  <Stat label="Files changed" value={String(h.editFiles)} />
                  <Stat label="Lines" value={`+${compact(h.linesAdded)} −${compact(h.linesRemoved)}`} />
                  <Stat label="Changes taken back" value={String(h.reverts)} />
                  {h.fit.slice(0, 4).map((f) => (
                    <Stat key={`${f.kind}/${f.class}`} label={`${f.kind}: ${f.class} tools`} value={`${f.calls} calls`} />
                  ))}
                </div>
              </Section>
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
        <div className="mx-auto flex w-full max-w-5xl flex-col gap-6 px-6 py-8">{body}</div>
      </div>
    </div>
  );
}
