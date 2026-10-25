// Word-level changes inside a changed line: which removed line goes with
// which added one, and which spans of each pair differ.

// A span of a line, [start, end) in UTF-16 units.
export type Span = [number, number];

export interface WordChange {
  del: Span[];
  add: Span[];
}

// Words, runs of white space and single marks.
const TOKEN = /[\p{L}\p{N}_]+|\s+|[^\p{L}\p{N}_\s]/gu;

// Past these the line is marked whole, not word by word.
const MAX_LEN = 1000;
const MAX_TOKENS = 300;
// Below this share of common text the lines are different lines.
const MIN_COMMON = 0.3;

function tokens(s: string): string[] {
  return s.match(TOKEN) ?? [];
}

// The changed spans of two versions of a line, by a longest common
// subsequence over their tokens. Null when the lines share too little for
// word marks to help, or are too long to compare.
export function wordDiff(a: string, b: string): WordChange | null {
  if (a.length > MAX_LEN || b.length > MAX_LEN) return null;
  if (a === b) return { del: [], add: [] };
  const ta = tokens(a);
  const tb = tokens(b);
  const n = ta.length;
  const m = tb.length;
  if (n > MAX_TOKENS || m > MAX_TOKENS) return null;
  // lcs[i * w + j]: the common length of ta[i:] and tb[j:].
  const w = m + 1;
  const lcs = new Uint16Array((n + 1) * w);
  const at = (i: number, j: number): number => lcs[i * w + j] ?? 0;
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      lcs[i * w + j] = ta[i] === tb[j] ? at(i + 1, j + 1) + 1 : Math.max(at(i + 1, j), at(i, j + 1));
    }
  }
  const keepA = new Array<boolean>(n).fill(false);
  const keepB = new Array<boolean>(m).fill(false);
  let common = 0;
  for (let i = 0, j = 0; i < n && j < m; ) {
    if (ta[i] === tb[j]) {
      keepA[i] = true;
      keepB[j] = true;
      common += (ta[i] ?? "").length;
      i++;
      j++;
    } else if (at(i + 1, j) >= at(i, j + 1)) i++;
    else j++;
  }
  if ((2 * common) / (a.length + b.length) < MIN_COMMON) return null;
  return { del: spans(ta, keepA), add: spans(tb, keepB) };
}

// The spans of the tokens not kept. White space alone between two changed
// tokens joins them, so a changed phrase reads as one mark.
function spans(toks: string[], keep: boolean[]): Span[] {
  const out: Span[] = [];
  let at = 0;
  for (let i = 0; i < toks.length; i++) {
    const t = toks[i] ?? "";
    const end = at + t.length;
    const bridge = keep[i] === true && /^\s+$/.test(t) && keep[i - 1] === false && keep[i + 1] === false;
    if (keep[i] !== true || bridge) {
      const last = out[out.length - 1];
      if (last !== undefined && last[1] === at) last[1] = end;
      else out.push([at, end]);
    }
    at = end;
  }
  return out;
}

function bigrams(s: string): Map<string, number> {
  const out = new Map<string, number>();
  const t = s.trim();
  for (let i = 0; i < t.length - 1; i++) {
    const g = t.slice(i, i + 2);
    out.set(g, (out.get(g) ?? 0) + 1);
  }
  return out;
}

// How alike two lines are, from 0 to 1 (Dice over character pairs). It is
// cheap, so a block of changed lines can compare every pair.
export function similarity(a: string, b: string): number {
  if (a.trim() === b.trim()) return 1;
  const ga = bigrams(a);
  const gb = bigrams(b);
  let total = 0;
  let both = 0;
  for (const n of ga.values()) total += n;
  for (const [g, n] of gb) {
    total += n;
    both += Math.min(n, ga.get(g) ?? 0);
  }
  return total === 0 ? 0 : (2 * both) / total;
}

// Lines this alike pair up when the counts differ.
const MIN_PAIR = 0.4;
// A bigger block pairs by position.
const MAX_BLOCK = 80;

// Which removed line goes with which added line in one block of changes,
// as [removed index, added index], both rising. Equal counts pair by
// position; otherwise the pairs that are most alike in total.
export function pairLines(dels: readonly string[], adds: readonly string[]): [number, number][] {
  const n = dels.length;
  const m = adds.length;
  if (n === 0 || m === 0) return [];
  if (n === m || n > MAX_BLOCK || m > MAX_BLOCK) {
    return Array.from({ length: Math.min(n, m) }, (_, i): [number, number] => [i, i]);
  }
  const sim = new Float64Array(n * m);
  for (let i = 0; i < n; i++) for (let j = 0; j < m; j++) sim[i * m + j] = similarity(dels[i] ?? "", adds[j] ?? "");
  const s = (i: number, j: number): number => sim[i * m + j] ?? 0;
  // best[i * w + j]: the best total for dels[i:] and adds[j:].
  const w = m + 1;
  const best = new Float64Array((n + 1) * w);
  const b = (i: number, j: number): number => best[i * w + j] ?? 0;
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      const take = s(i, j) >= MIN_PAIR ? s(i, j) + b(i + 1, j + 1) : -1;
      best[i * w + j] = Math.max(take, b(i + 1, j), b(i, j + 1));
    }
  }
  const out: [number, number][] = [];
  for (let i = 0, j = 0; i < n && j < m; ) {
    if (s(i, j) >= MIN_PAIR && b(i, j) === s(i, j) + b(i + 1, j + 1)) {
      out.push([i, j]);
      i++;
      j++;
    } else if (b(i, j) === b(i + 1, j)) i++;
    else j++;
  }
  return out;
}
