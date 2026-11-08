import type { Session } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { toMillis } from "@web/time";
import { type CSSProperties, type ReactNode, useEffect, useMemo, useState } from "react";
import { cn } from "../lib/cn";
import type { ProjectApi, Usage, UsageDay, UsageLimit } from "../project";
import { BarChart3, Calendar, Clock, Gauge, Loader2, ShieldQuestion } from "./icons";
import { Tick } from "./kit/motion";
import { compact, dollars } from "./Usage";

// The new-thread screen's home, under the composer (docs/design-glass.md,
// section 10): what you and the agents have been doing, at a glance. Five
// cards of smoked glass over the picture: the activity of the last months
// as a grid of days, the threads you touched last, what the plan's windows
// have used, the threads working or waiting on you now, and this month
// against the last. A folder on this computer reads Deck's local usage
// record; a paired Deck has only its threads, and the cards say so.

// The weeks the activity grid shows, ending with this one.
const WEEKS = 24;
const DAY_MS = 86_400_000;

export interface HomeProps {
  // The project's own usage record: a folder on this computer.
  api: ProjectApi | undefined;
  // The project's threads (top-level, no side chats).
  sessions: readonly Session[];
  onOpen: (id: string) => void;
}

export function Home({ api, sessions, onOpen }: HomeProps) {
  const usage = useHomeUsage(api);
  const now = useNow();
  const recent = useMemo(() => [...sessions].sort((a, b) => toMillis(b.updated_at) - toMillis(a.updated_at)), [sessions]);
  const live = recent.filter((s) => s.busy || s.pending > 0);
  return (
    <section className="home grid grid-cols-2 gap-2 pt-2 @[34rem]/new:grid-cols-4" aria-label="Overview">
      <HomeCard className="col-span-2" title="Activity" icon={<Calendar />}>
        <Activity usage={usage} sessions={sessions} now={now} />
      </HomeCard>
      <HomeCard className="col-span-2" title="Recent threads" icon={<Clock />}>
        <Recent rows={recent.slice(0, 3)} more={recent.length - 3} now={now} onOpen={onOpen} empty="No threads yet. The first one starts above." />
      </HomeCard>
      <HomeCard className="col-span-1" title="Usage" icon={<Gauge />}>
        <UsageStat usage={usage} local={api !== undefined} now={now} />
      </HomeCard>
      <HomeCard className="col-span-2 @max-[34rem]/new:order-last" title="In progress" icon={<Loader2 />}>
        <Recent rows={live.slice(0, 3)} more={live.length - 3} now={now} onOpen={onOpen} empty="Nothing running. Threads at work or waiting on you show here." status />
      </HomeCard>
      <HomeCard className="col-span-1" title="This month" icon={<BarChart3 />}>
        <Month usage={usage} sessions={sessions} now={now} />
      </HomeCard>
    </section>
  );
}

function HomeCard({ title, icon, className, children }: { title: string; icon: ReactNode; className?: string; children: ReactNode }) {
  return (
    <div className={cn("home-card liquid flex min-w-0 flex-col gap-2.5 rounded-(--radius) p-3", className)} role="group" aria-label={title}>
      <h3 className="flex items-center gap-1.5 font-normal text-muted-foreground text-xs [&>svg]:size-3.5 [&>svg]:shrink-0">
        {icon}
        {title}
      </h3>
      {children}
    </div>
  );
}

// The usage record of the last two months, read once and every few
// minutes after; null until it arrives, or for a paired Deck.
function useHomeUsage(api: ProjectApi | undefined): Usage | null {
  const [usage, setUsage] = useState<Usage | null>(null);
  useEffect(() => {
    if (api === undefined) {
      setUsage(null);
      return;
    }
    let live = true;
    const load = () =>
      void api
        .usage(62)
        .then((u) => live && typeof u !== "string" && u.available && setUsage(u))
        .catch(() => {});
    load();
    const t = window.setInterval(load, 180_000);
    return () => {
      live = false;
      window.clearInterval(t);
    };
  }, [api]);
  return usage;
}

// The time, a minute at a time, for the "ago"s.
function useNow(): number {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = window.setInterval(() => setNow(Date.now()), 60_000);
    return () => window.clearInterval(t);
  }, []);
  return now;
}

const startOfDay = (ms: number) => {
  const d = new Date(ms);
  d.setHours(0, 0, 0, 0);
  return d.getTime();
};
const dayKey = (ms: number) => new Date(ms).toDateString();

// How long ago, in words: "just now", "5 min ago", "8 days ago", "last month".
export function ago(ms: number, now: number): string {
  const s = Math.max(0, now - ms) / 1000;
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86_400) return `${Math.floor(s / 3600)} h ago`;
  const days = Math.floor(s / 86_400);
  if (days === 1) return "yesterday";
  if (days < 30) return `${days} days ago`;
  if (days < 60) return "last month";
  return `${Math.floor(days / 30)} months ago`;
}

