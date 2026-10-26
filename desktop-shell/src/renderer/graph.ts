import type { AgentLine, ChangedFile } from "@web/protocol";
import { type Board, boardRows, type BoardTask, type Crew, type RunningTool } from "./crew";
import { agentLane, contextPct, type LaneStatus, leasePaths, YOU } from "./lanes";
import type { Lease } from "./roomMeta";

// The Work Graph's model (docs/design-glass.md, section 7): one thread's
// work as nodes and edges, laid out as a layered DAG from left to right.
// You, then the agents by depth, then the tasks by their dependencies, then
// the files. A tall, narrow host (the side panel) may lay the same layers
// out from top to bottom instead. Pure and deterministic, so the tests drive
// it directly, and the same input always draws the same picture.

export type NodeKind = "you" | "agent" | "task" | "file" | "fold";

interface Placed {
  // "you", "agent:<name>" (the agent's lane, lanes.ts), "task:<id>",
  // "file:<path>", or "fold" for the changed files no one is linked to.
  id: string;
  layer: number;
  // The top left corner and the size, in graph units (1 = 1px at 100%).
  x: number;
  y: number;
  w: number;
  h: number;
}

export interface YouNode extends Placed {
  kind: "you";
}

export interface AgentGraphNode extends Placed {
  kind: "agent";
  // The roster id, or the tree's name for a subagent.
  name: string;
  agentKind: string;
  status: LaneStatus;
  model: string;
  effort: string;
  contextPct: number | null;
  contextTokens: number;
  costUsd: number;
  // 0 for a member of the thread, 1 for its subagent, and so on.
  depth: number;
  // The node id of the agent that started it, or null.
  parent: string | null;
  branch: string;
  // When it started its current work (ms), or 0.
  since: number;
  // What it runs right now, the first one shown on its card.
  tools: RunningTool[];
  leases: string[];
}

export interface TaskGraphNode extends Placed {
  kind: "task";
  task: BoardTask;
  // How many dependencies deep it sits: 0 needs nothing first.
  level: number;
  // Its owner's agent kind, for the owner's glyph and hue; "" when the room
  // does not know the owner.
  ownerKind: string;
}

export interface FileGraphNode extends Placed {
  kind: "file";
  path: string;
  added: number;
  removed: number;
  // In the thread's Changes (else only named by a task or a lease).
  changed: boolean;
  // A lease's pattern that matched no file ("pkg/remote/**").
  glob: boolean;
  // The agents that hold a lease on it.
  holders: string[];
}

// Changed files no agent or task is linked to, folded into one node once
// there are more than FOLD_AT of them.
export interface FoldGraphNode extends Placed {
  kind: "fold";
  paths: string[];
  added: number;
  removed: number;
}

export type GraphNode = YouNode | AgentGraphNode | TaskGraphNode | FileGraphNode | FoldGraphNode;

// you → a member (leads); an agent → its subagent (spawned); an agent → its
// task (owns); a task → the task that waits on it (after); a task → its
// files (touches); an agent → a file it leases (holds, drawn dashed) or
// edits (live while it does so now).
export type EdgeKind = "leads" | "spawned" | "owns" | "after" | "touches" | "holds" | "edits";

export interface GraphEdge {
  id: string;
  from: string;
  to: string;
  kind: EdgeKind;
  // Data moves on it right now: an agent editing the file.
  live: boolean;
  // The agent node whose hue the edge takes, or null for a neutral one.
  tint: string | null;
}

export interface Bounds {
  x: number;
  y: number;
  w: number;
  h: number;
}

// The way the layers run: across, from left to right, or down.
export type Direction = "across" | "down";

export interface Graph {
  dir: Direction;
  nodes: GraphNode[];
  edges: GraphEdge[];
  bounds: Bounds;
  // The layers in order: where each starts along the direction, its size
  // and what it holds, for the canvas's column labels.
  layers: { layer: number; at: number; size: number; label: string }[];
}

