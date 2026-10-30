// Sub-threads in the thread list (docs/design/lead-agent-and-subthreads.md):
// a thread an agent (or the person) opened from another sits under it,
// indented, whatever its own state; one whose parent is not in the list
// stands on its own.

export interface Nested<T> {
  item: T;
  // 0 for a thread, 1 for a sub-thread under its parent, and so on.
  depth: number;
}

// tops are the threads that lead the list, in order; all is every thread,
// for their sub-threads, which follow each parent newest first.
export function nest<T>(
  tops: readonly T[],
  all: readonly T[],
  idOf: (x: T) => string,
  parentOf: (x: T) => string,
  newer: (a: T, b: T) => number,
): Nested<T>[] {
  const kids = new Map<string, T[]>();
  for (const x of all) {
    const p = parentOf(x);
    if (p === "") continue;
    kids.set(p, [...(kids.get(p) ?? []), x]);
  }
  const out: Nested<T>[] = [];
  const seen = new Set<string>();
  const walk = (x: T, depth: number) => {
    const id = idOf(x);
    if (seen.has(id)) return;
    seen.add(id);
    out.push({ item: x, depth });
    for (const k of [...(kids.get(id) ?? [])].sort(newer)) walk(k, depth + 1);
  };
  for (const x of tops) walk(x, 0);
  return out;
}

// Whether a thread leads the list: it has no parent, or its parent is not
// among the threads listed.
export function isTop<T>(x: T, ids: ReadonlySet<string>, parentOf: (x: T) => string): boolean {
  const p = parentOf(x);
  return p === "" || !ids.has(p);
}
