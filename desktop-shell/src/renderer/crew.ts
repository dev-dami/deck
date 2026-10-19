// The multi-agent and multiplayer side of a local Deck (proto/deck/v1:
// Board, Agents, Team and friends), as plain typed objects.

type Obj = Record<string, unknown>;
const obj = (v: unknown): Obj => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Obj) : {});
const str = (o: Obj, k: string): string => (typeof o[k] === "string" ? (o[k] as string) : "");
const num = (o: Obj, k: string): number => (typeof o[k] === "number" && Number.isFinite(o[k]) ? (o[k] as number) : 0);
const bool = (o: Obj, k: string): boolean => o[k] === true;
const strs = (o: Obj, k: string): string[] => (Array.isArray(o[k]) ? (o[k] as unknown[]).filter((x): x is string => typeof x === "string") : []);
const list = <T>(o: Obj, k: string, f: (v: Obj) => T): T[] => (Array.isArray(o[k]) ? (o[k] as unknown[]).map((v) => f(obj(v))) : []);

export type TaskStatus = "pending" | "ready" | "running" | "review" | "blocked" | "done" | "failed" | "cancelled";

export interface BoardTask {
  id: string;
  title: string;
  status: TaskStatus | string;
  owner: string;
  deps: string[];
  brief: string;
  result: string;
  error: string;
  attempts: number;
  tries: number;
  branch: string;
  updatedAt: number;
  race: boolean;
  entry: boolean;
  stats: string;
  files: string[];
  reviewer: string;
  reviews: number;
}

export interface Board {
  orchestrate: boolean;
  tasks: BoardTask[];
}

export function toBoard(v: unknown): Board {
  const o = obj(v);
  return {
    orchestrate: bool(o, "orchestrate"),
    tasks: list(o, "tasks", (t) => ({
      id: str(t, "id"),
      title: str(t, "title"),
      status: str(t, "status"),
      owner: str(t, "owner"),
      deps: strs(t, "deps"),
      brief: str(t, "brief"),
      result: str(t, "result"),
      error: str(t, "error"),
      attempts: num(t, "attempts"),
      tries: num(t, "tries"),
      branch: str(t, "branch"),
      updatedAt: num(t, "updated_at"),
      race: bool(t, "race"),
      entry: bool(t, "entry"),
      stats: str(t, "stats"),
      files: strs(t, "files"),
      reviewer: str(t, "reviewer"),
      reviews: num(t, "reviews"),
    })),
  };
}

export interface AgentNode {
  name: string;
  kind: string;
  model: string;
  effort: string;
  status: string;
  contextTokens: number;
  contextWindow: number;
  costUsd: number;
  pending: number;
  since: number;
  depth: number;
  parent: string;
  branch: string;
  leases: string;
}

export interface RunningTool {
  agent: string;
  kind: string;
  name: string;
  summary: string;
  detail: string;
  started: number;
  subagent: boolean;
  command: boolean;
}

export interface Crew {
  agents: AgentNode[];
  running: RunningTool[];
}

export function toCrew(v: unknown): Crew {
  const o = obj(v);
  return {
    agents: list(o, "agents", (a) => ({
      name: str(a, "name"),
      kind: str(a, "kind"),
      model: str(a, "model"),
      effort: str(a, "effort"),
      status: str(a, "status"),
      contextTokens: num(a, "context_tokens"),
      contextWindow: num(a, "context_window"),
      costUsd: num(a, "cost_usd"),
      pending: num(a, "pending"),
      since: num(a, "since"),
      depth: num(a, "depth"),
      parent: str(a, "parent"),
      branch: str(a, "branch"),
      leases: str(a, "leases"),
    })),
    running: list(o, "running", (t) => ({
      agent: str(t, "agent"),
      kind: str(t, "kind"),
      name: str(t, "name"),
      summary: str(t, "summary"),
      detail: str(t, "detail"),
      started: num(t, "started"),
      subagent: bool(t, "subagent"),
      command: bool(t, "command"),
    })),
  };
}

export interface TeamThread {
  id: string;
  title: string;
  role: string;
  mode: string;
  members: string[];
  entries: number;
}