export interface GraphInput {
  roster: readonly AgentLine[];
  // The agents tree and the board; null for a paired Deck, or before they load.
  crew: Crew | null;
  board: Board | null;
  files: readonly ChangedFile[] | null;
  // The room's live leases (lease_changed), when the Deck reports them.
  leases?: readonly Lease[];
}

// Node sizes, in graph units. An agent's card grows a line for the tool it runs.
export const SIZE = {
  you: { w: 60, h: 30 },
  agent: { w: 196, h: 50 },
  agentTool: { w: 196, h: 74 },
  task: { w: 196, h: 48 },
  file: { w: 188, h: 30 },
} as const;
// Down, a layer is a row and its width is what limits the fit: narrower cards.
export const SIZE_DOWN: { [K in keyof typeof SIZE]: { w: number; h: number } } = {
  you: { w: 60, h: 30 },
  agent: { w: 164, h: 50 },
  agentTool: { w: 164, h: 74 },
  task: { w: 164, h: 48 },
  file: { w: 152, h: 30 },
};
// Across, the gap between columns and between the cards of one; down,
// between rows and between the cards of one.
export const COLUMN_GAP = 56;
export const ROW_GAP = 14;
export const LAYER_GAP = 44;
export const SIBLING_GAP = 12;
export const FOLD_AT = 4;

export const taskId = (id: string): string => `task:${id}`;
export const fileId = (path: string): string => `file:${path}`;
export const FOLD = "fold";

const NODE_STATUS: Record<string, LaneStatus> = {
  working: "working",
  tool: "working",
  approval: "waiting",
  error: "error",
  exited: "exited",
  idle: "idle",
  ready: "idle",
};

