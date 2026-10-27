import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { memo, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { agentColour } from "../../agents";
import type { DeckClient } from "../../client";
import type { BoardTask } from "../../crew";
import { neighbourhood } from "../../graph";
import { cn } from "../../lib/cn";
import type { ProjectApi } from "../../project";
import { shallowEqual, useAppSlice } from "../../select";
import {
  ALL_WORK,
  buildHistory,
  type HistoryEdge,
  type HistoryFilter,
  type HistoryGraph,
  type HistoryNode,
  type HistoryThread,
  historyPath,
  inView,
  ITEMS_MAX,
  LABEL_W,
} from "../../workHistory";
import { ChevronRight, CircleAlert, Graph as GraphIcon, Loader2 } from "../icons";
import { EmptyState } from "../kit/empty";
import { HistoryFilters, HistoryLegend, Minimap, type MapNode, ZoomCapsule } from "./Controls";
import { hueOf } from "./Nodes";
import { type FitTarget, type Pad, useView } from "./useView";

// The Work Graph's History (workHistory.ts): every thread's work over time,
// the agents' prompts, replies and runs of calls in lanes, the handoffs and
// reviews between agents, the tasks and the files they touched. A click
// jumps to that point of the conversation; a run or a cluster opens in
// place. A big history stays smooth: past CULL_AT nodes only those near the
// view draw, and zoomed far out the cards drop their text (data-lod).

export interface HistoryProps {
  c: DeckClient;
  api: ProjectApi | undefined;
  // The open thread: "This thread" in the filter, and the one whose board
  // shows its tasks.
  sid: string;
  tasks: readonly BoardTask[] | null;
  theme: Theme | null;
  compact: boolean;
  onOpenMessage: (thread: string, mid: string) => void;
  onOpenFile: (path: string) => void;
  onOpenTasks: () => void;
}

// The least zoom: a long history fits further out than the live graph.
const HISTORY_ZOOM_MIN = 0.08;
// The zoom a fit keeps to, when the whole history would be smaller.
const FIT_READ = { min: 0.55, max: 0.85 };
// Below this zoom, the cards are blocks of their hue (no text).
const LOD_FAR = 0.35;
export const CULL_AT = 240;
// How often a busy project's history redraws.
const THROTTLE_MS = 600;
// "Load full history" reads at most this many older pages of a thread.
const PAGES_MAX = 50;

// The filter and the opened nodes, for as long as the window lives.
let kept: { filter: HistoryFilter; open: Set<string> } = { filter: ALL_WORK, open: new Set() };

const slice = (s: DeckClient["state"]) => ({ sessions: s.sessions, messages: s.messages, roster: s.roster, pages: s.pages });

// value, at most once every ms.
function useThrottled<T>(value: T, ms: number): T {
  const [shown, setShown] = useState(value);
  const last = useRef(0);
  useEffect(() => {
    const wait = last.current + ms - Date.now();
    if (wait <= 0) {
      last.current = Date.now();
      setShown(value);
      return;
    }
    const t = setTimeout(() => {
      last.current = Date.now();
      setShown(value);
    }, wait);
    return () => clearTimeout(t);
  }, [value, ms]);
  return shown;
}

export function History({ c, api, sid, tasks, theme, compact, onOpenMessage, onOpenFile, onOpenTasks }: HistoryProps) {
  const live = useAppSlice(c.store, slice, shallowEqual);
  const s = useThrottled(live, THROTTLE_MS);
  const [filter, setFilter] = useState(kept.filter);
  const [open, setOpen] = useState(kept.open);
  kept = { filter, open };

  // Every thread's latest page, once; older pages on demand.
  const [loading, setLoading] = useState<string | null>(null);
  const unloaded = s.sessions.filter((x) => s.pages[x.id] === undefined).map((x) => x.id);
  const missing = unloaded.join(" ");
  useEffect(() => {
    if (missing === "" || api === undefined) return;
    let live = true;
    void (async () => {
      for (const id of missing.split(" ")) {
        if (!live) return;
        await api.loadThread(id);
      }
    })();
    return () => {
      live = false;
    };
  }, [missing, api]);
  const older = s.sessions.filter((x) => s.pages[x.id]?.more === true).length;
  const loadAll = async () => {
    for (const x of c.state.sessions) {
      for (let i = 0; i < PAGES_MAX && c.state.pages[x.id]?.more === true; i++) {
        setLoading(x.title || x.id);
        if (!(await c.loadOlder(x.id))) break;
      }
    }
    setLoading(null);
  };

  const threads = useMemo<HistoryThread[]>(
    () =>
      s.sessions.map((x) => ({
        id: x.id,
        title: x.title || "Untitled",
        messages: s.messages[x.id] ?? [],
        roster: s.roster[x.id] ?? [],
        ...(x.id === sid && tasks !== null ? { tasks } : {}),
      })),
    [s, sid, tasks],
  );
  const g = useMemo(() => buildHistory(threads, filter, open), [threads, filter, open]);
  const toggle = useCallback((id: string) => {
    setOpen((o) => {
      const next = new Set(o);
      if (!next.delete(id)) next.add(id);
      return next;
    });
  }, []);

  return (
    <div className="relative flex min-h-0 flex-1">
      {g.nodes.length === 0 && filter === ALL_WORK ? (
        <div className="work-graph flex min-h-0 flex-1 items-center justify-center" data-empty="">
          <EmptyState
            icon={GraphIcon}
            title="No work to map yet."
            hint="Every thread's prompts, the agents' replies and actions, their handoffs and the files they touch show here over time."
            className="max-w-72"
          />
        </div>
      ) : (
        <Canvas
          g={g}
          theme={theme}
          compact={compact}
          onToggle={toggle}
          onOpenMessage={onOpenMessage}
          onOpenFile={onOpenFile}
          onOpenTasks={onOpenTasks}
        />
      )}
      <HistoryFilters
        g={g}
        filter={filter}
        sid={sid}
        theme={theme}
        compact={compact}
        older={older}
        loading={loading}
        onFilter={setFilter}
        onLoadAll={() => void loadAll()}
      />
    </div>
  );
}

function Canvas({
  g,
  theme,
  compact,
  onToggle,
  onOpenMessage,
  onOpenFile,
  onOpenTasks,
}: {
  g: HistoryGraph;
  theme: Theme | null;
  compact: boolean;
  onToggle: (id: string) => void;
  onOpenMessage: (thread: string, mid: string) => void;
  onOpenFile: (path: string) => void;
  onOpenTasks: () => void;
}) {
  const [hover, setHover] = useState<string | null>(null);
  const byId = useMemo(() => new Map(g.nodes.map((n) => [n.id, n])), [g]);
  const near = useMemo(() => (hover === null || !byId.has(hover) ? null : neighbourhood(g.edges, hover)), [g, byId, hover]);

  // A fit shows the whole history while its cards stay readable; a longer
  // one shows its start, the top left, at a zoom that reads (FIT_READ), and
  // the rest is a pan away (the minimap shows where).
  const target = useRef<FitTarget>(null);
  const root = useRef<HTMLElement | null>(null);
  target.current = {
    get bounds() {
      const el = root.current;
      const b = g.bounds;
      if (el === null) return b;
      const w = el.clientWidth - 64;
      const h = el.clientHeight - 200;
      if (Math.min(w / b.w, h / b.h) >= FIT_READ.min) return b;
      const k = Math.min(FIT_READ.max, Math.max(FIT_READ.min, h / b.h));
      return { ...b, w: Math.min(b.w, w / k), h: Math.min(b.h, h / k) };
    },
    pad: (el: HTMLElement): Pad => {
      const composer = Number.parseFloat(getComputedStyle(el).getPropertyValue("--composer-h")) || 0;
      return compact ? { top: 96, right: 20, bottom: 56, left: 20 } : { top: 104, right: 32, bottom: composer + 40, left: 32 };
    },
  };
  const view = useView(target, () => {}, HISTORY_ZOOM_MIN);
  root.current = view.root.current;
  // A new shape fits again, unless the person moved the view.
  const shape = `${g.nodes.length}/${g.bounds.w}x${g.bounds.h}`;
  const first = useRef(shape);
  useEffect(() => {
    if (shape !== first.current && !view.moved.current) view.fit(true);
    first.current = shape;
  }, [shape, view]);
  // Far out, the cards drop their text: a class on the canvas, so a zoom
  // renders nothing in React.
  useEffect(
    () =>
      view.subscribe((v) => {
        const el = view.root.current;
        if (el === null) return;
        const far = v.k < LOD_FAR ? "far" : "near";
        if (el.dataset["lod"] !== far) el.dataset["lod"] = far;
      }),
    [view],
  );

  const host = useRef({ onOpenMessage, onOpenFile, onOpenTasks, onToggle });
  host.current = { onOpenMessage, onOpenFile, onOpenTasks, onToggle };
  const pick = useCallback(
    (id: string) => {
      const n = byId.get(id);
      if (n === undefined) return;
      if (n.kind === "cluster") host.current.onToggle(id);
      else if (n.kind === "file") host.current.onOpenFile(n.path ?? "");
      else if (n.kind === "task") host.current.onOpenTasks();
      else if (n.mid !== null) host.current.onOpenMessage(n.thread, n.mid);
    },
    [byId],
  );

  // A big history draws what is near the view; until the view settles, nothing.
  const big = g.nodes.length > CULL_AT;
  const shown = useMemo(() => (big ? (view.seen === null ? { nodes: [], edges: [] } : inView(g, view.seen)) : g), [g, big, view.seen]);
  const map = useMemo<MapNode[]>(
    () =>
      g.nodes.map((n) => ({
        id: n.id,
        x: n.x,
        y: n.y,
        w: n.w,
        h: n.h,
        fill: n.agent === null ? "var(--muted-foreground)" : agentColour(n.agent, n.agentKind),
        strong: n.agent !== null && n.kind !== "task",
      })),
    [g],
  );
  const labels = g.lanes.filter((l) => !big || view.seen === null || (l.y + l.h >= view.seen.y && l.y <= view.seen.y + view.seen.h));

  return (
    <div
      ref={view.root}
      role="group"
      aria-label="Work history"
      aria-roledescription="graph"
      data-live="on"
      data-lod="near"
      data-clustered={g.clustered ? "" : undefined}
      className="work-graph history-graph relative isolate flex min-h-0 flex-1 cursor-grab touch-none select-none overflow-hidden outline-none data-moving:cursor-grabbing"
    >
      <div ref={view.layer} className="graph-layer absolute top-0 left-0 origin-top-left">
        {g.bands.map((b) => (
          <div
            key={b.thread || "files"}
            aria-hidden
            className="history-band absolute top-0 left-0 rounded-lg border-hairline border-t"
            style={{ transform: `translate(${g.bounds.x}px, ${b.y}px)`, width: g.bounds.w, height: b.h }}
          >
            <span className="hist-text flex items-baseline gap-2 truncate pt-1.5 pl-1 text-xs" style={{ maxWidth: LABEL_W + 480 }}>
              <span className="truncate font-medium text-foreground">{b.title}</span>
              {b.at > 0 && <time className="shrink-0 text-2xs text-faint tabular-nums">{when(b.at)}</time>}
            </span>
          </div>
        ))}
        {labels.map((l) => (
          <span
            key={l.id}
            aria-hidden
            className="hist-text absolute top-0 left-0 flex items-center gap-1.5 truncate text-2xs text-muted-foreground"
            style={{ transform: `translate(${g.bounds.x + 8}px, ${l.y}px)`, width: LABEL_W - 16, height: 28, ...(l.agent === null ? {} : hueOf(l.agent, l.agentKind)) }}
          >
            {l.agent !== null && <span className="font-mono text-(--hue)">{kindMark(theme, l.agentKind) || "●"}</span>}
            <span className={cn("truncate", l.agent !== null && "text-(--hue)")}>{l.label}</span>
          </span>
        ))}
        <HistoryEdges edges={shown.edges} nodes={byId} near={near} />
        {shown.nodes.map((n) => (
          <HistoryCard key={n.id} n={n} dim={near !== null && !near.has(n.id)} theme={theme} onPick={pick} onToggle={onToggle} onHover={setHover} />
        ))}
      </div>
      {!compact && <HistoryLegend g={g} />}
      {!compact && <Minimap bounds={g.bounds} nodes={map} view={view} style={{ bottom: "calc(var(--composer-h, 0px) + 3.5rem)" }} />}
      <ZoomCapsule
        view={view}
        className="absolute right-3 z-10"
        style={{ bottom: compact ? "0.75rem" : "calc(var(--composer-h, 0px) + 0.75rem)" }}
      />
    </div>
  );
}

// "27 Oct, 09:14": when a thread's shown work began.
const when = (ms: number) => new Date(ms).toLocaleString(undefined, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" });

const DASH: Partial<Record<HistoryEdge["kind"], string>> = { handoff: "4 3", reviews: "2 3", reads: "1 3" };

// The edges: a hairline each, the lit ones in their agent's hue.
const HistoryEdges = memo(function HistoryEdges({
  edges,
  nodes,
  near,
}: {
  edges: readonly HistoryEdge[];
  nodes: Map<string, HistoryNode>;
  near: Set<string> | null;
}) {
  return (
    <svg aria-hidden className="pointer-events-none absolute top-0 left-0 overflow-visible" width="1" height="1">
      {edges.map((e) => {
        const a = nodes.get(e.from);
        const b = nodes.get(e.to);
        if (a === undefined || b === undefined) return null;
        const lit = near !== null && near.has(e.from) && near.has(e.to);
        const hue = lit && e.agent !== null ? agentColour(e.agent, e.agentKind) : null;
        // A handoff or a review is the story of the work: it reads stronger
        // than the files.
        const strong = e.kind === "handoff" || e.kind === "reviews" || e.kind === "asks";
        return (
          <path
            key={e.id}
            data-edge={e.kind}
            d={historyPath(a, b)}
            fill="none"
            stroke={hue ?? (strong ? "var(--muted-foreground)" : "var(--graph-edge)")}
            strokeOpacity={hue !== null ? 1 : strong ? 0.55 : 1}
            strokeWidth={strong || lit ? 1.25 : 1}
            strokeDasharray={DASH[e.kind]}
            strokeLinecap="round"
            vectorEffect="non-scaling-stroke"
            className="transition-[opacity,stroke] duration-200"
            style={{ opacity: near !== null && !lit ? 0.12 : 1 }}
          />
        );
      })}
    </svg>
  );
});

const TASK_MARK: Record<string, string> = { done: "✓", failed: "✕", running: "●", ready: "○" };

// A card: flat, as the live graph's (Nodes.tsx). A run of calls has a caret
// that opens it to its calls; the rest of the card jumps to the message.
const HistoryCard = memo(function HistoryCard({
  n,
  dim,
  theme,
  onPick,
  onToggle,
  onHover,
}: {
  n: HistoryNode;
  dim: boolean;
  theme: Theme | null;
  onPick: (id: string) => void;
  onToggle: (id: string) => void;
  onHover: (id: string | null) => void;
}) {
  const agent = n.agent;
  const style = agent === null ? {} : hueOf(agent, n.agentKind);
  const mark = agent === null ? "" : kindMark(theme, n.agentKind) || "●";
  const run = n.actions;
  const label =
    n.kind === "prompt"
      ? `You: ${n.title}`
      : n.kind === "file"
        ? `File ${n.path ?? n.title}`
        : n.kind === "task"
          ? `Task ${n.title}, ${n.status ?? ""}`
          : n.kind === "cluster"
            ? `${agent ?? ""}: ${n.title}, ${n.meta}. Open`
            : `${agent ?? ""}: ${n.kind === "actions" ? `${n.title}, ${n.meta}` : n.title}`;
  return (
    <div
      data-node={n.id}
      data-kind={n.kind}
      data-state={run?.running === true ? "running" : (run?.failed ?? 0) > 0 ? "failed" : undefined}
      className={cn("graph-node history-node absolute top-0 left-0 transition-opacity duration-200", dim && "opacity-35")}
      style={{ transform: `translate(${n.x}px, ${n.y}px)`, width: n.w, height: n.h, ...style }}
      onPointerEnter={() => onHover(n.id)}
      onPointerLeave={() => onHover(null)}
    >
      <div
        className={cn(
          "hist-card flex size-full animate-enter flex-col overflow-hidden rounded-md border border-(--graph-rim) bg-surface-1 transition-colors hover:border-muted-foreground/50",
          agent !== null && n.kind !== "task" && "border-l-2 border-l-(--hue)",
          n.kind === "prompt" && "bg-surface-2",
        )}
      >
        <button
          type="button"
          aria-label={label}
          title={n.kind === "file" ? n.path : n.title}
          className={cn(
            "flex min-h-0 w-full flex-1 cursor-pointer flex-col justify-center gap-0.5 px-2.5 text-left outline-none focus-visible:ring-1 focus-visible:ring-primary focus-visible:ring-inset",
            n.kind === "actions" && "pr-7",
          )}
          style={{ maxHeight: n.kind === "file" ? undefined : 46 }}
          onClick={() => onPick(n.id)}
          onFocus={() => onHover(n.id)}
          onBlur={() => onHover(null)}
        >
          {n.kind === "file" ? (
            <span className="hist-text flex min-w-0 items-baseline font-mono text-code">
              <span className="truncate text-faint">{n.meta}</span>
              <span className="shrink-0 text-foreground">{n.title}</span>
            </span>
          ) : (
            <>
              <span className="hist-text flex min-w-0 items-center gap-1.5 text-2xs">
                {run?.running === true ? (
                  <Loader2 aria-hidden className="size-3 shrink-0 animate-spin text-(--hue)" weight="bold" />
                ) : n.kind === "task" ? (
                  <span aria-hidden className="font-mono text-muted-foreground">
                    {TASK_MARK[n.status ?? ""] ?? "·"}
                  </span>
                ) : agent !== null ? (
                  <span aria-hidden className="font-mono text-(--hue)">
                    {mark}
                  </span>
                ) : null}
                <span className={cn("shrink-0 font-medium", agent !== null && n.kind !== "task" ? "text-(--hue)" : "text-muted-foreground")}>
                  {n.kind === "prompt" ? "you" : n.kind === "task" ? "task" : agent}
                </span>
                {(n.kind === "actions" || n.kind === "cluster") && <span className="shrink-0 text-muted-foreground tabular-nums">· {n.title}</span>}
                {(run?.failed ?? 0) > 0 && (
                  <span className="ml-auto flex shrink-0 items-center gap-0.5 text-destructive-foreground">
                    <CircleAlert aria-hidden className="size-3" />
                    {run!.failed}
                  </span>
                )}
              </span>
              <span className={cn("hist-text truncate text-xs", n.kind === "actions" || n.kind === "cluster" ? "text-faint" : "text-foreground")}>
                {n.kind === "actions" || n.kind === "cluster" ? n.meta || "—" : n.title}
              </span>
            </>
          )}
        </button>
        {n.open === true && run !== undefined && (
          <ul className="hist-text flex flex-col border-hairline border-t px-2.5 pt-1" aria-label="Actions">
            {run.items.slice(0, ITEMS_MAX).map((it, i) => (
              <li key={i} className={cn("flex h-[18px] min-w-0 items-center gap-1.5 font-mono text-[11px]", it.failed ? "text-destructive-foreground" : "text-muted-foreground")}>
                <span className="shrink-0 text-faint">{it.name}</span>
                <span className="truncate">{it.summary}</span>
              </li>
            ))}
            {run.items.length > ITEMS_MAX && <li className="h-[18px] text-2xs text-faint">+{run.items.length - ITEMS_MAX} more</li>}
          </ul>
        )}
      </div>
      {n.kind === "actions" && (
        <button
          type="button"
          aria-label={n.open === true ? "Hide the actions" : "Show the actions"}
          aria-expanded={n.open === true}
          className="absolute top-1 right-1 grid size-5 cursor-pointer place-items-center rounded text-faint outline-none hover:bg-hover hover:text-foreground focus-visible:ring-1 focus-visible:ring-primary"
          onClick={() => onToggle(n.id)}
        >
          <ChevronRight className={cn("size-3 transition-transform duration-200", n.open === true && "rotate-90")} />
        </button>
      )}
    </div>
  );
});
