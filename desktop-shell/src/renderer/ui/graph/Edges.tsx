import { memo } from "react";
import { agentColour } from "../../agents";
import { type Bounds, type Direction, edgePath, type GraphEdge, type GraphNode } from "../../graph";
import { cn } from "../../lib/cn";

// The Work Graph's edges: one SVG under the cards, a thin curve each, one
// pixel at any zoom. Colour carries meaning only: an agent's edges take its
// hue, the rest are a neutral hairline. A lease is dotted, and the edge to
// the file an agent edits now flows.

const inside = (b: Bounds, s: Bounds) => b.x + b.w >= s.x && b.x <= s.x + s.w && b.y + b.h >= s.y && b.y <= s.y + s.h;

function stroke(e: GraphEdge, nodes: Map<string, GraphNode>): string | null {
  const a = e.tint === null ? undefined : nodes.get(e.tint);
  return a?.kind === "agent" ? agentColour(a.name, a.agentKind) : null;
}

export const Edges = memo(function Edges({
  dir,
  edges,
  nodes,
  near,
  seen,
}: {
  dir: Direction;
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
        const lit = near !== null && near.has(e.from) && near.has(e.to);
        const faded = near !== null && !lit;
        const hue = stroke(e, nodes);
        const moving = e.live && (seen === null || inside(a, seen) || inside(b, seen));
        // A neutral edge is a hairline; a hue reads at 55%, at full when lit
        // or live.
        const opacity = faded ? 0.08 : e.live || lit ? 1 : hue === null ? 1 : 0.55;
        return (
          <path
            key={e.id}
            data-edge={e.kind}
            d={edgePath(a, b, dir)}
            fill="none"
            stroke={hue ?? (lit ? "var(--muted-foreground)" : "var(--hairline-strong)")}
            strokeWidth={e.live ? 1.5 : 1}
            strokeDasharray={e.kind === "holds" ? "1 3" : undefined}
            strokeLinecap="round"
            vectorEffect="non-scaling-stroke"
            className={cn("transition-opacity duration-200", e.live && "graph-flow", e.live && !moving && "[animation:none]")}
            style={{ opacity }}
          />
        );
      })}
    </svg>
  );
});
