import type { Chunk } from "@web/transcript";

// A finished reply folds its work (thinking, tool calls and the narration
// between them) behind "Worked for …", as T3 Code does; the text after the
// last piece of work stays in view. A reply without work has nothing to fold.
export function splitWork(chunks: Chunk[]): { work: Chunk[]; answer: Chunk[] } {
  let last = -1;
  chunks.forEach((c, i) => {
    if (c.k === "tool" || c.k === "reasoning") last = i;
  });
  if (last < 0) return { work: [], answer: chunks };
  return { work: chunks.slice(0, last + 1), answer: chunks.slice(last + 1) };
}

// "45s", "3m 43s", "1h 2m".
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}