export interface Teammate {
  name: string;
  role: string;
  pinned: boolean;
  link: string;
  lastSeen: number;
  present: boolean;
  agents: { name: string; kind: string; status: string; task: string; contextPct: number }[];
  threads: TeamThread[];
}

export interface TeamChat {
  seq: number;
  from: string;
  agent: string;
  text: string;
  at: number;
  mine: boolean;
}

export interface TeamContract {
  id: string;
  peer: string;
  inbound: boolean;
  title: string;
  brief: string;
  state: string;
  agent: string;
  note: string;
  summary: string;
  kind: string;
}

export interface Team {
  available: boolean;
  enabled: boolean;
  name: string;
  fingerprint: string;
  teammates: Teammate[];
  chat: TeamChat[];
  contracts: TeamContract[];
  contractKinds: string[];
}

const toThread = (t: Obj): TeamThread => ({
  id: str(t, "id"),
  title: str(t, "title"),
  role: str(t, "role"),
  mode: str(t, "mode"),
  members: strs(t, "members"),
  entries: num(t, "entries"),
});

export function toTeam(v: unknown): Team {
  const o = obj(v);
  return {
    available: bool(o, "available"),
    enabled: bool(o, "enabled"),
    name: str(o, "name"),
    fingerprint: str(o, "fingerprint"),
    teammates: list(o, "teammates", (p) => ({
      name: str(p, "name"),
      role: str(p, "role"),
      pinned: bool(p, "pinned"),
      link: str(p, "link"),
      lastSeen: num(p, "last_seen"),
      present: bool(p, "present"),
      agents: list(p, "agents", (a) => ({
        name: str(a, "name"),
        kind: str(a, "kind"),
        status: str(a, "status"),
        task: str(a, "task"),
        contextPct: num(a, "context_pct"),
      })),
      threads: list(p, "threads", toThread),
    })),
    chat: list(o, "chat", (m) => ({
      seq: num(m, "seq"),
      from: str(m, "from"),
      agent: str(m, "agent"),
      text: str(m, "text"),
      at: num(m, "at"),
      mine: bool(m, "mine"),
    })),
    contracts: list(o, "contracts", (c) => ({
      id: str(c, "id"),
      peer: str(c, "peer"),
      inbound: bool(c, "inbound"),
      title: str(c, "title"),
      brief: str(c, "brief"),
      state: str(c, "state"),
      agent: str(c, "agent"),
      note: str(c, "note"),
      summary: str(c, "summary"),
      kind: str(c, "kind"),
    })),
    contractKinds: strs(o, "contract_kinds"),
  };
}

export interface SharedMirror {
  thread: TeamThread;
  lines: string[];
  next: number;
  pending: { id: string; agent: string; kind: string; summary: string }[];
}

export function toMirror(v: unknown): SharedMirror {
  const o = obj(v);
  return {
    thread: toThread(obj(o["thread"])),
    lines: strs(o, "lines"),
    next: num(o, "next"),
    pending: list(o, "pending", (p) => ({ id: str(p, "id"), agent: str(p, "agent"), kind: str(p, "kind"), summary: str(p, "summary") })),
  };
}

// The tasks as a tree for the board: each task under its parent race (an
// entry under the race), the others in order.
export function boardRows(b: Board): { task: BoardTask; entries: BoardTask[] }[] {
  const rows: { task: BoardTask; entries: BoardTask[] }[] = [];
  let race: { task: BoardTask; entries: BoardTask[] } | null = null;
  for (const t of b.tasks) {
    if (t.entry && race !== null) {
      race.entries.push(t);
      continue;
    }
    const row = { task: t, entries: [] as BoardTask[] };
    rows.push(row);
    race = t.race ? row : null;
  }
  return rows;
}

// A race in the composer's own syntax: "$claude×2 $codex×1 brief".
export function raceMessage(counts: Record<string, number>, brief: string): string {
  const picks = Object.entries(counts)
    .filter(([, n]) => n > 0)
    .map(([k, n]) => `$${k}×${n}`);
  return `${picks.join(" ")} ${brief.trim()}`;
}
