import type { Theme } from "@web/protocol";
import { kindMark } from "@web/theme";
import { type CSSProperties, type ReactNode, useEffect, useRef, useState } from "react";
import { agentColour } from "../../agents";
import { type Bounds, type Graph, graphSummary, type View } from "../../graph";
import { cn } from "../../lib/cn";
import { HISTORY_KINDS, type HistoryFilter, type HistoryFilterKind, type HistoryGraph } from "../../workHistory";
import { Check, ChevronDown, FrameCorners, Minus, Plus } from "../icons";
import { Button } from "../kit/button";
import { MenuItem, MenuSeparator, Popover } from "../kit/menu";
import { segmentItem, segmentTrack } from "../kit/segmented";
import { hueOf } from "./Nodes";
import type { ViewApi } from "./useView";

// What floats over the Work Graph's canvas: the zoom capsule, the legend
// and the minimap. Each follows the view through a subscription that writes
// to the DOM, so a pan renders none of them.

// Zoom out, the zoom as a percentage (a click fits), zoom in, and fit.
export function ZoomCapsule({ view, className, style }: { view: ViewApi; className?: string; style?: CSSProperties }) {
  const label = useRef<HTMLSpanElement>(null);
  useEffect(
    () =>
      view.subscribe((v) => {
        if (label.current !== null) label.current.textContent = `${Math.round(v.k * 100)}%`;
      }),
    [view],
  );
  return (
    <div
      data-graph-control=""
      role="toolbar"
      aria-label="Zoom"
      className={cn("liquid-clear flex items-center gap-0.5 rounded-full p-0.5", className)}
      style={style}
    >
      <Button variant="ghost-muted" size="icon-xs" aria-label="Zoom out" title="Zoom out (−)" onClick={() => view.zoomBy(1 / 1.25, undefined, true)}>
        <Minus />
      </Button>
      <span ref={label} className="w-9 text-center text-2xs text-muted-foreground tabular-nums" aria-live="off">
        100%
      </span>
      <Button variant="ghost-muted" size="icon-xs" aria-label="Zoom in" title="Zoom in (+)" onClick={() => view.zoomBy(1.25, undefined, true)}>
        <Plus />
      </Button>
      <span aria-hidden className="mx-0.5 h-4 w-px bg-hairline" />
      <Button variant="ghost-muted" size="icon-xs" aria-label="Fit" title="Fit the graph (0)" onClick={() => view.fit(true)}>
        <FrameCorners />
      </Button>
    </div>
  );
}

// The counts, and a key to the three kinds of line, in plain type.
export function Legend({ g }: { g: Graph }) {
  const s = graphSummary(g);
  const parts = [
    `${s.agents} ${s.agents === 1 ? "agent" : "agents"}`,
    ...(s.tasks > 0 ? [`${s.done}/${s.tasks} tasks done`] : []),
    `${s.files} ${s.files === 1 ? "file" : "files"}`,
  ];
  return (
    <div data-graph-control="" className="graph-legend pointer-events-none absolute top-3.5 left-4 z-10 flex flex-col gap-1.5 text-2xs">
      <p className="flex items-center gap-2 text-muted-foreground tabular-nums">
        {s.working > 0 && (
          <span className="flex items-center gap-1.5 text-foreground">
            <span className="size-1.5 animate-deck-pulse rounded-full bg-signal" />
            {s.working} working
          </span>
        )}
        <span>{parts.join(" · ")}</span>
      </p>
      <ul aria-label="Key" className="flex items-center gap-3 text-3xs text-faint">
        <Key label="works on" />
        <Key label="holds" dash="1 3" />
        <Key label="editing now" dash="4 3" live />
      </ul>
    </div>
  );
}

function Key({ label, dash, live = false }: { label: string; dash?: string; live?: boolean }) {
  return (
    <li className="flex items-center gap-1.5">
      <svg aria-hidden width="16" height="4" className={live ? "text-muted-foreground" : "text-faint"}>
        <path d="M1 2 H15" stroke="currentColor" strokeWidth={live ? 1.5 : 1} strokeLinecap="round" strokeDasharray={dash} />
      </svg>
      {label}
    </li>
  );
}

const MAP_W = 140;
const MAP_H = 84;

// A node as the minimap draws it: an agent's in its hue, the rest faint.
export interface MapNode extends Bounds {
  id: string;
  fill: string;
  strong: boolean;
}

export const mapNodes = (g: Graph): MapNode[] =>
  g.nodes.map((n) => ({
    id: n.id,
    x: n.x,
    y: n.y,
    w: n.w,
    h: n.h,
    fill: n.kind === "agent" ? agentColour(n.name, n.agentKind) : "var(--muted-foreground)",
    strong: n.kind === "agent",
  }));

