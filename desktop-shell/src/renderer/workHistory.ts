import type { AgentLine, Message } from "@web/protocol";
import { toMillis } from "@web/time";
import { type ActionGroup, actionKind, actionStats, buildTurns, chunkOrigin, groupId, isNote, isTurn, pieces, type Turn, toolName } from "./actions";
import type { BoardTask } from "./crew";
import { askOf, diffStat, type FileAsk, samePath } from "./fileWork";
import { type Bounds, boundsOf } from "./graph";
import { messageText } from "./messages";

// The Work Graph's History (docs/design-glass.md, section 7): every
// thread's work over time, where the live graph (graph.ts) shows one
// thread now. Each thread is a band of lanes (you, each agent, the tasks);
// along a lane, its work in time order: the person's prompts, an agent's
// replies and its runs of tool calls (actions.ts), each one node. Edges say
// who asked whom (asks), who handed work on by naming another agent
// (handoff, or reviews when it asks for one), who owns or reviews a task,
// and which files a run read or edited, in a band of files under all the
// threads. Columns follow time and pack: work that runs side by side in two
// lanes shares a column, and an edge never runs backwards. Past CLUSTER_AT
// nodes, an agent's work between two prompts folds into one node that
// opens. Pure and deterministic, so the tests drive it directly.

export interface HistoryThread {
  id: string;
  title: string;
  messages: readonly Message[];
  roster: readonly AgentLine[];
  // The thread's board, when the room knows it (the open one).
  tasks?: readonly BoardTask[];
}

// edit and ask: a file's timeline (fileWork.ts), in a row of its own under
// the threads: each later edit of a file two agents edited or one asked
// about, and each ask between agents over it.
export type HistoryKind = "prompt" | "reply" | "actions" | "task" | "file" | "cluster" | "edit" | "ask";

// What the kind filter switches: prompts and replies, runs of calls, tasks
// and files.
export const HISTORY_KINDS = ["messages", "actions", "tasks", "files"] as const;
export type HistoryFilterKind = (typeof HISTORY_KINDS)[number];

const FILTER_OF: Record<HistoryKind, HistoryFilterKind | null> = {
  prompt: "messages",
  reply: "messages",
  actions: "actions",
  task: "tasks",
  file: "files",
  cluster: null,
  edit: "files",
  ask: "files",
};

export interface HistoryFilter {
  // Agent names to keep; null keeps every agent.
  agents: readonly string[] | null;
  // Thread ids to keep; null keeps every thread.
  threads: readonly string[] | null;
  // The time range (ms), either end open.
  since: number | null;
  until: number | null;
  kinds: readonly HistoryFilterKind[];
}

export const ALL_WORK: HistoryFilter = { agents: null, threads: null, since: null, until: null, kinds: HISTORY_KINDS };

export interface ActionItem {
  name: string;
  summary: string;
  failed: boolean;
}

export interface HistoryNode extends Bounds {
  // "p:<mid>", "r:<mid>:<i>", "a:<group id>", "t:<thread>:<task>", "f:<path>"
  // or "c:<thread>:<agent>:<round>".
  id: string;
  kind: HistoryKind;
  // "" for a file, which belongs to every thread that touched it.
  thread: string;
  lane: string;
  col: number;
  agent: string | null;
  agentKind: string;
  // When it happened (ms), and the message it jumps to; null for a file.
  at: number;
  mid: string | null;
  title: string;
  meta: string;
  // A run of calls: how many, how many failed, whether one runs now, and
  // (opened) the calls.
  actions?: { count: number; failed: number; running: boolean; items: ActionItem[] };
  open?: boolean;
  // A cluster: the nodes it folds.
  members?: number;
  path?: string;
  status?: string;
  // An edit: its diff ("" when the call reported none). An ask: its line.
  diff?: string;
  ask?: FileAsk;
}

// requests: an agent's work → its ask about a file; answers: the ask → the
// asker's next edit of the file, once it was let through.
export type HistoryEdgeKind = "asks" | "handoff" | "reviews" | "owns" | "touches" | "reads" | "requests" | "answers";

export interface HistoryEdge {
  id: string;
  from: string;
  to: string;
  kind: HistoryEdgeKind;
  // The agent whose hue the edge takes when lit.
  agent: string | null;
  agentKind: string;
}

export interface HistoryLane {
  id: string;
  thread: string;
  label: string;
  agent: string | null;
  agentKind: string;
  y: number;
  h: number;
}