// Whether a lease's path is a pattern rather than one file.
export const isGlob = (p: string): boolean => /[*?[]/.test(p);

// Whether path falls under a lease's path: "**" spans folders, "*" and "?"
// stay in one, and a plain folder holds everything under it.
export function globMatch(glob: string, path: string): boolean {
  if (!isGlob(glob)) return path === glob || path.startsWith(glob.endsWith("/") ? glob : `${glob}/`);
  let re = "";
  for (let i = 0; i < glob.length; i++) {
    const ch = glob[i]!;
    if (ch === "*" && glob[i + 1] === "*") {
      const slash = glob[i + 2] === "/";
      re += slash ? "(?:.*/)?" : ".*";
      i += slash ? 2 : 1;
    } else if (ch === "*") re += "[^/]*";
    else if (ch === "?") re += "[^/]";
    else re += ch.replace(/[.+^${}()|[\]\\]/g, "\\$&");
  }
  return new RegExp(`^${re}$`).test(path);
}

const EDIT_TOOL = /^(edit|multi_?edit|write|apply_?patch|patch|str_replace\w*|create|notebook_?edit)$/i;

// The file a running tool edits now, matched to a known path when it can
// be: Claude's Edit names the whole path, a patch may name it relative.
// null for a tool that edits nothing (Bash, Read, a subagent).
export function editTarget(t: RunningTool, known: readonly string[]): string | null {
  if (t.subagent || t.command || !EDIT_TOOL.test(t.name.trim())) return null;
  for (const text of [t.summary, t.detail]) {
    const s = text.trim();
    if (s === "") continue;
    const hit = known.find((k) => k === s || s.endsWith(`/${k}`) || k.endsWith(`/${s}`));
    if (hit !== undefined) return hit;
    if (/^[^\s]+\.[^\s/]+$/.test(s) || /^[^\s]+\/[^\s]+$/.test(s)) return s.replace(/^\.\//, "");
  }
  return null;
}

// Builds the graph and lays it out. Nodes keep a stable order: you, the
// roster, the subagents, the board's tasks, the files by path.
export function buildGraph(input: GraphInput, dir: Direction = "across"): Graph {
  const nodes = new Map<string, GraphNode>();
  const edges = new Map<string, GraphEdge>();
  const at = { layer: 0, x: 0, y: 0 };
  const size = dir === "down" ? SIZE_DOWN : SIZE;
  const edge = (kind: EdgeKind, from: string, to: string, tint: string | null, live = false) => {
    if (from === to || !nodes.has(from) || !nodes.has(to)) return;
    const id = `${kind}:${from}>${to}`;
    if (!edges.has(id)) edges.set(id, { id, from, to, kind, live, tint });
  };

  nodes.set(YOU, { kind: "you", id: YOU, ...at, ...size.you });

  // The agents: the roster merged with the tree, as the lane strip does.
  const crewNodes = input.crew?.agents ?? [];
  const running = input.crew?.running ?? [];
  const byName = new Map<string, AgentGraphNode>();
  const addAgent = (name: string, line: AgentLine | undefined, n: (typeof crewNodes)[number] | undefined) => {
    const id = agentLane(name);
    if (nodes.has(id)) return;
    const tools = running.filter((t) => t.agent === name || (line !== undefined && t.agent === line.name));
    const status: LaneStatus =
      n !== undefined && n.status in NODE_STATUS ? NODE_STATUS[n.status]! : line?.busy === true || tools.length > 0 ? "working" : "idle";
    const live = input.leases?.filter((l) => l.agent === name).flatMap((l) => l.paths) ?? [];
    const a: AgentGraphNode = {
      kind: "agent",
      id,
      ...at,
      ...(tools.length > 0 ? size.agentTool : size.agent),
      name,
      agentKind: line?.kind || n?.kind || name.replace(/[-_]?\d+$/, ""),
      status,
      model: n?.model || line?.model || "",
      effort: n?.effort || line?.effort || "",
      contextPct: contextPct(n),
      contextTokens: n?.contextTokens ?? 0,
      costUsd: n?.costUsd ?? 0,
      depth: 0,
      parent: null,
      branch: n?.branch ?? "",
      since: n?.since ?? 0,
      tools,
      leases: live.length > 0 ? live : leasePaths(n?.leases ?? ""),
    };
    nodes.set(id, a);
    byName.set(name, a);
    if (line !== undefined && line.name !== name) byName.set(line.name, a);
    if (n !== undefined) byName.set(n.name, a);
  };
  const used = new Set<(typeof crewNodes)[number]>();
  for (const line of input.roster) {
    const n = crewNodes.find((x) => x.name === line.id || x.name === line.name);
    if (n !== undefined) used.add(n);
    addAgent(line.id, line, n);
  }
  for (const n of crewNodes) if (!used.has(n)) addAgent(n.name, undefined, n);

  // Parents and depth from the tree's parent names; a name the room does
  // not know, or a loop, makes the agent a member.
  const crewOf = new Map(crewNodes.map((n) => [n.name, n]));
  for (const a of byName.values()) {
    const p = crewOf.get(a.name)?.parent ?? "";
    const parent = p === "" ? undefined : byName.get(p);
    a.parent = parent === undefined || parent === a ? null : parent.id;
  }
  const agents = [...new Set(byName.values())];
  // An agent whose parents lead back to itself stands at the top.
  const up = (a: AgentGraphNode) => (a.parent === null ? undefined : (nodes.get(a.parent) as AgentGraphNode));
  const looped = agents.filter((a) => {
    const seen = new Set<AgentGraphNode>();
    for (let cur = up(a); cur !== undefined && !seen.has(cur); cur = up(cur)) {
      if (cur === a) return true;
      seen.add(cur);
    }
    return false;
  });
  for (const a of looped) a.parent = null;
  for (const a of agents) for (let cur = up(a); cur !== undefined; cur = up(cur)) a.depth++;
  for (const a of agents) {
    if (a.parent === null) edge("leads", YOU, a.id, null);
    else edge("spawned", a.parent, a.id, a.parent);
  }

  // The tasks, each a race's entry after its race.
  const tasks = input.board?.tasks ?? [];
  const raceOf = new Map<string, string>();
  if (input.board !== null) {
    for (const r of boardRows(input.board)) for (const e of r.entries) raceOf.set(e.id, r.task.id);
  }
  for (const t of tasks) {
    if (nodes.has(taskId(t.id))) continue;
    const ownerKind = byName.get(t.owner)?.agentKind ?? "";
    nodes.set(taskId(t.id), { kind: "task", id: taskId(t.id), ...at, ...size.task, task: t, level: 0, ownerKind });
  }
  const depsOf = (t: BoardTask): string[] => {
    const race = raceOf.get(t.id);
    return [...(race === undefined ? [] : [race]), ...t.deps].filter((d, i, all) => d !== t.id && all.indexOf(d) === i);
  };
  levelTasks(tasks, depsOf, (id) => nodes.get(taskId(id)) as TaskGraphNode | undefined);

  // The files: the changed ones, those the tasks name, and those leased.
  const changed = input.files ?? [];
  const addFile = (path: string, f?: ChangedFile, glob = false) => {
    const id = fileId(path);
    const cur = nodes.get(id) as FileGraphNode | undefined;
    if (cur !== undefined) return cur;
    const node: FileGraphNode = {
      kind: "file",
      id,
      ...at,
      ...size.file,
      path,
      added: f?.added ?? 0,
      removed: f?.removed ?? 0,
      changed: f !== undefined,
      glob,
      holders: [],
    };
    nodes.set(id, node);
    return node;
  };
  for (const f of changed) addFile(f.path, f);
  for (const t of tasks) for (const p of t.files) addFile(p);
  const known = () => [...nodes.values()].flatMap((n) => (n.kind === "file" && !n.glob ? [n.path] : []));
  for (const a of agents) {
    for (const lease of a.leases) {
      const hits = isGlob(lease) || !known().includes(lease) ? known().filter((p) => globMatch(lease, p)) : [lease];
      const held = hits.length > 0 ? hits.map((p) => addFile(p)) : [addFile(lease, undefined, isGlob(lease))];
      for (const f of held) if (!f.holders.includes(a.name)) f.holders.push(a.name);
    }
  }

  // The edges among them, the live edits first so they win a tie.
  for (const a of agents) {
    for (const t of a.tools) {
      const path = editTarget(t, known());
      if (path === null) continue;
      edge("edits", a.id, addFile(path).id, a.id, true);
    }
  }
  for (const t of tasks) {
    const id = taskId(t.id);
    const owner = byName.get(t.owner);
    if (owner !== undefined) edge("owns", owner.id, id, owner.id);
    for (const d of depsOf(t)) edge("after", taskId(d), id, null);
    for (const p of t.files) edge("touches", id, fileId(p), null);
  }
  for (const n of nodes.values()) {
    if (n.kind !== "file") continue;
    for (const h of n.holders) {
      const a = byName.get(h)!;
      if (!edges.has(`edits:${a.id}>${n.id}`)) edge("holds", a.id, n.id, a.id);
    }
  }

  // A changed file nothing reaches: the work of the one agent there is,
  // else unattributed, and folded once there are many.
  const reached = new Set([...edges.values()].map((e) => e.to));
  const loose = [...nodes.values()].filter((n): n is FileGraphNode => n.kind === "file" && !reached.has(n.id));
  const only = agents.length === 1 ? agents[0]! : null;
  if (only !== null) for (const f of loose) edge("edits", only.id, f.id, only.id);
  else if (loose.length > FOLD_AT) {
    for (const f of loose) nodes.delete(f.id);
    nodes.set(FOLD, {
      kind: "fold",
      id: FOLD,
      ...at,
      ...size.file,
      paths: loose.map((f) => f.path).sort(),
      added: loose.reduce((s, f) => s + f.added, 0),
      removed: loose.reduce((s, f) => s + f.removed, 0),
    });
  }

  return layout([...nodes.values()], [...edges.values()], dir);
}

// Each task's level: one more than its deepest dependency. A dependency on
// a task the board does not hold counts for nothing; a loop stops at the
// task where it closes.
function levelTasks(tasks: readonly BoardTask[], depsOf: (t: BoardTask) => string[], nodeOf: (id: string) => TaskGraphNode | undefined) {
  const byId = new Map(tasks.map((t) => [t.id, t]));
  const memo = new Map<string, number>();
  const level = (t: BoardTask, path: Set<string>): number => {
    const got = memo.get(t.id);
    if (got !== undefined) return got;
    if (path.has(t.id)) return -1;
    path.add(t.id);
    let lv = 0;
    for (const d of depsOf(t)) {
      const dep = byId.get(d);
      if (dep !== undefined) lv = Math.max(lv, level(dep, path) + 1);
    }
    path.delete(t.id);
    memo.set(t.id, lv);
    return lv;
  };
  for (const t of tasks) {
    const n = nodeOf(t.id);
    if (n !== undefined) n.level = level(t, new Set());
  }
}

const sortFiles = (a: GraphNode, b: GraphNode): number => {
  const key = (n: GraphNode) => (n.kind === "fold" ? "\uffff" : n.kind === "file" ? n.path : "");
  return key(a) < key(b) ? -1 : key(a) > key(b) ? 1 : 0;
};

// The two axes of a direction: along the layers, and along one layer.
interface Axes {
  main: "x" | "y";
  cross: "x" | "y";
  mainSize: "w" | "h";
  crossSize: "w" | "h";
  layerGap: number;
  siblingGap: number;
}

const AXES: Record<Direction, Axes> = {
  across: { main: "x", cross: "y", mainSize: "w", crossSize: "h", layerGap: COLUMN_GAP, siblingGap: ROW_GAP },
  down: { main: "y", cross: "x", mainSize: "h", crossSize: "w", layerGap: LAYER_GAP, siblingGap: SIBLING_GAP },
};

// Assigns the layers, orders each by barycentre and places the nodes.
export function layout(nodes: GraphNode[], edges: GraphEdge[], dir: Direction = "across"): Graph {
  const ax = AXES[dir];
  const agents = nodes.filter((n): n is AgentGraphNode => n.kind === "agent");
  const tasks = nodes.filter((n): n is TaskGraphNode => n.kind === "task");
  const maxDepth = agents.reduce((m, a) => Math.max(m, a.depth), -1);
  const taskBase = maxDepth + 2;
  const maxLevel = tasks.reduce((m, t) => Math.max(m, t.level), -1);
  const fileLayer = tasks.length > 0 ? taskBase + maxLevel + 1 : taskBase;
  for (const n of nodes) {
    n.layer =
      n.kind === "you" ? 0 : n.kind === "agent" ? 1 + n.depth : n.kind === "task" ? taskBase + n.level : fileLayer;
  }
  // The layers in use, packed so none is empty.
  const used = [...new Set(nodes.map((n) => n.layer))].sort((a, b) => a - b);
  const remap = new Map(used.map((l, i) => [l, i]));
  for (const n of nodes) n.layer = remap.get(n.layer)!;
  const layers: GraphNode[][] = used.map(() => []);
  for (const n of nodes) layers[n.layer]!.push(n);
  for (const l of layers) if (l[0]?.kind === "file" || l[0]?.kind === "fold") l.sort(sortFiles);

  const ordered = orderLayers(layers, edges);
  const byId = new Map(nodes.map((n) => [n.id, n]));
  const spans = placeLayers(ordered, ax);
  placeCross(ordered, edges, byId, ax);

  const out = ordered.flat();
  return { dir, nodes: out, edges, bounds: boundsOf(out), layers: spans.map((sp, i) => ({ layer: i, ...sp, label: columnLabel(ordered[i]!) })) };
}

function columnLabel(layer: GraphNode[]): string {
  const n = layer[0]!;
  if (n.kind === "you") return "";
  if (n.kind === "agent") return n.depth === 0 ? "Agents" : n.depth === 1 ? "Subagents" : `Subagents · ${n.depth}`;
  if (n.kind === "task") return n.level === 0 ? "Tasks" : "";
  return "Files";
}

// The number of pairs of edges that cross between two neighbouring layers.
export function crossings(layers: GraphNode[][], edges: readonly GraphEdge[]): number {
  const index = new Map<string, { layer: number; i: number }>();
  layers.forEach((l, li) => l.forEach((n, i) => index.set(n.id, { layer: li, i })));
  let total = 0;
  for (let li = 0; li + 1 < layers.length; li++) {
    const span = edges
      .map((e) => [index.get(e.from), index.get(e.to)] as const)
      .filter(([a, b]) => a !== undefined && b !== undefined && a.layer === li && b.layer === li + 1)
      .map(([a, b]) => [a!.i, b!.i] as const);
    for (let i = 0; i < span.length; i++) {
      for (let j = i + 1; j < span.length; j++) {
        const [a1, b1] = span[i]!;
        const [a2, b2] = span[j]!;
        if ((a1 - a2) * (b1 - b2) < 0) total++;
      }
    }
  }
  return total;
}

const SWEEPS = 8;

// Barycentre ordering: sweep right then left, sorting each layer by the
// mean place of its neighbours on the side the sweep came from, and keep
// the order with the fewest crossings. Places are fractions of a layer's
// height, so a long edge counts as well as a short one. Ties keep the
// input order, so the result is deterministic.
export function orderLayers(layers: GraphNode[][], edges: readonly GraphEdge[]): GraphNode[][] {
  const layerOf = new Map<string, number>();
  layers.forEach((l, li) => l.forEach((n) => layerOf.set(n.id, li)));
  const near = new Map<string, { before: string[]; after: string[] }>();
  for (const n of layers.flat()) near.set(n.id, { before: [], after: [] });
  for (const e of edges) {
    const a = layerOf.get(e.from);
    const b = layerOf.get(e.to);
    if (a === undefined || b === undefined || a === b) continue;
    const [lo, hi] = a < b ? [e.from, e.to] : [e.to, e.from];
    near.get(hi)!.before.push(lo);
    near.get(lo)!.after.push(hi);
  }
  let cur = layers.map((l) => [...l]);
  let best = cur.map((l) => [...l]);
  let bestX = crossings(best, edges);
  const place = new Map<string, number>();
  const mark = (ls: GraphNode[][]) => ls.forEach((l) => l.forEach((n, i) => place.set(n.id, (i + 0.5) / l.length)));
  for (let s = 0; s < SWEEPS && bestX > 0; s++) {
    const down = s % 2 === 0;
    mark(cur);
    const order = down ? cur.map((_, i) => i) : cur.map((_, i) => cur.length - 1 - i);
    for (const li of order) {
      const l = cur[li]!;
      const keyed = l.map((n, i) => {
        const from = down ? near.get(n.id)!.before : near.get(n.id)!.after;
        const bary = from.length === 0 ? place.get(n.id)! : from.reduce((sum, id) => sum + place.get(id)!, 0) / from.length;
        return { n, bary, i };
      });
      keyed.sort((a, b) => a.bary - b.bary || a.i - b.i);
      cur[li] = keyed.map((k) => k.n);
      cur[li]!.forEach((n, i) => place.set(n.id, (i + 0.5) / l.length));
    }
    const x = crossings(cur, edges);
    if (x < bestX) {
      bestX = x;
      best = cur.map((l) => [...l]);
    }
  }
  return best;
}

// Places the layers one after the other along the direction, each card in
// the middle of its layer's extent.
function placeLayers(layers: GraphNode[][], ax: Axes): { at: number; size: number }[] {
  let at = 0;
  return layers.map((l) => {
    const size = Math.max(...l.map((n) => n[ax.mainSize]));
    for (const n of l) n[ax.main] = at + Math.round((size - n[ax.mainSize]) / 2);
    const span = { at, size };
    at += size + ax.layerGap;
    return span;
  });
}

// Lines each layer up, then pulls each card towards the middle of its
// neighbours, a sweep each way, without changing the order or letting two
// cards come closer than the gap.
function placeCross(layers: GraphNode[][], edges: readonly GraphEdge[], byId: Map<string, GraphNode>, ax: Axes): void {
  const { cross, crossSize, siblingGap: gap } = ax;
  for (const l of layers) {
    const total = l.reduce((s, n) => s + n[crossSize], 0) + gap * (l.length - 1);
    let c = -total / 2;
    for (const n of l) {
      n[cross] = c;
      c += n[crossSize] + gap;
    }
  }
  const mid = (n: GraphNode) => n[cross] + n[crossSize] / 2;
  const near = (n: GraphNode, before: boolean) =>
    edges.flatMap((e) => {
      const other = e.from === n.id ? byId.get(e.to) : e.to === n.id ? byId.get(e.from) : undefined;
      return other !== undefined && (before ? other.layer < n.layer : other.layer > n.layer) ? [other] : [];
    });
  for (let s = 0; s < 4; s++) {
    const forward = s % 2 === 0;
    const order = forward ? layers.map((_, i) => i) : layers.map((_, i) => layers.length - 1 - i);
    for (const li of order) {
      const l = layers[li]!;
      const want = l.map((n) => {
        const ns = near(n, forward);
        return ns.length === 0 ? n[cross] : ns.reduce((sum, o) => sum + mid(o), 0) / ns.length - n[crossSize] / 2;
      });
      // Along the layer, no card over the one before it; then shift the
      // whole layer by the mean shortfall, so it does not drift.
      const got: number[] = [];
      l.forEach((_, i) => {
        const prev = i === 0 ? -Infinity : got[i - 1]! + l[i - 1]![crossSize] + gap;
        got.push(Math.max(want[i]!, prev));
      });
      const drift = got.reduce((s2, c, i) => s2 + (c - want[i]!), 0) / l.length;
      l.forEach((n, i) => {
        n[cross] = got[i]! - drift;
      });
    }
  }
  // The middle of the graph at 0, in whole units so text stays crisp.
  const all = layers.flat();
  const lo = Math.min(...all.map((n) => n[cross]));
  const hi = Math.max(...all.map((n) => n[cross] + n[crossSize]));
  const shift = (lo + hi) / 2;
  for (const n of all) n[cross] = Math.round(n[cross] - shift);
}

export function boundsOf(nodes: readonly Placed[]): Bounds {
  if (nodes.length === 0) return { x: 0, y: 0, w: 0, h: 0 };
  const x = Math.min(...nodes.map((n) => n.x));
  const y = Math.min(...nodes.map((n) => n.y));
  return { x, y, w: Math.max(...nodes.map((n) => n.x + n.w)) - x, h: Math.max(...nodes.map((n) => n.y + n.h)) - y };
}

// An edge as a smooth curve from the right side of a to the left side of
// b, or down from its bottom to b's top. Its handles reach half the gap, at
// least 32 units, so an edge across one column is an S and a long one
// leaves and lands level.
export function edgePath(a: Bounds, b: Bounds, dir: Direction = "across"): string {
  if (dir === "down") {
    const x1 = a.x + a.w / 2;
    const y1 = a.y + a.h;
    const x2 = b.x + b.w / 2;
    const y2 = b.y;
    const reach = Math.max(24, Math.abs(y2 - y1) / 2);
    return `M${x1} ${y1} C${x1} ${y1 + reach} ${x2} ${y2 - reach} ${x2} ${y2}`;
  }
  const x1 = a.x + a.w;
  const y1 = a.y + a.h / 2;
  const x2 = b.x;
  const y2 = b.y + b.h / 2;
  const reach = Math.max(32, Math.abs(x2 - x1) / 2);
  return `M${x1} ${y1} C${x1 + reach} ${y1} ${x2 - reach} ${y2} ${x2} ${y2}`;
}

// The ids next to a node: itself, and every node one edge away.
export function neighbourhood(edges: readonly GraphEdge[], id: string): Set<string> {
  const out = new Set([id]);
  for (const e of edges) {
    if (e.from === id) out.add(e.to);
    if (e.to === id) out.add(e.from);
  }
  return out;
}

export interface View {
  // Screen = graph × k + (x, y).
  x: number;
  y: number;
  k: number;
}

export const ZOOM_MIN = 0.4;
export const ZOOM_MAX = 2;

export const clampZoom = (k: number): number => Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, k));