// One hue, light to dark, for counts in a grid (Usage.tsx's scale); an
// empty day is a quiet step of the text.
const HEAT = ["bg-[color-mix(in_srgb,var(--text)_7%,transparent)]", "bg-primary/30", "bg-primary/50", "bg-primary/75", "bg-primary"] as const;
function heat(n: number, max: number): string {
  if (n <= 0 || max <= 0) return HEAT[0];
  return HEAT[Math.min(4, Math.ceil((n / max) * 4))] ?? HEAT[4];
}

// Days by week, Monday at the top, this week last: turns from the usage
// record, or for a paired Deck the threads last touched each day.
export function activityGrid(days: readonly UsageDay[] | null, sessions: readonly Session[], now: number, weeks = WEEKS): { day: number; n: number }[][] {
  const counts = new Map<string, number>();
  if (days !== null) for (const d of days) counts.set(dayKey(d.day), (counts.get(dayKey(d.day)) ?? 0) + d.turns);
  else
    for (const s of sessions) {
      const k = dayKey(toMillis(s.updated_at));
      counts.set(k, (counts.get(k) ?? 0) + 1);
    }
  const today = startOfDay(now);
  const monday = today - ((new Date(today).getDay() + 6) % 7) * DAY_MS;
  const first = monday - (weeks - 1) * 7 * DAY_MS;
  const grid: { day: number; n: number }[][] = [];
  for (let w = 0; w < weeks; w++) {
    const col: { day: number; n: number }[] = [];
    for (let d = 0; d < 7; d++) {
      // Noon, so a change of the clocks never lands two cells on one day.
      const day = first + (w * 7 + d) * DAY_MS + DAY_MS / 2;
      col.push({ day, n: day > now ? -1 : (counts.get(dayKey(day)) ?? 0) });
    }
    grid.push(col);
  }
  return grid;
}

const month = (ms: number) => new Date(ms).toLocaleDateString([], { month: "short" });
const date = (ms: number) => new Date(ms).toLocaleDateString([], { month: "short", day: "numeric" });

function Activity({ usage, sessions, now }: { usage: Usage | null; sessions: readonly Session[]; now: number }) {
  const grid = useMemo(() => activityGrid(usage?.heat ?? null, sessions, now), [usage, sessions, now]);
  const max = Math.max(0, ...grid.flat().map((c) => c.n));
  const first = grid[0]?.[0]?.day ?? now;
  const turns = usage?.heat.reduce((t, d) => t + d.turns, 0) ?? null;
  const tokens = usage?.heat.reduce((t, d) => t + d.tokens, 0) ?? null;
  const unit = usage === null ? "thread" : "turn";
  return (
    <>
      <div className="flex gap-[3px]" role="img" aria-label={`${unit === "turn" ? "Turns" : "Threads"} per day, the last ${WEEKS} weeks`}>
        {grid.map((col, i) => (
          <div key={i} className="activity-week flex min-w-0 flex-1 flex-col gap-[3px]" style={{ "--week": i } as CSSProperties}>
            {col.map((c) => (
              <div
                key={c.day}
                className={cn("aspect-square w-full rounded-[2px]", c.n < 0 ? "invisible" : heat(c.n, max))}
                title={c.n < 0 ? undefined : `${date(c.day)}: ${c.n} ${unit}${c.n === 1 ? "" : "s"}`}
              />
            ))}
          </div>
        ))}
      </div>
      <div className="flex items-baseline gap-2 text-2xs text-faint tabular-nums">
        <span className="min-w-0 truncate">
          {sessions.length} thread{sessions.length === 1 ? "" : "s"}
          {turns !== null && ` · ${compact(turns)} turns`}
          {tokens !== null && tokens > 0 && ` · ${compact(tokens)} tokens`}
        </span>
        <span className="ml-auto shrink-0">
          {month(first)} – {month(now)}
        </span>
      </div>
    </>
  );
}

// A status dot: working pulses in the signal colour, waiting on you is the
// warning's, the rest are quiet.
function Dot({ s }: { s: Session }) {
  return (
    <span
      aria-hidden
      className={cn(
        "size-1.5 shrink-0 rounded-full",
        s.pending > 0 ? "bg-warning" : s.busy ? "animate-deck-pulse bg-signal" : "bg-[color-mix(in_srgb,var(--text)_28%,transparent)]",
      )}
    />
  );
}

