import { memo } from "react";
import { agentColour } from "../../agents";
import { type Bounds, edgePath, type GraphEdge, type GraphNode } from "../../graph";
import { cn } from "../../lib/cn";

// The Work Graph's edges: one SVG under the cards, a smooth curve each. An
// agent's edges take its hue; the rest are a neutral line. A lease is a
// dotted line, and the edge to the file an agent edits now flows.

const inside = (b: Bounds, s: Bounds) => b.x + b.w >= s.x && b.x <= s.x + s.w && b.y + b.h >= s.y && b.y <= s.y + s.h;

function stroke(e: GraphEdge, nodes: Map<string, GraphNode>): string {
  const a = e.tint === null ? undefined : nodes.get(e.tint);
  if (a?.kind !== "agent") return "var(--muted-foreground)";
  return agentColour(a.name, a.agentKind);
}

export const Edges = memo(function Edges({
  edges,
  nodes,
  near,
  seen,
}: {
  edges: GraphEdge[];
  nodes: Map<string, GraphNode>;
  // The hovered neighbourhood, or null: an edge out of it fades.
  near: Set<string> | null;
  // The part of the graph in sight; a live edge out of it stands still.
  seen: Bounds | null;
}) {
  return (
    <svg aria-hidden className="pointer-events-none absolute top-0 left-0 overflow-visible" width="1" height="1">
      {edges.map((e) => {
        const a = nodes.get(e.from);
        const b = nodes.get(e.to);
        if (a === undefined || b === undefined) return null;
        const lit = near === null || (near.has(e.from) && near.has(e.to));
        const tinted = e.tint !== null;
        const d = edgePath(a, b);
        const live = e.live && (seen === null || inside(a, seen) || inside(b, seen));
        return (
          <g
            key={e.id}
            data-edge={e.kind}
            className="transition-opacity duration-200"
            style={{ opacity: lit ? (near === null ? 1 : 1) : 0.1 }}
            stroke={stroke(e, nodes)}
          >
            {e.live && <path d={d} fill="none" strokeWidth={6} strokeOpacity={0.14} strokeLinecap="round" vectorEffect="non-scaling-stroke" />}
            <path
              d={d}
              fill="none"
              strokeWidth={e.live ? 1.75 : 1.25}
              strokeOpacity={e.live ? 0.95 : tinted ? (near !== null && lit ? 0.85 : 0.55) : near !== null && lit ? 0.6 : 0.32}
              strokeDasharray={e.kind === "holds" ? "1.5 4" : undefined}
              strokeLinecap="round"
              vectorEffect="non-scaling-stroke"
              className={cn(e.live && "graph-flow", e.live && !live && "[animation:none]")}
            />
            <circle cx={b.x} cy={b.y + b.h / 2} r={2} fill="var(--canvas)" strokeWidth={1.25} strokeOpacity={tinted ? 0.8 : 0.5} vectorEffect="non-scaling-stroke" />
          </g>
        );
      })}
    </svg>
  );
});