// The view that shows all of b in a w × h viewport, inside the padding,
// centred, never above 100% (a small graph stays its size) nor below the
// least zoom.
export function fitView(
  b: Bounds,
  w: number,
  h: number,
  pad: { top: number; right: number; bottom: number; left: number },
): View {
  const room = { w: Math.max(1, w - pad.left - pad.right), h: Math.max(1, h - pad.top - pad.bottom) };
  const k = clampZoom(Math.min(1, room.w / Math.max(1, b.w), room.h / Math.max(1, b.h)));
  return {
    k,
    x: Math.round(pad.left + (room.w - b.w * k) / 2 - b.x * k),
    y: Math.round(pad.top + (room.h - b.h * k) / 2 - b.y * k),
  };
}

// Zooms to k around a point on screen, which stays over the same spot of
// the graph.
export function zoomAt(v: View, k: number, px: number, py: number): View {
  const next = clampZoom(k);
  const f = next / v.k;
  return { k: next, x: px - (px - v.x) * f, y: py - (py - v.y) * f };
}

// The node an arrow key moves to: along a layer (up and down a column, or
// left and right along a row), or to the nearest node in the next layer
// that has one.
export function stepFocus(nodes: readonly GraphNode[], from: string, key: string, dir: Direction = "across"): string | null {
  const cur = nodes.find((n) => n.id === from);
  if (cur === undefined) return nodes[0]?.id ?? null;
  const ax = AXES[dir];
  const mid = (n: GraphNode) => n[ax.cross] + n[ax.crossSize] / 2;
  const [back, fwd, prevLayer, nextLayer] =
    dir === "across" ? ["ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"] : ["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"];
  if (key === back || key === fwd) {
    const line = nodes.filter((n) => n.layer === cur.layer).sort((a, b) => a[ax.cross] - b[ax.cross]);
    const i = line.indexOf(cur) + (key === fwd ? 1 : -1);
    return line[i]?.id ?? cur.id;
  }
  if (key !== prevLayer && key !== nextLayer) return null;
  const step = key === nextLayer ? 1 : -1;
  const ahead = [...new Set(nodes.map((n) => n.layer))].filter((l) => (l - cur.layer) * step > 0).sort((a, b) => (a - b) * step);
  const next = ahead[0];
  if (next === undefined) return cur.id;
  const line = nodes.filter((n) => n.layer === next);
  line.sort((a, b) => Math.abs(mid(a) - mid(cur)) - Math.abs(mid(b) - mid(cur)) || a[ax.cross] - b[ax.cross]);
  return line[0]!.id;
}

// The direction that shows the graph larger in a w × h canvas: across,
// unless down shows it at least 15% larger (a tall, narrow panel).
export function chooseDirection(across: Bounds, down: Bounds, w: number, h: number): Direction {
  const scale = (b: Bounds) => Math.min(1, (w - 48) / Math.max(1, b.w), (h - 96) / Math.max(1, b.h));
  return scale(down) > scale(across) * 1.15 ? "down" : "across";
}

// What the canvas's legend counts.
export function graphSummary(g: Graph): { agents: number; working: number; tasks: number; done: number; files: number } {
  let agents = 0;
  let working = 0;
  let tasks = 0;
  let done = 0;
  let files = 0;
  for (const n of g.nodes) {
    if (n.kind === "agent") {
      agents++;
      if (n.status === "working") working++;
    } else if (n.kind === "task") {
      tasks++;
      if (n.task.status === "done") done++;
    } else if (n.kind === "file") files += n.glob ? 0 : 1;
    else if (n.kind === "fold") files += n.paths.length;
  }
  return { agents, working, tasks, done, files };
}

// Only the person: nothing to map yet.
export const isEmpty = (g: Graph): boolean => g.nodes.every((n) => n.kind === "you");
