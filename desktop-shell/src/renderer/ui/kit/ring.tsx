import { cn } from "../../lib/cn";

// Context use as a 12px ring. It is a measure, not a state, so it stays
// muted; only the warning arc past 85% takes the warning colour.
export function Ring({ pct, className }: { pct: number; className?: string }) {
  const r = 4.5;
  const c = 2 * Math.PI * r;
  return (
    <svg aria-hidden viewBox="0 0 12 12" className={cn("context-ring size-3 shrink-0 -rotate-90", className)}>
      <circle cx="6" cy="6" r={r} fill="none" stroke="currentColor" strokeWidth="1.5" className="text-hairline-strong" />
      <circle
        cx="6"
        cy="6"
        r={r}
        fill="none"
        stroke="currentColor"
        strokeWidth="1.5"
        strokeDasharray={`${(c * pct) / 100} ${c}`}
        className={pct >= 85 ? "text-warning" : "text-muted-foreground"}
      />
    </svg>
  );
}
