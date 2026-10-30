// The multi-agent and multiplayer side of a local Deck (proto/deck/v1:
// Board, Agents, Team and friends), as plain typed objects.

import { bool, list, num, type Obj, obj, str, strs } from "./guards";
import { type Level, levelOf } from "./orchestration";

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
  // The thread's orchestration level (orchestration.ts).
  level: Level;
  tasks: BoardTask[];
}

export function toBoard(v: unknown): Board {
  const o = obj(v);
  return {
    level: levelOf(o["level"], bool(o, "orchestrate")),
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
  // The profile it was started from and that profile's one-line role
  // (docs/design/agents-and-mentions.md), and its colour; "" for none.
  profile: string;
  role: string;
  color: string;
  // A subagent its member's CLI started with its own tool (Claude Code's
  // Task), not with agent.spawn: name is "<member>/<name>", the agent of its
  // messages, and only its member can be steered or stopped. subStatus is
  // running, done, failed or stopped; description its task, subagentType
  // the CLI's kind of it, actions its calls so far, error why it failed,
  // toolCallId its member's call that started it, ended when it ended (ms).
  native: boolean;
  subStatus: SubStatus | "";
  description: string;
  subagentType: string;
  actions: number;
  error: string;
  toolCallId: string;
  ended: number;
}

export type SubStatus = "running" | "done" | "failed" | "stopped";

// The native fields of a node that is not a CLI-native subagent.
export const notNative = {
  native: false,
  subStatus: "",
  description: "",
  subagentType: "",
  actions: 0,
  error: "",
  toolCallId: "",
  ended: 0,
} as const satisfies Partial<AgentNode>;

const SUB_STATUS: readonly string[] = ["running", "done", "failed", "stopped"];
const subStatusOf = (v: string): SubStatus | "" => (SUB_STATUS.includes(v) ? (v as SubStatus) : "");

const subsMemo = new WeakMap<readonly AgentNode[], ReadonlyMap<string, AgentNode>>();
const NO_SUBS: ReadonlyMap<string, AgentNode> = new Map();

// The subagents of a tree by name, the same map for the same tree: the
// CLI-native ones and the ones an agent started with agent.spawn.
export function subsOf(agents: readonly AgentNode[] | null | undefined): ReadonlyMap<string, AgentNode> {
  if (agents === null || agents === undefined) return NO_SUBS;
  let m = subsMemo.get(agents);
  if (m === undefined) {
    m = new Map(agents.filter((a) => a.native || a.parent !== "").map((a) => [a.name, a]));
    subsMemo.set(agents, m);
  }
  return m;
}

// How a subagent stands. A CLI-native one says so itself; one agent.spawn
// started is a member: it runs while it works or waits, failed on an error,
// and is done once it exited, or is idle with its last message finished.
// Without the tree's word, its messages say.
export function subStanding(n: AgentNode | undefined, finished: boolean): SubStatus {
  if (n?.native === true && n.subStatus !== "") return n.subStatus;
  switch (n?.native === false ? n.status : "") {
    case "working":
    case "tool":
    case "approval":
      return "running";
    case "error":
      return "failed";
    case "exited":
      return "done";
  }
  return finished ? "done" : "running";
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
      profile: str(a, "profile"),
      role: str(a, "role"),
      color: str(a, "color"),
      native: bool(a, "native"),
      subStatus: subStatusOf(str(a, "sub_status")),
      description: str(a, "description"),
      subagentType: str(a, "subagent_type"),
      actions: num(a, "actions"),
      error: str(a, "error"),
      toolCallId: str(a, "tool_call_id"),
      ended: num(a, "ended"),
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

// One line of a teammate's shared thread, as the engine writes it for
// agents: "[claude2] text", "[ana → claude-1] text", or a note such as
// "[claude2 ran: go test]".
export interface MirrorRow {
  who: string;
  to: string;
  text: string;
  note: boolean;
}

export function mirrorRow(line: string): MirrorRow {
  const said = /^\[([^\]\n]+)\] ([\s\S]*)$/.exec(line);
  if (said !== null) {
    const [who = "", to = ""] = (said[1] ?? "").split(" → ");
    return { who: who.trim(), to: to.trim(), text: said[2] ?? "", note: false };
  }
  const did = /^\[(\S+) ((?:changed|ran)\b[\s\S]*)\]$/.exec(line);
  if (did !== null) return { who: did[1] ?? "", to: "", text: did[2] ?? "", note: true };
  return { who: "", to: "", text: line, note: true };
}

// Apply and discard change the tree or drop work, so the board asks first.
export type Decision = "apply" | "discard";

export const asks = (action: string): action is Decision => action === "apply" || action === "discard";

export interface PreviewFile {
  path: string;
  // The file's diff from the thread's Changes, when the tree is on the
  // task's branch; else null and the dialog lists the path alone.
  diff: string | null;
  added: number;
  removed: number;
}

// What the dialog shows before an apply or a discard. Changes reads the
// thread's work tree, so its diff is the task's only while that tree is on
// the task's branch.
export function taskPreview(
  task: BoardTask,
  branch: string | null,
  changes: { path: string; added: number; removed: number; diff?: string }[] | null,
): PreviewFile[] {
  const diffs = branch !== null && task.branch !== "" && task.branch === branch ? (changes ?? []) : [];
  const paths = task.files.length > 0 ? task.files : diffs.map((f) => f.path);
  return paths.map((path) => {
    const f = diffs.find((d) => d.path === path);
    return { path, diff: f?.diff ?? null, added: f?.added ?? 0, removed: f?.removed ?? 0 };
  });
}

export function decisionText(action: Decision, task: BoardTask, files: number): { title: string; description: string; button: string } {
  const name = task.entry ? `${task.owner || task.id}'s entry` : task.title || task.id;
  const what = files === 0 ? "its work" : `${files} ${files === 1 ? "file" : "files"}`;
  return action === "apply"
    ? {
        title: `Apply ${name}?`,
        description: `Deck writes ${what} from ${task.branch || task.id} into the work tree.${task.entry ? " The other entries of the race are discarded." : ""}`,
        button: "Apply",
      }
    : { title: `Discard ${name}?`, description: `Deck drops ${what} and its worktree. This cannot be undone.`, button: "Discard" };
}
