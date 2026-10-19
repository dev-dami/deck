import { parseDiff } from "@web/diff";
import { useMemo } from "react";

export function DiffView({ diff, truncated }: { diff: string; truncated?: boolean | undefined }) {
  const lines = useMemo(() => parseDiff(diff), [diff]);
  return (
    <div className="diff" role="table" aria-label="Diff">
      {lines.map((l, i) => (
        <div key={i} className={`diff-line diff-${l.kind}`} role="row">
          {l.text === "" ? " " : l.text}
        </div>
      ))}
      {truncated === true && <div className="diff-line diff-meta">The diff is cut short.</div>}
    </div>
  );
}