export interface HistoryBand {
  thread: string;
  title: string;
  // When its first shown work happened (ms), or 0.
  at: number;
  y: number;
  h: number;
}

export interface HistoryGraph {
  nodes: HistoryNode[];
  edges: HistoryEdge[];
  lanes: HistoryLane[];
  bands: HistoryBand[];
  bounds: Bounds;
  // Everything the filters can pick from, before they apply.
  agents: { name: string; kind: string }[];
  threads: { id: string; title: string }[];
  // The first and last moment of all the work (ms), or 0.
  span: { from: number; to: number };
  clustered: boolean;
}

export const NODE_W = 184;
export const NODE_H = 46;
export const FILE_H = 28;
export const ITEM_H = 18;
// An opened run lists this many calls, then "+N more".
export const ITEMS_MAX = 12;
export const COL_GAP = 28;
export const LANE_GAP = 10;
export const BAND_HEAD = 26;
export const BAND_GAP = 30;
// Room on the left of the lanes for their names.
export const LABEL_W = 120;
export const CLUSTER_AT = 400;
// An edit in a file's timeline, and the least gap between two steps of one.
export const EDIT_W = 120;
const STEP_GAP = 8;

const colX = (col: number) => col * (NODE_W + COL_GAP);

// "@bob", "$bob": the agents a text names, among those the thread knows.
export function mentions(text: string, known: ReadonlySet<string>): string[] {
  const out: string[] = [];
  for (const m of text.matchAll(/(?:^|[\s(,])[@$]([A-Za-z][\w.-]*)/g)) {
    const name = m[1]!.replace(/[.,]+$/, "").toLowerCase();
    const hit = [...known].find((k) => k.toLowerCase() === name);
    if (hit !== undefined && !out.includes(hit)) out.push(hit);
  }
  return out;
}

// A line of text for a card: the first line, without markdown's marks.
export function excerpt(text: string, max = 80): string {
  const line =
    text
      .split("\n")
      .map((l) => l.replace(/^[#>*\-+\d.\s]+/, "").replace(/[*_`]/g, "").trim())
      .find((l) => l !== "") ?? "";
  return line.length > max ? `${line.slice(0, max - 1)}…` : line;
}

// A board's time in ms: seconds when it is small enough to be.
const taskMs = (n: number) => (n > 0 && n < 1e12 ? n * 1000 : n);

const isMessage = (_r: Message): _r is Message => true;

// One thread's events before the filters: its nodes in time order, with
// the round each falls in (a prompt starts a round) and the agents each
// reply names.
interface Event {
  n: HistoryNode;
  seq: number;
  round: number;
  names: string[];
  review: boolean;
  files: Files;
}

// The files a node read and edited, and the diff of each edit call, by path.
interface Files {
  edited: string[];
  read: string[];
  diffs?: Map<string, string>;
}

function threadEvents(
  t: HistoryThread,
  expanded: ReadonlySet<string>,
  seq: { n: number },
): { events: Event[]; agents: Map<string, string>; asks: FileAsk[] } {
  const kindOf = new Map<string, string>();
  for (const l of t.roster) {
    kindOf.set(l.id, l.kind);
    kindOf.set(l.name, l.kind);
  }
  const rows = buildTurns<never>(t.messages, isMessage);
  const agents = new Map<string, string>();
  for (const r of rows) {
    if (isTurn(r) && !agents.has(r.agent)) agents.set(r.agent, kindOf.get(r.agent) ?? r.agent.replace(/[-_]?\d+$/, ""));
  }
  for (const l of t.roster) if (!agents.has(l.id)) agents.set(l.id, l.kind);
  const known = new Set(agents.keys());
  const lane = (agent: string | null) => `${t.id}/${agent ?? "you"}`;
  const events: Event[] = [];
  let round = 0;
  const base = (over: Partial<HistoryNode> & Pick<HistoryNode, "id" | "kind" | "at" | "title">): HistoryNode => ({
    thread: t.id,
    lane: lane(null),
    col: 0,
    agent: null,
    agentKind: "",
    mid: null,
    meta: "",
    x: 0,
    y: 0,
    w: NODE_W,
    h: NODE_H,
    ...over,
  });
  const push = (n: HistoryNode, names: string[] = [], files: Files = { edited: [], read: [] }, review = false) =>
    events.push({ n, seq: seq.n++, round, names, review, files });
  const asks: FileAsk[] = [];

  for (const r of rows) {
    if (!isTurn(r)) {
      const ask = askOf(r);
      if (ask !== null) asks.push(ask);
      if (r.role !== "user") continue;
      round++;
      const text = messageText(r);
      push(base({ id: `p:${r.id}`, kind: "prompt", at: toMillis(r.created_at), mid: r.id, title: excerpt(text) || "(image)", meta: "you" }), mentions(text, known));
      continue;
    }
    turnEvents(r, agents.get(r.agent) ?? "", lane(r.agent), known, expanded, base, push);
  }
  // A task the board gives no time sits after the thread's work.
  const end = events.reduce((m, e) => Math.max(m, e.n.at), 0);
  for (const task of t.tasks ?? []) {
    const owner = task.owner === "" ? null : task.owner;
    push(
      base({
        id: `t:${t.id}:${task.id}`,
        kind: "task",
        lane: `${t.id}/tasks`,
        at: task.updatedAt > 0 ? taskMs(task.updatedAt) : end,
        title: task.title,
        meta: [task.status, owner ?? ""].filter((x) => x !== "").join(" · "),
        status: String(task.status),
        agent: owner,
        agentKind: owner === null ? "" : (agents.get(owner) ?? ""),
      }),
      [...(owner === null ? [] : [owner]), ...(task.reviewer === "" ? [] : [task.reviewer])],
      { edited: task.files, read: [] },
    );
  }
  return { events, agents, asks };
}

// A turn's nodes: a run of calls each, and the text that matters, the
// answer, text long enough to stand alone, or text that names another
// agent. Narration before a call ("Let me look.") goes, as it folds in the
// timeline.
function turnEvents(
  t: Turn,
  kind: string,
  lane: string,
  known: ReadonlySet<string>,
  expanded: ReadonlySet<string>,
  base: (over: Partial<HistoryNode> & Pick<HistoryNode, "id" | "kind" | "at" | "title">) => HistoryNode,
  push: (n: HistoryNode, names?: string[], files?: Files, review?: boolean) => void,
) {
  const ps = pieces(t.chunks, t.agent, (i) => groupId(t, i));
  const atOf = (mid: string | undefined) => toMillis((t.msgs.find((m) => m.id === mid) ?? t.msgs[0]!).created_at);
  const lastText = ps.findLastIndex((p) => p.k === "chunk" && p.c.k === "text");
  const own = { agent: t.agent, agentKind: kind, lane };
  ps.forEach((p, i) => {
    if (p.k === "actions") {
      push(actionsNode(p.g, t, own, expanded, base, atOf), [], actionFiles(p.g, t.finished));
      return;
    }
    if (p.c.k !== "text") return;
    const text = p.c.text;
    const names = mentions(text, known).filter((n) => n !== t.agent);
    if (i !== lastText && names.length === 0 && isNote(p.c)) return;
    const o = chunkOrigin(p.c);
    const mid = o?.mid ?? t.id;
    push(
      base({ id: `r:${mid}:${o?.index ?? i}`, kind: "reply", at: atOf(mid), mid, title: excerpt(text), meta: t.agent, ...own }),
      names,
      undefined,
      /\breview/i.test(text),
    );
  });
}

function actionFiles(g: ActionGroup, finished: boolean): Files {
  const s = actionStats(g.items, !finished);
  const diffs = new Map<string, string>();
  for (const c of g.items) {
    if (c.k !== "tool" || actionKind(toolName(c)) !== "edit") continue;
    const path = (c.call?.summary ?? "").trim();
    const diff = c.result?.diff ?? "";
    if (path !== "" && diff !== "") diffs.set(path, [diffs.get(path), diff].filter((d) => d !== undefined).join("\n"));
  }
  return { edited: s.edited, read: s.read, diffs };
}

function actionsNode(
  g: ActionGroup,
  t: Turn,
  own: { agent: string; agentKind: string; lane: string },
  expanded: ReadonlySet<string>,
  base: (over: Partial<HistoryNode> & Pick<HistoryNode, "id" | "kind" | "at" | "title">) => HistoryNode,
  atOf: (mid: string | undefined) => number,
): HistoryNode {
  const s = actionStats(g.items, !t.finished);
  const first = g.items.find((c) => c.k === "tool") ?? g.items[0]!;
  const mid = chunkOrigin(first)?.mid ?? t.id;
  const id = `a:${g.id}`;
  const open = expanded.has(id);
  const items: ActionItem[] = g.items.flatMap((c) =>
    c.k === "tool" ? [{ name: toolName(c), summary: c.call?.summary ?? "", failed: c.result?.is_error === true }] : [],
  );
  const shown = Math.min(items.length, ITEMS_MAX) + (items.length > ITEMS_MAX ? 1 : 0);
  return base({
    id,
    kind: "actions",
    at: atOf(mid),
    mid,
    title: `${s.actions} action${s.actions === 1 ? "" : "s"}`,
    meta: summaryLine(s),
    actions: { count: s.actions, failed: s.failed, running: s.pending > 0, items },
    open,
    h: open ? NODE_H + 6 + shown * ITEM_H : NODE_H,
    ...own,
  });
}

// The summary, short enough for a card.
function summaryLine(s: ReturnType<typeof actionStats>): string {
  const parts: string[] = [];
  if (s.read.length + s.byKind.read > 0) parts.push(`${s.read.length || s.byKind.read} read`);
  if (s.byKind.command > 0) parts.push(`${s.byKind.command} run`);
  if (s.edited.length + s.byKind.edit > 0) parts.push(`${s.edited.length || s.byKind.edit} edited`);
  const rest = s.byKind.search + s.byKind.web + s.byKind.other;
  if (rest > 0) parts.push(`${rest} other`);
  return parts.join(" · ");
}

// Builds the history and lays it out. expanded holds the ids of opened
// runs and clusters.
export function buildHistory(
  threads: readonly HistoryThread[],
  filter: HistoryFilter = ALL_WORK,
  expanded: ReadonlySet<string> = new Set(),
  clusterAt = CLUSTER_AT,
): HistoryGraph {
  const seq = { n: 0 };
  const perThread = threads.map((t) => ({ t, ...threadEvents(t, expanded, seq) }));
  const allAgents = new Map<string, string>();
  for (const p of perThread) for (const [a, k] of p.agents) if (!allAgents.has(a)) allAgents.set(a, k);
  let span = { from: 0, to: 0 };
  for (const p of perThread) {
    for (const e of p.events) {
      if (e.n.at <= 0) continue;
      span = span.from === 0 ? { from: e.n.at, to: e.n.at } : { from: Math.min(span.from, e.n.at), to: Math.max(span.to, e.n.at) };
    }
  }

  // The filters.
  const kinds = new Set(filter.kinds);
  const keep = (e: Event, thread: string) => {
    const f = FILTER_OF[e.n.kind];
    if (f !== null && !kinds.has(f)) return false;
    if (filter.threads !== null && !filter.threads.includes(thread)) return false;
    if (filter.since !== null && e.n.at < filter.since) return false;
    if (filter.until !== null && e.n.at > filter.until) return false;
    if (filter.agents !== null && e.n.agent !== null && e.n.kind !== "prompt" && !filter.agents.includes(e.n.agent)) return false;
    return true;
  };
  const kept = perThread.map((p) => ({ ...p, events: p.events.filter((e) => keep(e, p.t.id)) }));

  // The edges among one thread's events.
  const edges = new Map<string, HistoryEdge>();
  const edge = (kind: HistoryEdgeKind, from: string, to: string, agent: string | null, agentKind: string) => {
    if (from === to) return;
    const id = `${kind}:${from}>${to}`;
    if (!edges.has(id)) edges.set(id, { id, from, to, kind, agent, agentKind });
  };
  for (const p of kept) {
    const ev = p.events.filter((e) => e.n.kind !== "task");
    // The first node of an agent's next turn after event i.
    const nextOf = (i: number, agent: string): HistoryNode | undefined => ev.slice(i + 1).find((e) => e.n.agent === agent && e.n.kind !== "prompt")?.n;
    ev.forEach((e, i) => {
      if (e.n.kind === "prompt") {
        const replying = new Set(ev.filter((x) => x.round === e.round && x.n.agent !== null).map((x) => x.n.agent!));
        const asked = e.names.length > 0 ? e.names.filter((n) => replying.has(n)) : [...replying];
        for (const a of asked) {
          const to = nextOf(i, a);
          if (to !== undefined) edge("asks", e.n.id, to.id, a, to.agentKind);
        }
      } else if (e.n.kind === "reply") {
        for (const a of e.names) {
          const to = nextOf(i, a);
          if (to !== undefined) edge(e.review ? "reviews" : "handoff", e.n.id, to.id, e.n.agent, e.n.agentKind);
        }
      }
    });
    // A task: from its owner's latest work before it (else the first), and
    // from its reviewer's.
    for (const e of p.events) {
      if (e.n.kind !== "task") continue;
      e.names.forEach((a, i) => {
        const work = ev.filter((x) => x.n.agent === a && x.n.kind !== "prompt");
        const from = [...work].reverse().find((x) => x.n.at <= e.n.at) ?? work[0];
        if (from !== undefined) edge(i === 0 && a === e.n.agent ? "owns" : "reviews", from.n.id, e.n.id, a, from.n.agentKind);
      });
    }
  }

  // Clusters: past clusterAt nodes, an agent's work between two prompts
  // folds into one node, unless it is opened.
  const count = kept.reduce((s, p) => s + p.events.length, 0);
  const clustered = count > clusterAt;
  const into = new Map<string, string>();
  if (clustered) {
    for (const p of kept) {
      const groups = new Map<string, Event[]>();
      for (const e of p.events) {
        if (e.n.agent === null || e.n.kind === "prompt" || e.n.kind === "task") continue;
        const key = `c:${p.t.id}:${e.n.agent}:${e.round}`;
        groups.set(key, [...(groups.get(key) ?? []), e]);
      }
      const out: Event[] = p.events.filter((e) => !groups.has(`c:${p.t.id}:${e.n.agent}:${e.round}`) || e.n.kind === "prompt" || e.n.kind === "task");
      for (const [key, members] of groups) {
        if (members.length < 2 || expanded.has(key)) {
          out.push(...members);
          continue;
        }
        const first = members[0]!;
        const runs = members.filter((m) => m.n.kind === "actions");
        const calls = runs.reduce((s, m) => s + (m.n.actions?.count ?? 0), 0);
        const failed = runs.reduce((s, m) => s + (m.n.actions?.failed ?? 0), 0);
        const replies = members.length - runs.length;
        for (const m of members) into.set(m.n.id, key);
        out.push({
          ...first,
          n: {
            ...first.n,
            id: key,
            kind: "cluster",
            h: NODE_H,
            title: `${members.length} steps`,
            meta: [replies > 0 ? `${replies} repl${replies === 1 ? "y" : "ies"}` : "", calls > 0 ? `${calls} actions` : "", failed > 0 ? `${failed} failed` : ""]
              .filter((x) => x !== "")
              .join(" · "),
            members: members.length,
            open: false,
          },
          files: {
            edited: [...new Set(members.flatMap((m) => m.files.edited))],
            read: [...new Set(members.flatMap((m) => m.files.read))],
          },
        });
      }
      out.sort((a, b) => a.seq - b.seq);
      p.events = out;
    }
    for (const e of [...edges.values()]) {
      const from = into.get(e.from) ?? e.from;
      const to = into.get(e.to) ?? e.to;
      if (from === e.from && to === e.to) continue;
      edges.delete(e.id);
      edge(e.kind, from, to, e.agent, e.agentKind);
    }
  }

  // The files a kept run touched, as nodes, with their edges.
  const nodes = new Map<string, HistoryNode>();
  const events = kept.flatMap((p) => p.events).sort((a, b) => a.n.at - b.n.at || a.seq - b.seq);
  for (const e of events) nodes.set(e.n.id, e.n);
  const files = new Map<string, HistoryNode>();
  if (kinds.has("files")) {
    for (const e of events) {
      if (e.n.kind === "prompt" || e.n.kind === "reply") continue;
      const touch = (path: string, kind: HistoryEdgeKind) => {
        const id = `f:${path}`;
        if (!files.has(id)) files.set(id, fileNode(path, e.n.at));
        edge(kind, e.n.id, id, e.n.agent, e.n.agentKind);
      };
      for (const f of e.files.edited) touch(f, e.n.kind === "task" ? "owns" : "touches");
      for (const f of e.files.read) if (!e.files.edited.includes(f)) touch(f, "reads");
    }
  }
  // An edge between two nodes that both stayed.
  const all = new Map([...nodes, ...files]);
  for (const [id, e] of edges) if (!all.has(e.from) || !all.has(e.to)) edges.delete(id);

  // Columns: each thread from its own start, in time order: a node after
  // the last in its lane, after what leads to it, and never before the
  // node before it in its thread. The bands stack in the order the threads
  // began, so time reads down the threads and along each.
  const col = new Map<string, number>();
  // A prompt and the reply it asks for share a column (the edge runs down
  // from you to the agent); a handoff moves on a column.
  const preds = new Map<string, { from: string; step: number }[]>();
  for (const e of edges.values()) {
    if (e.kind === "touches" || e.kind === "reads" || e.to.startsWith("f:")) continue;
    preds.set(e.to, [...(preds.get(e.to) ?? []), { from: e.from, step: e.kind === "asks" ? 0 : 1 }]);
  }
  const lastIn = new Map<string, number>();
  const prev = new Map<string, number>();
  for (const e of events) {
    const n = e.n;
    let c = Math.max(prev.get(n.thread) ?? 0, (lastIn.get(n.lane) ?? -1) + 1);
    for (const p of preds.get(n.id) ?? []) {
      const pc = col.get(p.from);
      if (pc !== undefined) c = Math.max(c, pc + p.step);
    }
    col.set(n.id, c);
    lastIn.set(n.lane, c);
    prev.set(n.thread, c);
    n.col = c;
    n.x = colX(c);
  }

  // Each file at the column of its first touch.
  const firstCol = new Map<string, number>();
  for (const e of edges.values()) {
    if (!files.has(e.to)) continue;
    const c = col.get(e.from) ?? 0;
    firstCol.set(e.to, Math.min(firstCol.get(e.to) ?? c, c));
  }
  const timelines = kinds.has("files") ? fileTimelines(kept, events, files, firstCol, filter, edge) : new Map<string, HistoryNode[]>();

  // The bands: a thread's lanes, you first, then its agents as they came,
  // then its tasks.
  const lanes: HistoryLane[] = [];
  const bands: HistoryBand[] = [];
  let y = 0;
  const order = kept
    .filter((p) => p.events.length > 0)
    .map((p) => ({ p, first: Math.min(...p.events.map((e) => (e.n.at > 0 ? e.n.at : Number.MAX_SAFE_INTEGER))) }))
    .sort((a, b) => a.first - b.first || threads.indexOf(a.p.t) - threads.indexOf(b.p.t));
  for (const { p, first } of order) {
    const top = y;
    y += BAND_HEAD;
    const ids: string[] = [];
    for (const e of [...p.events].sort((a, b) => a.seq - b.seq)) if (!ids.includes(e.n.lane)) ids.push(e.n.lane);
    const rank = (id: string) => (id.endsWith("/you") ? 0 : id.endsWith("/tasks") ? 2 : 1);
    ids.sort((a, b) => rank(a) - rank(b));
    for (const id of ids) {
      const ns = p.events.filter((e) => e.n.lane === id).map((e) => e.n);
      const h = Math.max(...ns.map((n) => n.h));
      const agent = ns[0]?.kind === "task" ? null : (ns[0]?.agent ?? null);
      for (const n of ns) n.y = y;
      lanes.push({
        id,
        thread: p.t.id,
        label: id.endsWith("/you") ? "you" : id.endsWith("/tasks") ? "tasks" : (agent ?? ""),
        agent,
        agentKind: agent === null ? "" : (p.agents.get(agent) ?? ""),
        y,
        h,
      });
      y += h + LANE_GAP;
    }
    bands.push({ thread: p.t.id, title: p.t.title, at: first === Number.MAX_SAFE_INTEGER ? 0 : first, y: top, h: y - top - LANE_GAP });
    y += BAND_GAP;
  }

  // The files' band: first a row for each file with a timeline, its edits
  // and asks along it; then the rest, each at the column of its first
  // touch, in as few rows as fit without two overlapping.
  if (files.size > 0) {
    const top = y;
    y += BAND_HEAD;
    const byCol = (a: HistoryNode, b: HistoryNode) => (firstCol.get(a.id) ?? 0) - (firstCol.get(b.id) ?? 0) || (a.path! < b.path! ? -1 : 1);
    for (const f of [...files.values()].filter((f) => timelines.has(f.id)).sort(byCol)) {
      f.col = firstCol.get(f.id) ?? 0;
      f.x = colX(f.col);
      const row = [f, ...timelines.get(f.id)!];
      // Along the row in time; one that would overlap the one before moves on.
      let end = f.x + f.w;
      for (const n of row.slice(1)) {
        n.x = Math.max(colX(n.col), end + STEP_GAP);
        end = n.x + n.w;
      }
      const h = Math.max(...row.map((n) => n.h));
      for (const n of row) n.y = y;
      lanes.push({ id: `files/${f.path}`, thread: "", label: f.title, agent: null, agentKind: "", y, h });
      y += h + LANE_GAP;
    }
    const list = [...files.values()].filter((f) => !timelines.has(f.id)).sort(byCol);
    const rows: number[] = [];
    for (const f of list) {
      f.col = firstCol.get(f.id) ?? 0;
      f.x = colX(f.col);
      let r = rows.findIndex((end) => end + COL_GAP <= f.x);
      if (r < 0) {
        r = rows.length;
        rows.push(0);
      }
      rows[r] = f.x + f.w;
      f.y = y + r * (FILE_H + LANE_GAP);
    }
    const h = rows.length * (FILE_H + LANE_GAP) - LANE_GAP;
    if (rows.length > 0) lanes.push({ id: "files", thread: "", label: "files", agent: null, agentKind: "", y, h });
    else y -= LANE_GAP;
    bands.push({ thread: "", title: "Files", at: 0, y: top, h: y + Math.max(h, 0) - top });
    y += Math.max(h, 0);
  }

  const placed = [...events.map((e) => e.n), ...files.values(), ...[...timelines.values()].flat()];
  const b = boundsOf(placed);
  return {
    nodes: placed,
    edges: [...edges.values()],
    lanes,
    bands,
    bounds: placed.length === 0 ? b : { x: b.x - LABEL_W, y: b.y - BAND_HEAD, w: b.w + LABEL_W, h: b.h + BAND_HEAD },
    agents: [...allAgents].map(([name, kind]) => ({ name, kind })).sort((a, c) => a.name.localeCompare(c.name)),
    threads: threads.map((t) => ({ id: t.id, title: t.title })),
    span,
    clustered,
  };
}

// A file's timeline in the files' band: for a file two agents edited, or one
// an agent asked another about, each edit after its first touch (an edit
// node at its run's column, from the run) and each ask over it (at the
// column its thread was at then, from the asker's work before it, and on
// to the asker's next edit once the owner let it through). By file node id,
// each row in time order; a file only asked about joins the files.
function fileTimelines(
  kept: readonly { t: HistoryThread; events: Event[]; agents: Map<string, string>; asks: FileAsk[] }[],
  events: readonly Event[],
  files: Map<string, HistoryNode>,
  firstCol: Map<string, number>,
  filter: HistoryFilter,
  edge: (kind: HistoryEdgeKind, from: string, to: string, agent: string | null, agentKind: string) => void,
): Map<string, HistoryNode[]> {
  const out = new Map<string, HistoryNode[]>();
  const fileOf = (path: string) => [...files.values()].find((f) => samePath(f.path!, path));
  const keepAsk = (a: FileAsk, thread: string) =>
    (filter.threads === null || filter.threads.includes(thread)) &&
    (filter.since === null || a.at >= filter.since) &&
    (filter.until === null || a.at <= filter.until) &&
    (filter.agents === null || filter.agents.includes(a.from) || filter.agents.includes(a.to));
  const asks = kept.flatMap((p) => p.asks.filter((a) => keepAsk(a, p.t.id)).map((a) => ({ a, p })));
  // Where a thread was at a moment: the column of its last node by then.
  const colAt = (p: (typeof kept)[number], at: number) => p.events.reduce((c, e) => (e.n.at <= at ? Math.max(c, e.n.col) : c), 0);
  for (const { a, p } of asks) {
    if (fileOf(a.path) !== undefined) continue;
    const c = colAt(p, a.at);
    const id = `f:${a.path}`;
    files.set(id, { ...fileNode(a.path, a.at), id, col: c });
    firstCol.set(id, c);
  }

  for (const f of files.values()) {
    const edits = events.filter((e) => e.n.kind === "actions" && e.files.edited.some((p) => samePath(p, f.path!)));
    const qs = asks.filter((x) => samePath(x.a.path, f.path!));
    if (new Set(edits.map((e) => e.n.agent)).size < 2 && qs.length === 0) continue;
    const row: HistoryNode[] = [];
    edits.forEach((e, i) => {
      // The first touch is the file's own node.
      if (i === 0 && e.n.col === firstCol.get(f.id)) return;
      const path = e.files.edited.find((p) => samePath(p, f.path!))!;
      const diff = e.files.diffs?.get(path) ?? "";
      const stat = diffStat(diff);
      const n: HistoryNode = {
        id: `e:${e.n.id}:${f.path}`,
        kind: "edit",
        thread: e.n.thread,
        lane: `files/${f.path}`,
        col: e.n.col,
        agent: e.n.agent,
        agentKind: e.n.agentKind,
        at: e.n.at,
        mid: e.n.mid,
        title: "edited",
        meta: diff === "" ? "" : `+${stat.added} −${stat.removed}`,
        diff,
        path: f.path!,
        x: 0,
        y: 0,
        w: EDIT_W,
        h: FILE_H,
      };
      row.push(n);
      edge("touches", e.n.id, n.id, e.n.agent, e.n.agentKind);
    });
    for (const { a, p } of qs) {
      const kind = p.agents.get(a.from) ?? "";
      const n: HistoryNode = {
        id: `q:${a.mid}`,
        kind: "ask",
        thread: p.t.id,
        lane: `files/${f.path}`,
        col: colAt(p, a.at),
        agent: a.from,
        agentKind: kind,
        at: a.at,
        mid: a.mid,
        title: `asked ${a.to}`,
        meta: a.state === "pending" ? "waiting for an answer" : `${a.by ?? "deck"}${a.state === "held" ? " (wait)" : ""}: ${a.answer}`,
        ask: a,
        path: f.path!,
        x: 0,
        y: 0,
        w: NODE_W,
        h: NODE_H,
      };
      row.push(n);
      const before = [...p.events].reverse().find((e) => e.n.agent === a.from && e.n.at <= a.at && e.n.kind !== "prompt");
      if (before !== undefined) edge("requests", before.n.id, n.id, a.from, kind);
    }
    row.sort((x, y) => x.col - y.col || x.at - y.at);
    // An ask let through leads to the asker's next edit of the file.
    for (const q of row) {
      if (q.kind !== "ask" || q.ask === undefined || q.ask.state === "pending" || q.ask.state === "held") continue;
      const next = row.find((n) => n.kind === "edit" && n.agent === q.agent && n.at >= q.at);
      if (next !== undefined) edge("answers", q.id, next.id, q.agent, q.agentKind);
    }
    out.set(f.id, row);
  }
  return out;
}

function fileNode(path: string, at: number): HistoryNode {
  return {
    id: `f:${path}`,
    kind: "file",
    thread: "",
    lane: "files",
    col: 0,
    agent: null,
    agentKind: "",
    at,
    mid: null,
    title: path.slice(path.lastIndexOf("/") + 1),
    meta: path.slice(0, path.lastIndexOf("/") + 1),
    path,
    x: 0,
    y: 0,
    w: NODE_W,
    h: FILE_H,
  };
}

// The edge path between two history nodes: across, from a's right to b's
// left, when b lies ahead; else down, from a's bottom to b's top (a file
// under its run, or work in the same column of another lane).
export function historyPath(a: Bounds, b: Bounds): string {
  if (b.x >= a.x + a.w) {
    const [x1, y1, x2, y2] = [a.x + a.w, a.y + a.h / 2, b.x, b.y + b.h / 2];
    const reach = Math.max(24, (x2 - x1) / 2);
    return `M${x1} ${y1} C${x1 + reach} ${y1} ${x2 - reach} ${y2} ${x2} ${y2}`;
  }
  const down = b.y >= a.y;
  const [x1, y1] = [a.x + a.w / 2, down ? a.y + a.h : a.y];
  const [x2, y2] = [b.x + b.w / 2, down ? b.y : b.y + b.h];
  const reach = Math.max(16, Math.abs(y2 - y1) / 2) * (down ? 1 : -1);
  return `M${x1} ${y1} C${x1} ${y1 + reach} ${x2} ${y2 - reach} ${x2} ${y2}`;
}

// The nodes and edges worth drawing in sight (with a margin around it):
// a big history draws only these.
export function inView(g: HistoryGraph, seen: Bounds | null, margin = 0.5): { nodes: HistoryNode[]; edges: HistoryEdge[] } {
  if (seen === null) return { nodes: g.nodes, edges: g.edges };
  const mx = seen.w * margin;
  const my = seen.h * margin;
  const s = { x: seen.x - mx, y: seen.y - my, w: seen.w + 2 * mx, h: seen.h + 2 * my };
  const hit = (b: Bounds) => b.x + b.w >= s.x && b.x <= s.x + s.w && b.y + b.h >= s.y && b.y <= s.y + s.h;
  const nodes = g.nodes.filter(hit);
  const byId = new Map(g.nodes.map((n) => [n.id, n]));
  const edges = g.edges.filter((e) => {
    const a = byId.get(e.from);
    const b = byId.get(e.to);
    if (a === undefined || b === undefined) return false;
    return hit(boundsOf([a, b]));
  });
  return { nodes, edges };
}
