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

// A tool's output cut to its first max lines: the text to show, the number
// of lines in all, and whether lines were left out. A trailing newline does
// not count as a line.
export function capLines(text: string, max: number): { text: string; count: number; cut: boolean } {
  const lines = text.split("\n");
  const count = lines.at(-1) === "" ? lines.length - 1 : lines.length;
  return count > max ? { text: lines.slice(0, max).join("\n"), count, cut: true } : { text, count, cut: false };
}

// "45s", "3m 43s", "1h 2m".
export function formatDuration(ms: number): string {
  const s = Math.max(0, Math.round(ms / 1000));
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ${s % 60}s`;
  return `${Math.floor(m / 60)}h ${m % 60}m`;
}
