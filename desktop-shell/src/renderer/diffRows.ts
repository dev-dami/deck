import { type DiffLine, parseDiff } from "@web/diff";
import { pairLines } from "./wordDiff";

// A unified diff as the diff panel draws it. Each hunk starts with its
// header, which says how many lines it skips and names the function it is
// in. Each line carries its number in the old and the new file; num is the
// one a comment cites (the old one for a removed line).
export type LineKind = "add" | "del" | "ctx";
export interface LineRow {
  k: "line";
  kind: LineKind;
  num: number | null;
  oldNum: number | null;
  newNum: number | null;
  text: string;
}
export interface HunkRow {
  k: "hunk";
  // "@@ -12,7 +12,9 @@" and the function context after it.
  header: string;
  context: string;
  skipped: number | null;
  first: boolean;
}
export type DiffRow = LineRow | HunkRow | { k: "meta"; text: string };

const HUNK = /^(@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@)\s?(.*)$/;

export function diffRows(diff: string): DiffRow[] {
  const out: DiffRow[] = [];
  let oldNo = 0;
  let newNo = 0;
  // Where the last hunk ended in the new file, to count the skipped lines.
  let lastEnd: number | null = null;
  for (const l of parseDiff(diff) as DiffLine[]) {
    if (l.kind === "hunk") {
      const m = HUNK.exec(l.text);
      const first = !out.some((r) => r.k === "hunk");
      if (m === null) {
        out.push({ k: "hunk", header: l.text, context: "", skipped: null, first });
        continue;
      }
      oldNo = Number(m[2]);
      newNo = Number(m[4]);
      const skipped = lastEnd === null ? newNo - 1 : newNo - lastEnd;
      out.push({ k: "hunk", header: m[1] ?? "", context: (m[6] ?? "").trim(), skipped: skipped > 0 ? skipped : null, first });
      continue;
    }
    if (l.kind === "meta") {
      // File headers (diff --git, ---, +++) say nothing the panel does not.
      if (!/^(diff |index |--- |\+\+\+ |new file|deleted file|similarity |rename |old mode|new mode)/.test(l.text)) {
        out.push({ k: "meta", text: l.text });
      }
      continue;
    }
    const text = l.text.slice(1);
    if (l.kind === "add") {
      out.push({ k: "line", kind: "add", num: newNo, oldNum: null, newNum: newNo, text });
      newNo++;
    } else if (l.kind === "del") {
      out.push({ k: "line", kind: "del", num: oldNo, oldNum: oldNo, newNum: null, text });
      oldNo++;
    } else {
      out.push({ k: "line", kind: "ctx", num: newNo, oldNum: oldNo, newNum: newNo, text });
      newNo++;
      oldNo++;
    }
    lastEnd = newNo;
  }
  return out;
}

// Whether a hunk's header earns a row: not for a file's first hunk at its
// top with no function to name.
export function showHunk(r: HunkRow): boolean {
  return !r.first || r.skipped !== null || r.context !== "";
}

// The new path a diff names in its +++ header, if it has one.
export function pathOfDiff(diff: string): string | null {
  const m = /^\+\+\+ (?:b\/)?(.+)$/m.exec(diff);
  if (m === null) return null;
  const p = (m[1] ?? "").trim();
  return p === "/dev/null" || p === "" ? null : p;
}

// Which removed row goes with which added row: each block of removed lines
// and the added lines right after it pair up by pairLines. The map holds
// both directions.
export function pairRows(rows: readonly DiffRow[]): Map<number, number> {
  const out = new Map<number, number>();
  let i = 0;
  while (i < rows.length) {
    const dels: number[] = [];
    const adds: number[] = [];
    while (i < rows.length && isLine(rows[i], "del")) dels.push(i++);
    while (i < rows.length && isLine(rows[i], "add")) adds.push(i++);
    if (dels.length === 0 && adds.length === 0) {
      i++;
      continue;
    }
    const text = (ix: number[]) => ix.map((r) => (rows[r] as LineRow).text);
    for (const [d, a] of pairLines(text(dels), text(adds))) {
      const dr = dels[d] as number;
      const ar = adds[a] as number;
      out.set(dr, ar);
      out.set(ar, dr);
    }
  }
  return out;
}

function isLine(r: DiffRow | undefined, kind: LineKind): boolean {
  return r !== undefined && r.k === "line" && r.kind === kind;
}

// One row of the split view: indexes into the rows, old on the left and
// new on the right; null is a blank filler.
export type SplitRow = { k: "pair"; left: number | null; right: number | null } | { k: "row"; i: number };

// Lines a context line shows on both sides; a removed line pairs with its
// added line on one row, and the rest get a blank across from them.
export function splitRows(rows: readonly DiffRow[], pairs: ReadonlyMap<number, number>): SplitRow[] {
  const out: SplitRow[] = [];
  let i = 0;
  while (i < rows.length) {
    const r = rows[i] as DiffRow;
    if (r.k !== "line") {
      out.push({ k: "row", i: i++ });
      continue;
    }
    if (r.kind === "ctx") {
      out.push({ k: "pair", left: i, right: i });
      i++;
      continue;
    }
    const dels: number[] = [];
    const adds: number[] = [];
    while (i < rows.length && isLine(rows[i], "del")) dels.push(i++);
    while (i < rows.length && isLine(rows[i], "add")) adds.push(i++);
    // Lines without a partner fill rows side by side up to the next pair.
    let d = 0;
    let a = 0;
    const fill = (dEnd: number, aEnd: number) => {
      while (d < dEnd || a < aEnd) {
        out.push({ k: "pair", left: d < dEnd ? (dels[d++] as number) : null, right: a < aEnd ? (adds[a++] as number) : null });
      }
    };
    dels.forEach((dr, k) => {
      const ar = pairs.get(dr);
      const at = ar === undefined ? -1 : adds.indexOf(ar);
      if (at < a) return;
      fill(k, at);
      out.push({ k: "pair", left: dr, right: ar as number });
      d = k + 1;
      a = at + 1;
    });
    fill(dels.length, adds.length);
  }
  return out;
}

// A run of unchanged lines folded away until clicked.
export interface Fold<T> {
  k: "fold";
  items: T[];
}

// Folds the middle of every long run of context, keeping keep lines next
// to each change. A run at the start or end of a hunk keeps lines only on
// its changed side. A fold hides at least min lines.
export function foldContext<T>(items: readonly T[], isCtx: (t: T) => boolean, isEdge: (t: T) => boolean, keep = 3, min = 6): (T | Fold<T>)[] {
  const out: (T | Fold<T>)[] = [];
  let i = 0;
  while (i < items.length) {
    if (!isCtx(items[i] as T)) {
      out.push(items[i++] as T);
      continue;
    }
    const start = i;
    while (i < items.length && isCtx(items[i] as T)) i++;
    const run = items.slice(start, i);
    const before = start === 0 || isEdge(items[start - 1] as T) ? 0 : keep;
    const after = i === items.length || isEdge(items[i] as T) ? 0 : keep;
    if (run.length - before - after < min) {
      out.push(...run);
      continue;
    }
    out.push(...run.slice(0, before), { k: "fold", items: run.slice(before, run.length - after) }, ...run.slice(run.length - after));
  }
  return out;
}

export function isFold<T>(x: T | Fold<T>): x is Fold<T> {
  return typeof x === "object" && x !== null && (x as { k?: unknown }).k === "fold" && Array.isArray((x as { items?: unknown }).items);
}
