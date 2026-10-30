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

// A parent agent's brief or follow-up to a sub-thread (session/subthread.go)
// is a user message whose agent is that agent. Its text opens with Deck's
// own line to the sub-thread's agent ('[deck] claude, the agent of the
// thread "Fix the limiter", opened this sub-thread for you. …'), which the
// clients fold: they show the brief as the agent's, under who sent it.
export interface Brief {
  from: string;
  // The parent thread's title as Deck's line names it; "" when it does not.
  thread: string;
  // The brief without Deck's line.
  body: string;
  // The brief that opened the sub-thread, not a follow-up.
  first: boolean;
}

const THREAD = /the thread "((?:[^"\\]|\\.)*)"/;

// The title Go's %q wrote, unquoted.
function unquote(s: string): string {
  try {
    return JSON.parse(`"${s}"`) as string;
  } catch {
    return s;
  }
}

// The brief a message is, or null for the person's own message.
export function briefOf(m: {
  role: string;
  agent?: string;
  parts: readonly { type: string; text?: string }[];
}): Brief | null {
  if (m.role !== "user" || m.agent === undefined || m.agent === "") return null;
  const text = m.parts
    .filter((p) => p.type === "text")
    .map((p) => p.text ?? "")
    .join("\n\n")
    .trim();
  if (!text.startsWith("[deck] ")) return { from: m.agent, thread: "", body: text, first: false };
  const cut = text.indexOf("\n\n");
  const intro = cut < 0 ? text : text.slice(0, cut);
  const t = THREAD.exec(intro);
  return {
    from: m.agent,
    thread: t?.[1] === undefined ? "" : unquote(t[1]),
    body: cut < 0 ? "" : text.slice(cut + 2).trim(),
    first: intro.includes("opened this sub-thread for you"),
  };
}
