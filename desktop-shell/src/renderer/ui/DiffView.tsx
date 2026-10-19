import { parseDiff } from "@web/diff";
import { useMemo } from "react";
import { diffRows } from "../diffRows";

// numbered: the diff panel's view, with line numbers, change bars and
// folds between hunks. Without it, the compact view of a tool call.
export function DiffView({
  diff,
  truncated,
  numbered = false,
}: {
  diff: string;
  truncated?: boolean | undefined;
  numbered?: boolean;
}) {
  const lines = useMemo(() => (numbered ? [] : parseDiff(diff)), [diff, numbered]);
  const rows = useMemo(() => (numbered ? diffRows(diff) : []), [diff, numbered]);
  if (numbered) {
    return (
      <div className="diff diff-numbered py-1" role="table" aria-label="Diff">
        {rows.map((r, i) =>
          r.k === "fold" ? (
            <div key={i} className="diff-fold" role="row">
              {r.skipped === null ? "…" : `${r.skipped} unmodified line${r.skipped === 1 ? "" : "s"}`}
            </div>
          ) : r.k === "meta" ? (
            <div key={i} className="diff-line diff-meta" role="row">
              {r.text}
            </div>
          ) : (
            <div key={i} className={`diff-row diff-${r.kind}`} role="row">
              <span className="diff-gutter">{r.num}</span>
              <span className="diff-code">{r.text === "" ? " " : r.text}</span>
            </div>
          ),
        )}
        {truncated === true && <div className="diff-line diff-meta">The diff is cut short.</div>}
      </div>
    );
  }
  return (
    <div className="diff py-1" role="table" aria-label="Diff">
      {lines.map((l, i) => (
        <div key={i} className={`diff-line diff-${l.kind}`} role="row">
          {l.text === "" ? " " : l.text}
        </div>
      ))}
      {truncated === true && <div className="diff-line diff-meta">The diff is cut short.</div>}
    </div>
  );
}