// The whole graph small, the part in sight as a frame; a press there moves
// the view to it.
export function Minimap({ bounds: b, nodes, view, style }: { bounds: Bounds; nodes: readonly MapNode[]; view: ViewApi; style?: CSSProperties }) {
  const frame = useRef<SVGRectElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const pad = 8;
  const s = Math.min((MAP_W - pad * 2) / Math.max(1, b.w), (MAP_H - pad * 2) / Math.max(1, b.h));
  const ox = pad + (MAP_W - pad * 2 - b.w * s) / 2 - b.x * s;
  const oy = pad + (MAP_H - pad * 2 - b.h * s) / 2 - b.y * s;
  useEffect(
    () =>
      view.subscribe((v: View) => {
        const el = view.root.current;
        const f = frame.current;
        if (el === null || f === null) return;
        // Only while part of the graph is out of sight.
        const all =
          b.x * v.k + v.x >= 0 && b.y * v.k + v.y >= 0 && (b.x + b.w) * v.k + v.x <= el.clientWidth && (b.y + b.h) * v.k + v.y <= el.clientHeight;
        box.current?.toggleAttribute("data-hidden", all);
        f.setAttribute("x", String(ox + (-v.x / v.k) * s));
        f.setAttribute("y", String(oy + (-v.y / v.k) * s));
        f.setAttribute("width", String((el.clientWidth / v.k) * s));
        f.setAttribute("height", String((el.clientHeight / v.k) * s));
      }),
    [view, ox, oy, s, b],
  );
  const go = (e: React.PointerEvent<SVGSVGElement>) => {
    const el = view.root.current;
    if (el === null) return;
    const r = e.currentTarget.getBoundingClientRect();
    const gx = (e.clientX - r.left - ox) / s;
    const gy = (e.clientY - r.top - oy) / s;
    const v = view.view.current;
    view.set({ k: v.k, x: el.clientWidth / 2 - gx * v.k, y: el.clientHeight / 2 - gy * v.k }, { user: true, glide: e.type === "pointerdown" });
  };
  return (
    <div
      ref={box}
      data-graph-control=""
      className="graph-minimap absolute right-3 z-10 overflow-hidden rounded-lg border border-hairline bg-surface-1 transition-opacity duration-200 data-hidden:pointer-events-none data-hidden:opacity-0"
      style={style}
    >
      <svg
        role="img"
        aria-label="Minimap"
        width={MAP_W}
        height={MAP_H}
        className="block cursor-pointer"
        onPointerDown={(e) => {
          e.currentTarget.setPointerCapture(e.pointerId);
          go(e);
        }}
        onPointerMove={(e) => {
          if (e.buttons === 1) go(e);
        }}
      >
        {nodes.map((n) => (
          <rect
            key={n.id}
            x={ox + n.x * s}
            y={oy + n.y * s}
            width={Math.max(2, n.w * s)}
            height={Math.max(2, n.h * s)}
            rx={1}
            fill={n.fill}
            fillOpacity={n.strong ? 0.7 : 0.22}
          />
        ))}
        <rect ref={frame} rx={2} fill="none" stroke="var(--muted-foreground)" strokeOpacity={0.7} strokeWidth={1} />
      </svg>
    </div>
  );
}

// ---- the History's controls --------------------------------------------------

// The time ranges the History offers, back from now.
export const RANGES: ReadonlyArray<{ label: string; ms: number | null }> = [
  { label: "All time", ms: null },
  { label: "Last hour", ms: 3_600_000 },
  { label: "Last 24 hours", ms: 86_400_000 },
  { label: "Last 7 days", ms: 7 * 86_400_000 },
  { label: "Last 30 days", ms: 30 * 86_400_000 },
];

const KIND_LABEL: Record<HistoryFilterKind, string> = { messages: "Messages", actions: "Actions", tasks: "Tasks", files: "Files" };

const pill = "liquid-clear flex h-7 cursor-pointer items-center gap-1.5 rounded-full px-3 text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring";

// A menu of a filter's choices, each checked or not, with "All" first.
function Choice({
  label,
  all,
  items,
  picked,
  onPick,
}: {
  label: string;
  all: string;
  items: ReadonlyArray<{ id: string; label: ReactNode; text: string }>;
  // null: all of them.
  picked: readonly string[] | null;
  onPick: (picked: string[] | null) => void;
}) {
  const [open, setOpen] = useState(false);
  const shown = picked === null ? all : picked.length === 1 ? (items.find((i) => i.id === picked[0])?.text ?? label) : `${picked.length} ${label.toLowerCase()}`;
  const flip = (id: string) => {
    const cur = picked ?? items.map((i) => i.id);
    const next = cur.includes(id) ? cur.filter((x) => x !== id) : [...cur, id];
    onPick(next.length === items.length ? null : next);
  };
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      label={label}
      className="max-h-80 w-60 overflow-y-auto"
      trigger={
        <button type="button" className={cn(pill, "max-w-52 text-foreground")} aria-label={label} aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <span className="truncate">{shown}</span>
          <ChevronDown aria-hidden className="size-3 shrink-0 text-faint" />
        </button>
      }
    >
      <MenuItem onClick={() => onPick(null)}>
        <Check className={cn(picked !== null && "invisible")} />
        {all}
      </MenuItem>
      <MenuSeparator />
      {items.map((i) => (
        <MenuItem key={i.id} onClick={() => flip(i.id)}>
          <Check className={cn(picked !== null && !picked.includes(i.id) && "invisible")} />
          <span className="flex min-w-0 items-center gap-1.5 truncate">{i.label}</span>
        </MenuItem>
      ))}
    </Popover>
  );
}

