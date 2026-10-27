import type { Message, Part } from "@web/protocol";
import { type Chunk, groupParts } from "@web/transcript";
import { subParent } from "./lanes";

// Tool calls fold into "actions" (docs/design-glass.md, section 5): an
// agent's turn reads as its words, with the run of calls between them as
// one row per run ("@ada · 14 actions — read 6 files, ran 3 commands"),
// which opens to the calls. Pure, so the tests drive it directly; the
// Timeline renders it and the Work Graph draws a run as one node.
//
// Deck stores a turn as several messages: a step's text and calls in one,
// each result in a message of its own with no agent, and the text after a
// call in the next. A turn here is the run of one agent's messages, with
// each result paired back to its call and the result messages dropped.

export type ActionKind = "command" | "edit" | "search" | "read" | "web" | "other";

const COMMAND = /bash|shell|exec|command|terminal|run/;

// What a tool does, by its name as agents name them.
export function actionKind(name: string): ActionKind {
  const n = name.toLowerCase();
  if (COMMAND.test(n)) return "command";
  if (/edit|write|patch|apply|create|multiedit/.test(n)) return "edit";
  if (/grep|glob|search|find|ls/.test(n)) return "search";
  if (/read|view|cat|open/.test(n)) return "read";
  if (/fetch|web|http|browse/.test(n)) return "web";
  return "other";
}

export type ToolChunk = Extract<Chunk, { k: "tool" }>;
export type TextChunk = Extract<Chunk, { k: "text" }>;

export const toolName = (c: ToolChunk): string => c.call?.name ?? c.result?.name ?? "tool";

// One run of calls: its id (stable while the run grows), the agent's, and
// its items in order: the calls, the thinking between them, and the short
// notes an agent writes between two calls ("Now the tests.").
export interface ActionGroup {
  id: string;
  agent: string;
  items: Chunk[];
}

export type Piece = { k: "chunk"; c: Chunk } | { k: "actions"; g: ActionGroup };

// Narration between two calls this short, in one paragraph, folds into the
// run; longer text, or text with a fence, a list or a heading, stays out.
export const NOTE_MAX = 160;

const isWork = (c: Chunk): boolean => c.k === "tool" || c.k === "reasoning";

