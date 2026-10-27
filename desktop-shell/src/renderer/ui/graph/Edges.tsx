import { memo } from "react";
import { agentColour } from "../../agents";
import { type Bounds, type Direction, edgePath, type GraphEdge, type GraphNode } from "../../graph";
import { cn } from "../../lib/cn";

// The Work Graph's edges: one SVG under the cards, a thin curve each, one
// pixel at any zoom, and a long edge runs in its lane between the cards
// (graph.ts, route). Colour carries meaning only. At rest every edge is a
// neutral hairline; the edge to the file an agent edits now takes its hue
// and flows; and while a node is hovered or picked, its edges take the hue
// of the agent they belong to and the rest fade. A lease is dotted; a file's
// owner keeps its hue on its edge; an ask for a file flows in the warning
// hue while it waits.

const inside = (b: Bounds, s: Bounds) => b.x + b.w >= s.x && b.x <= s.x + s.w && b.y + b.h >= s.y && b.y <= s.y + s.h;

function hueOf(e: GraphEdge, nodes: Map<string, GraphNode>): string | null {
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
  // The hovered or picked neighbourhood, or null: an edge out of it fades.
  near: Set<string> | null;
  // The part of the graph in sight; a live edge out of it stands still.
  seen: Bounds | null;
}) {
  const drawn = edges.flatMap((e) => {
    const a = nodes.get(e.from);
    const b = nodes.get(e.to);
    if (a === undefined || b === undefined) return [];
    const lit = near !== null && near.has(e.from) && near.has(e.to);
    return [{ e, a, b, lit, rank: lit ? 2 : e.live ? 1 : 0 }];
  });
  // The lit and the live edges over the rest.
  drawn.sort((x, y) => x.rank - y.rank);
  return (
    <svg aria-hidden className="pointer-events-none absolute top-0 left-0 overflow-visible" width="1" height="1">
      {drawn.map(({ e, a, b, lit }) => {
        const faded = near !== null && !lit;
        // An ask waits in the warning hue; a file's owner keeps its own.
        // An agent's worktree keeps its hue too.
        const hue = e.kind === "asks" ? "var(--warning)" : e.live || lit || e.kind === "edited" || e.kind === "works" ? hueOf(e, nodes) : null;
        const moving = e.live && (seen === null || inside(a, seen) || inside(b, seen));
        // The lanes run from the earlier layer to the later one.
        const [p, q] = a.layer <= b.layer ? [a, b] : [b, a];
        return (
          <path
            key={e.id}
            data-edge={e.kind}
            d={edgePath(p, q, dir, e.via)}
            fill="none"
            stroke={hue ?? (lit ? "var(--muted-foreground)" : "var(--graph-edge)")}
            strokeWidth={e.live ? 1.5 : 1}
            strokeDasharray={e.kind === "holds" ? "1 3" : e.kind === "asks" ? "6 2" : e.kind === "lands" ? "3 2" : undefined}
            strokeOpacity={e.kind === "edited" && !lit ? 0.6 : undefined}
            strokeLinecap="round"
            vectorEffect="non-scaling-stroke"
            className={cn("transition-[opacity,stroke] duration-200", e.live && "graph-flow", e.live && !moving && "[animation:none]")}
            style={{ opacity: faded ? 0.12 : 1 }}
          />
        );
      })}
    </svg>
  );
});
