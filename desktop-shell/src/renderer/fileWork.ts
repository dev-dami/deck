import type { Message } from "@web/protocol";
import { toMillis } from "@web/time";
import { actionKind, buildTurns, chunkOrigin, isTurn, type ToolChunk, toolName } from "./actions";
import { messageText } from "./messages";
import type { Lease } from "./roomMeta";

// Who edits which file, and the asks between agents over them (the engine's
// edit gate, internal/deck/session/coord.go): before an agent edits a file
// another is changing or holds, Deck asks that agent, and the thread shows
// the ask as one system line that its answer rewrites in place. The Work
// Graph reads a thread's messages back into a timeline per file: the edits
// (each run's edit calls, with their diffs) and the asks, and from them the
// file's owner now. Pure, so the tests drive it directly.

export type AskState = "pending" | "granted" | "held" | "released";

export interface AskLine {
  from: string;
  to: string;
  path: string;
  state: AskState;
  // Who answered: the owner, or null when Deck let it go.
  by: string | null;
  answer: string;
}

// The engine's line (session.AskLine):
//   @codex asked @ada about `a.go`
//   @codex asked @ada about `a.go` → ada: go ahead, I'm done
//   @codex asked @ada about `a.go` → ada (wait): still on it
//   @codex asked @ada about `a.go` → deck: ada finished its turn
const ASK = /^@(\S+) asked @(\S+) about `([^`]+)`(?: → (\S+?)( \(wait\))?: ([\s\S]*))?$/;

export function parseAskLine(text: string): AskLine | null {
  const m = ASK.exec(text.trim());
  if (m === null) return null;
  const [, from, to, path, who, wait, answer] = m;
  if (who === undefined) return { from: from!, to: to!, path: path!, state: "pending", by: null, answer: "" };
  const deck = who === "deck";
  return {
    from: from!,
    to: to!,
    path: path!,
    state: wait !== undefined ? "held" : deck ? "released" : "granted",
    by: deck ? null : who,
    answer: answer ?? "",
  };
}

// An ask in a thread: its line, and the system message that shows it.
export interface FileAsk extends AskLine {
  mid: string;
  at: number;
}

export function askOf(m: Message): FileAsk | null {
  if (m.role !== "system") return null;
  const a = parseAskLine(messageText(m));
  return a === null ? null : { ...a, mid: m.id, at: toMillis(m.created_at) };
}

// One edit call: the agent, when, the message it is in, and its diff ("" when
// the call reported none).
export interface FileEdit {
  agent: string;
  at: number;
  mid: string;
  tool: string;
  diff: string;
  added: number;
  removed: number;
}

export interface FileTrack {
  path: string;
  // Oldest first.
  edits: FileEdit[];
  asks: FileAsk[];
  // Who owns it now: a lease's holder, else whoever had it last (an edit, or
  // a handoff after an ask), the way the engine counts; null for nobody.
  owner: string | null;
  // The agents that hold a lease on it.
  holders: string[];
}

// "./a/b.go", "/repo/a/b.go" and "a/b.go" name one file: the shorter one,
// when one path ends the other.
export function samePath(a: string, b: string): boolean {
  const x = a.replace(/^\.\//, "");
  const y = b.replace(/^\.\//, "");
  return x === y || x.endsWith(`/${y}`) || y.endsWith(`/${x}`);
}

// Lines added and removed by a unified diff.
export function diffStat(diff: string): { added: number; removed: number } {
  let added = 0;
  let removed = 0;
  for (const l of diff.split("\n")) {
    if (l.startsWith("+++") || l.startsWith("---")) continue;
    if (l.startsWith("+")) added++;
    else if (l.startsWith("-")) removed++;
  }
  return { added, removed };
}

const isMessage = (_r: Message): _r is Message => true;

// The edit calls of a thread's messages, oldest first.
export function editsOf(messages: readonly Message[]): (FileEdit & { path: string })[] {
  const out: (FileEdit & { path: string })[] = [];
  const at = new Map(messages.map((m) => [m.id, toMillis(m.created_at)]));
  for (const r of buildTurns<never>(messages, isMessage)) {
    if (!isTurn(r)) continue;
    for (const c of r.chunks) {
      if (c.k !== "tool") continue;
      const t = c as ToolChunk;
      const name = toolName(t);
      const path = (t.call?.summary ?? "").trim();
      if (actionKind(name) !== "edit" || path === "" || t.result?.is_error === true) continue;
      const mid = chunkOrigin(c)?.mid ?? r.id;
      const diff = t.result?.diff ?? "";
      out.push({ path, agent: r.agent, at: at.get(mid) ?? 0, mid, tool: name, diff, ...diffStat(diff) });
    }
  }
  return out;
}

// Every file a thread edited or asked about, by path, each with its
// timeline and owner.
export function fileWork(messages: readonly Message[], leases: readonly Lease[] = []): Map<string, FileTrack> {
  const files = new Map<string, FileTrack>();
  const track = (path: string): FileTrack => {
    for (const [p, f] of files) if (samePath(p, path)) return f;
    const f: FileTrack = { path, edits: [], asks: [], owner: null, holders: [] };
    files.set(path, f);
    return f;
  };
  for (const e of editsOf(messages)) {
    const { path, ...edit } = e;
    track(path).edits.push(edit);
  }
  for (const m of messages) {
    const a = askOf(m);
    if (a !== null) {
      const f = track(a.path);
      // The ask's path is the repository's; an edit may have named it longer.
      if (a.path.length < f.path.length) {
        files.delete(f.path);
        f.path = a.path;
        files.set(f.path, f);
      }
      f.asks.push(a);
    }
  }
  for (const f of files.values()) {
    f.holders = leases.filter((l) => l.paths.some((p) => leaseCovers(p, f.path))).map((l) => l.agent);
    // The last event: an edit, or a handoff (an ask let go or granted).
    let last: { agent: string; at: number } | null = null;
    for (const e of f.edits) if (last === null || e.at >= last.at) last = { agent: e.agent, at: e.at };
    for (const a of f.asks) if (a.state === "granted" || a.state === "released") if (last === null || a.at >= last.at) last = { agent: a.from, at: a.at };
    f.owner = f.holders[0] ?? last?.agent ?? null;
  }
  return files;
}

// Whether a lease's path or glob covers path; graph.ts has the full match.
function leaseCovers(lease: string, path: string): boolean {
  if (!/[*?[]/.test(lease)) return samePath(lease, path) || path.startsWith(lease.endsWith("/") ? lease : `${lease}/`);
  const re = lease
    .split("**")
    .map((part) => part.replace(/[.+^${}()|[\]\\]/g, "\\$&").replace(/\*/g, "[^/]*").replace(/\?/g, "[^/]"))
    .join(".*");
  return new RegExp(`(^|/)${re}$`).test(path);
}

// The asks waiting for an answer, oldest first.
export const pendingAsks = (f: FileTrack): FileAsk[] => f.asks.filter((a) => a.state === "pending");

// The distinct agents that edited a file, in the order of their last edit.
export function editors(f: FileTrack): string[] {
  const out: string[] = [];
  for (const e of [...f.edits].reverse()) if (!out.includes(e.agent)) out.unshift(e.agent);
  return out;
}
