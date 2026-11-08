import type { CSSProperties } from "react";
import { barHeights } from "../effort";
import { cn } from "../lib/cn";

// Bars stop this far above the box's foot, which puts their flat base on
// the text's baseline.
const BASELINE = 2;
const STEP = 4; // a 2px bar and a 2px gap

// The effort glyph (effort.ts; design-glass.md, section 12): rising bars on
// whole pixels, so they stay sharp on a 1x display, filled up to the level
// and the rest dimmed. Its colour is the text's. auto (the agent's own
// level) dims every bar alike.
export function EffortGlyph({ count, filled, auto = false, height = 14, className }: { count: number; filled: number; auto?: boolean; height?: number; className?: string }) {
  const width = STEP * count - 2;
  const max = height - BASELINE;
  return (
    <svg
      aria-hidden
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={cn("effort-glyph shrink-0", className)}
      data-auto={auto ? "" : undefined}
      data-filled={filled}
      style={{ width, height }}
    >
      {barHeights(count, max).map((h, i) => (
        <rect key={i} className={cn("effort-bar", !auto && i < filled && "on")} x={i * STEP} y={max - h} width={2} height={h} rx={0.5} style={{ "--i": i } as CSSProperties} />
      ))}
    </svg>
  );
}