function Recent({
  rows,
  more,
  now,
  onOpen,
  empty,
  status = false,
}: {
  rows: readonly Session[];
  more: number;
  now: number;
  onOpen: (id: string) => void;
  empty: string;
  // Say what each is doing instead of when it was touched.
  status?: boolean;
}) {
  if (rows.length === 0) return <p className="text-pretty text-faint text-xs leading-relaxed">{empty}</p>;
  return (
    <>
      <ul className="-mx-1.5 flex flex-col">
        {rows.map((s) => {
          const title = sessionTitle(s);
          const when = ago(toMillis(s.updated_at), now);
          const what = status ? (s.pending > 0 ? `Needs you · ${when}` : `Working · ${when}`) : when;
          return (
            <li key={s.id}>
              <button
                type="button"
                className="home-row grid w-full cursor-pointer grid-cols-[auto_minmax(0,1fr)] items-center gap-x-2.5 rounded-(--control-radius) px-1.5 py-1 text-left outline-none transition-colors hover:bg-hover focus-visible:bg-hover"
                aria-label={`Open ${title}, ${what}`}
                onClick={() => onOpen(s.id)}
              >
                {status && s.pending > 0 ? <ShieldQuestion aria-hidden className="size-3 shrink-0 text-warning-foreground" /> : <Dot s={s} />}
                <span className="truncate text-[13px] text-foreground">{title}</span>
                <span />
                <span className="truncate text-2xs text-faint">{what}</span>
              </button>
            </li>
          );
        })}
      </ul>
      {more > 0 && <span className="text-2xs text-faint">{more} more</span>}
    </>
  );
}

// The plan window nearest its limit, or with none the spend of the last
// thirty days.
function UsageStat({ usage, local, now }: { usage: Usage | null; local: boolean; now: number }) {
  if (usage === null)
    return <p className="text-pretty text-faint text-xs leading-relaxed">{local ? "Reading the usage record…" : "Usage is kept for folders on this computer."}</p>;
  const top = usage.limits.reduce<UsageLimit | null>((a, l) => (a === null || l.usedPct > a.usedPct ? l : a), null);
  if (top !== null)
    return (
      <Stat
        value={`${Math.round(top.usedPct)}%`}
        line={top.resetsAt > now ? `${top.label || "Plan"} · resets ${date(top.resetsAt)}` : `${top.label || "Plan"} used`}
      />
    );
  const cutoff = now - 30 * DAY_MS;
  const spend = usage.days.filter((d) => d.day >= cutoff).reduce((t, d) => t + d.costUsd, 0);
  return <Stat value={dollars(spend)} line="Last 30 days" />;
}

function Stat({ value, line }: { value: string; line: string }) {
  return (
    <div className="mt-auto flex min-w-0 flex-col gap-0.5">
      <Tick value={value} className="truncate font-medium text-[26px] text-foreground leading-tight tracking-[-0.02em] tabular-nums" />
      <span className="truncate text-2xs text-faint">{line}</span>
    </div>
  );
}

// This month so far against the whole of the last.
export function monthCounts(stamps: readonly number[], now: number): { now: number; last: number } {
  const d = new Date(now);
  const start = new Date(d.getFullYear(), d.getMonth(), 1).getTime();
  const prev = new Date(d.getFullYear(), d.getMonth() - 1, 1).getTime();
  let a = 0;
  let b = 0;
  for (const t of stamps) {
    if (t >= start && t <= now) a++;
    else if (t >= prev && t < start) b++;
  }
  return { now: a, last: b };
}

function Month({ usage, sessions, now }: { usage: Usage | null; sessions: readonly Session[]; now: number }) {
  const threads = monthCounts(
    sessions.map((s) => toMillis(s.updated_at)),
    now,
  );
  // Turns: each day's count, once per turn.
  const turns =
    usage === null
      ? null
      : usage.days.reduce(
          (m, d) => {
            const c = monthCounts([d.day], now);
            return { now: m.now + c.now * d.turns, last: m.last + c.last * d.turns };
          },
          { now: 0, last: 0 },
        );
  return (
    <div className="mt-auto flex flex-col gap-2">
      <Figure label="Threads" value={threads.now} last={threads.last} />
      {turns !== null && <Figure label="Turns" value={turns.now} last={turns.last} />}
    </div>
  );
}

function Figure({ label, value, last }: { label: string; value: number; last: number }) {
  const delta = value - last;
  return (
    <div className="flex flex-col">
      <span className="text-2xs text-faint">{label}</span>
      <span className="flex items-baseline gap-1.5 tabular-nums">
        <Tick value={compact(value)} className="font-medium text-lg text-primary leading-tight" />
        {delta !== 0 && (
          <span className={cn("text-2xs", delta > 0 ? "text-success-foreground" : "text-destructive-foreground")} title="Against last month">
            {delta > 0 ? "+" : "−"}
            {compact(Math.abs(delta))}
          </span>
        )}
      </span>
    </div>
  );
}