// The History's filters over its top left: the threads, the agents, the
// time range, the kinds of node; and, while threads hold older pages,
// "Load full history".
export function HistoryFilters({
  g,
  filter,
  sid,
  theme,
  compact,
  older,
  loading,
  onFilter,
  onLoadAll,
}: {
  g: HistoryGraph;
  filter: HistoryFilter;
  sid: string;
  theme: Theme | null;
  compact: boolean;
  // How many threads have older pages to read.
  older: number;
  // The thread whose older pages load now, or null.
  loading: string | null;
  onFilter: (f: HistoryFilter) => void;
  onLoadAll: () => void;
}) {
  const [range, setRange] = useState(0);
  const set = (over: Partial<HistoryFilter>) => onFilter({ ...filter, ...over });
  const threads = g.threads.map((t) => {
    const text = t.id === sid ? `${t.title} (this thread)` : t.title;
    return { id: t.id, label: text, text };
  });
  const agents = g.agents.map((a) => ({
    id: a.name,
    text: a.name,
    label: (
      <>
        <span aria-hidden className="font-mono text-(--hue)" style={hueOf(a.name, a.kind)}>
          {kindMark(theme, a.kind) || "●"}
        </span>
        {a.name}
      </>
    ),
  }));
  const kinds = new Set(filter.kinds);
  return (
    <div
      data-graph-control=""
      role="toolbar"
      aria-label="History filters"
      className={cn("history-filters absolute z-20 flex flex-wrap items-center gap-1.5", compact ? "top-12 right-3 left-3" : "top-3 left-4 max-w-[calc(100%-14rem)]")}
    >
      <Choice label="Threads" all="All threads" items={threads} picked={filter.threads} onPick={(threads) => set({ threads })} />
      <Choice label="Agents" all="All agents" items={agents} picked={filter.agents} onPick={(agents) => set({ agents })} />
      <select
        aria-label="Time range"
        className={cn(pill, "appearance-none bg-transparent text-foreground")}
        value={range}
        onChange={(e) => {
          const i = Number(e.target.value);
          setRange(i);
          const ms = RANGES[i]?.ms ?? null;
          set({ since: ms === null ? null : Date.now() - ms, until: null });
        }}
      >
        {RANGES.map((r, i) => (
          <option key={r.label} value={i}>
            {r.label}
          </option>
        ))}
      </select>
      <div className={segmentTrack()} role="group" aria-label="Show">
        {HISTORY_KINDS.map((k) => (
          <button
            key={k}
            type="button"
            aria-pressed={kinds.has(k)}
            className={segmentItem({ on: kinds.has(k) })}
            onClick={() => set({ kinds: HISTORY_KINDS.filter((x) => (x === k ? !kinds.has(k) : kinds.has(x))) })}
          >
            {KIND_LABEL[k]}
          </button>
        ))}
      </div>
      {(older > 0 || loading !== null) && (
        <Button size="sm" variant="ghost-muted" disabled={loading !== null} onClick={onLoadAll} title="Read every thread's older messages">
          {loading !== null ? `Loading ${loading}…` : "Load full history"}
        </Button>
      )}
    </div>
  );
}

// The History's counts and a key to its lines, under the filters.
export function HistoryLegend({ g }: { g: HistoryGraph }) {
  let actions = 0;
  let files = 0;
  for (const n of g.nodes) {
    if (n.kind === "actions") actions += n.actions?.count ?? 0;
    else if (n.kind === "file") files++;
  }
  const bands = g.bands.filter((b) => b.thread !== "").length;
  const agents = new Set(g.lanes.filter((l) => l.agent !== null).map((l) => `${l.thread}/${l.agent}`)).size;
  const parts = [
    `${bands} ${bands === 1 ? "thread" : "threads"}`,
    `${agents} ${agents === 1 ? "agent" : "agents"}`,
    `${actions} ${actions === 1 ? "action" : "actions"}`,
    `${files} ${files === 1 ? "file" : "files"}`,
  ];
  return (
    <div data-graph-control="" className="graph-legend pointer-events-none absolute top-14 left-4 z-10 flex flex-col gap-1.5 text-2xs">
      <p className="text-muted-foreground tabular-nums">
        {parts.join(" · ")}
        {g.clustered && <span className="text-faint"> · folded by round, click to open</span>}
      </p>
      <ul aria-label="Key" className="flex items-center gap-3 text-3xs text-faint">
        <Key label="asks" />
        <Key label="hands off" dash="4 3" />
        <Key label="reviews" dash="2 3" />
        <Key label="reads" dash="1 3" />
      </ul>
    </div>
  );
}
