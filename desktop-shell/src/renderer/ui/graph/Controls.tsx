import { type CSSProperties, useEffect, useRef } from "react";
import { agentColour } from "../../agents";
import { type Graph, graphSummary, type View } from "../../graph";
import { cn } from "../../lib/cn";
import { FrameCorners, Minus, Plus } from "../icons";
import { Button } from "../kit/button";
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

// The whole graph small, the part in sight as a frame; a press there moves
// the view to it.
export function Minimap({ g, view, style }: { g: Graph; view: ViewApi; style?: CSSProperties }) {
  const frame = useRef<SVGRectElement>(null);
  const box = useRef<HTMLDivElement>(null);
  const b = g.bounds;
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
        {g.nodes.map((n) => (
          <rect
            key={n.id}
            x={ox + n.x * s}
            y={oy + n.y * s}
            width={Math.max(2, n.w * s)}
            height={Math.max(2, n.h * s)}
            rx={1}
            fill={n.kind === "agent" ? agentColour(n.name, n.agentKind) : "var(--muted-foreground)"}
            fillOpacity={n.kind === "agent" ? 0.7 : 0.22}
          />
        ))}
        <rect ref={frame} rx={2} fill="none" stroke="var(--muted-foreground)" strokeOpacity={0.7} strokeWidth={1} />
      </svg>
    </div>
  );
}