export function isNote(c: Chunk): boolean {
  if (c.k !== "text") return false;
  const t = c.text.trim();
  return t.length <= NOTE_MAX && !t.includes("\n\n") && !/^\s*(```|[-*+] |#|\d+\. )/m.test(t);
}

// A turn's chunks as pieces: each run of work with at least one call is a
// group; text, images and thinking with no call around it stay as they are.
// A short note between two calls joins the run. idOf names a group by the
// index of its first chunk.
export function pieces(chunks: readonly Chunk[], agent: string, idOf: (i: number) => string = String): Piece[] {
  const out: Piece[] = [];
  let i = 0;
  while (i < chunks.length) {
    if (!isWork(chunks[i]!)) {
      out.push({ k: "chunk", c: chunks[i]! });
      i++;
      continue;
    }
    // The run: work, and notes that have work after them.
    let end = i;
    let j = i;
    while (j < chunks.length) {
      const c = chunks[j]!;
      if (isWork(c)) end = j;
      else if (!isNote(c)) break;
      j++;
    }
    const run = chunks.slice(i, end + 1);
    if (run.some((c) => c.k === "tool")) out.push({ k: "actions", g: { id: idOf(i), agent, items: run } });
    else for (const c of run) out.push({ k: "chunk", c });
    i = end + 1;
  }
  return out;
}

export interface ActionStats {
  // Calls, those that failed, and those still waiting on a result.
  actions: number;
  failed: number;
  pending: number;
  byKind: Record<ActionKind, number>;
  // The files read and edited, each once, by the call's summary.
  read: string[];
  edited: string[];
  // The call that runs now (the last without a result), else the last one.
  current: ToolChunk | null;
}

export function actionStats(items: readonly Chunk[], running: boolean): ActionStats {
  const s: ActionStats = {
    actions: 0,
    failed: 0,
    pending: 0,
    byKind: { command: 0, edit: 0, search: 0, read: 0, web: 0, other: 0 },
    read: [],
    edited: [],
    current: null,
  };
  let waiting: ToolChunk | null = null;
  for (const c of items) {
    if (c.k !== "tool") continue;
    s.actions++;
    s.current = c;
    if (c.result?.is_error === true) s.failed++;
    if (c.result === null && running) {
      s.pending++;
      waiting = c;
    }
    const kind = actionKind(toolName(c));
    s.byKind[kind]++;
    const file = (c.call?.summary ?? "").trim();
    const list = kind === "read" ? s.read : kind === "edit" ? s.edited : null;
    if (list !== null && file !== "" && !list.includes(file)) list.push(file);
  }
  if (waiting !== null) s.current = waiting;
  return s;
}

const plural = (n: number, one: string, many = `${one}s`) => `${n} ${n === 1 ? one : many}`;

// "read 6 files, ran 3 commands, edited 2 files": what a run did, the most
// common first, ties in a fixed order.
export function actionSummary(s: ActionStats): string {
  // Files when the calls name them, else the calls.
  const files = (list: string[], calls: number) => (list.length > 0 ? list.length : calls);
  const parts: { n: number; text: string }[] = [
    { n: s.byKind.read, text: `read ${plural(files(s.read, s.byKind.read), "file")}` },
    { n: s.byKind.command, text: `ran ${plural(s.byKind.command, "command")}` },
    { n: s.byKind.edit, text: `edited ${plural(files(s.edited, s.byKind.edit), "file")}` },
    { n: s.byKind.search, text: `searched ${plural(s.byKind.search, "time")}` },
    { n: s.byKind.web, text: `fetched ${plural(s.byKind.web, "page")}` },
    { n: s.byKind.other, text: `used ${plural(s.byKind.other, "other tool")}` },
  ];
  return parts
    .map((p, i) => ({ ...p, i }))
    .filter((p) => p.n > 0)
    .sort((a, b) => b.n - a.n || a.i - b.i)
    .map((p) => p.text)
    .join(", ");
}

// ---- turns ------------------------------------------------------------------

// One agent's run of messages as one row of the timeline.
export interface Turn {
  k: "turn";
  // The first message's id: the row's key.
  id: string;
  agent: string;
  // For the work of a subagent its member's CLI started with its own tool
  // (agent "<member>/<name>"), the member; else null.
  parent: string | null;
  msgs: Message[];
  // The results paired from other messages, in the order they were used:
  // the row changes when one arrives.
  paired: Part[];
  chunks: Chunk[];
  finished: boolean;
}

// The chunks of a message, kept while the message object is the same.
const chunkMemo = new WeakMap<Message, Chunk[]>();
// The message each chunk of a turn came from, and its index in it.
const origin = new WeakMap<Chunk, { mid: string; index: number }>();

export function chunkOrigin(c: Chunk): { mid: string; index: number } | undefined {
  return origin.get(c);
}

function chunksOfMessage(m: Message): Chunk[] {
  let got = chunkMemo.get(m);
  if (got === undefined) {
    got = groupParts(m.parts);
    got.forEach((c, index) => origin.set(c, { mid: m.id, index }));
    chunkMemo.set(m, got);
  }
  return got;
}

// The tool_call ids each message holds.
function callIds(m: Message): string[] {
  const out: string[] = [];
  for (const p of m.parts) if (p.type === "tool_call" && p.id !== undefined) out.push(p.id);
  return out;
}

// A result that belongs to a call in another message: Deck stores each in
// a message of its own.
interface Pairing {
  // Call id to its result, from another message.
  results: Map<string, Part>;
  // Messages whose every part is such a result: nothing of their own to show.
  consumed: Set<string>;
  // The agent of each call, for a result that stays on its own.
  callAgent: Map<string, string>;
}

export function pairResults(messages: readonly Message[]): Pairing {
  const home = new Map<string, string>();
  const callAgent = new Map<string, string>();
  for (const m of messages) {
    for (const id of callIds(m)) {
      home.set(id, m.id);
      if (m.agent !== undefined) callAgent.set(id, m.agent);
    }
  }
  const results = new Map<string, Part>();
  const consumed = new Set<string>();
  for (const m of messages) {
    if (m.role !== "assistant") continue;
    let own = 0;
    for (const p of m.parts) {
      const call = p.type === "tool_result" ? p.tool_call_id : undefined;
      const at = call === undefined ? undefined : home.get(call);
      if (call !== undefined && at !== undefined && at !== m.id) results.set(call, p);
      else own++;
    }
    if (own === 0 && m.parts.length > 0) consumed.add(m.id);
  }
  return { results, consumed, callAgent };
}

// A message's agent: its own, else the agent whose call its first result
// answers, else the agent before it.
function agentOf(m: Message, pairing: Pairing, before: string | null): string {
  if (m.agent !== undefined && m.agent !== "") return m.agent;
  for (const p of m.parts) {
    const a = p.type === "tool_result" && p.tool_call_id !== undefined ? pairing.callAgent.get(p.tool_call_id) : undefined;
    if (a !== undefined) return a;
  }
  return before ?? "agent";
}

function turnChunks(msgs: readonly Message[], pairing: Pairing): { chunks: Chunk[]; paired: Part[] } {
  const chunks: Chunk[] = [];
  const paired: Part[] = [];
  for (const m of msgs) {
    for (const c of chunksOfMessage(m)) {
      // A lone result paired to its call elsewhere shows there.
      if (c.k === "tool" && c.call === null && c.result?.tool_call_id !== undefined && pairing.results.get(c.result.tool_call_id) === c.result) continue;
      const id = c.k === "tool" && c.result === null ? c.call?.id : undefined;
      const result = id === undefined ? undefined : pairing.results.get(id);
      if (c.k !== "tool" || result === undefined) {
        chunks.push(c);
        continue;
      }
      const joined: Chunk = { ...c, result };
      const o = origin.get(c);
      if (o !== undefined) origin.set(joined, o);
      chunks.push(joined);
      paired.push(result);
    }
  }
  return { chunks, paired };
}

const sameList = <T>(a: readonly T[], b: readonly T[]) => a.length === b.length && a.every((x, i) => x === b[i]);

// A row that is not an agent's turn: a person's or a system message, or
// anything else the timeline puts between them (a room note).
export type TurnRow<R> = R | Message | Turn;

// The rows of a timeline: each run of one agent's messages as one Turn,
// the result messages folded into the calls they answer; everything else
// as it was. prev, the last call's turns by id, keeps a turn that did not
// change the same object, so its row does not render again.
//
// The work of a CLI-native subagent (agent "<member>/<name>") is one Turn
// per subagent from its first message until the person speaks again, its
// parallel siblings' messages in between or not; and its member's run goes
// on past the blocks of its own subagents, so they read as its children,
// under it.
export function buildTurns<R>(
  rows: readonly (Message | R)[],
  isMessage: (r: Message | R) => r is Message,
  prev?: ReadonlyMap<string, Turn>,
): TurnRow<R>[] {
  const messages = rows.filter(isMessage);
  const pairing = pairResults(messages);
  type Open = { k: "open"; agent: string; msgs: Message[] };
  const out: (Message | R | Open)[] = [];
  const subs = new Map<string, Open>();
  const asOpen = (r: unknown) => r as Partial<Open> | undefined;
  let lastAgent: string | null = null;
  for (const r of rows) {
    if (!isMessage(r)) {
      out.push(r);
      continue;
    }
    if (r.role !== "assistant") {
      if (r.role === "user") subs.clear();
      out.push(r);
      continue;
    }
    if (pairing.consumed.has(r.id)) continue;
    const agent = agentOf(r, pairing, lastAgent);
    if (subParent(agent) !== null) {
      const open = subs.get(agent);
      if (open !== undefined) open.msgs.push(r);
      else {
        const o: Open = { k: "open", agent, msgs: [r] };
        subs.set(agent, o);
        out.push(o);
      }
      continue;
    }
    lastAgent = agent;
    // The tail, past the blocks of this agent's own subagents.
    let at = out.length - 1;
    while (at >= 0 && asOpen(out[at])?.k === "open" && subParent(asOpen(out[at])!.agent!) === agent) at--;
    const tail = asOpen(out[at]);
    if (tail?.k === "open" && tail.agent === agent) tail.msgs!.push(r);
    else out.push({ k: "open", agent, msgs: [r] });
  }
  return out.map((r) => {
    const o = r as { k?: string; agent?: string; msgs?: Message[] };
    if (o.k !== "open") return r as TurnRow<R>;
    const msgs = o.msgs!;
    const { chunks, paired } = turnChunks(msgs, pairing);
    const id = msgs[0]!.id;
    const old = prev?.get(id);
    if (old !== undefined && old.agent === o.agent && sameList(old.msgs, msgs) && sameList(old.paired, paired)) return old;
    return { k: "turn", id, agent: o.agent!, parent: subParent(o.agent!), msgs, paired, chunks, finished: msgs.every((m) => m.finished) } satisfies Turn;
  });
}

export const isTurn = (r: unknown): r is Turn => typeof r === "object" && r !== null && (r as { k?: unknown }).k === "turn";

// A group's id from a turn's chunk index: the message it starts in and the
// chunk's index there, so it stays put as the turn grows.
export function groupId(t: Turn, i: number): string {
  const c = t.chunks[i];
  const o = c === undefined ? undefined : origin.get(c);
  return o === undefined ? `${t.id}:${i}` : `${o.mid}:${o.index}`;
}
