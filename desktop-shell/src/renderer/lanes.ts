import type { AgentLine, Message } from "@web/protocol";
import type { AgentNode } from "./crew";

// The lane strip of a room (docs/plan.md §3): one lane per participant.
// The person, each agent (the roster merged with the agents tree), and each
// teammate who follows the room. Pure, so the tests drive it directly.

export type LaneStatus = "working" | "waiting" | "error" | "idle" | "exited";

export interface Lane {
  // "you", "agent:<name>" or "mate:<peer>".
  id: string;
  kind: "you" | "agent" | "mate";
  name: string;
  // The agent kind (claude, codex…); "" for the person and teammates.
  agentKind: string;
  status: LaneStatus;
  model: string;
  effort: string;
  // Context use in percent, when the agents tree knows the window.
  contextPct: number | null;
  // How many paths the agent holds a lease on.
  leases: number;
  // A teammate's role: observer, participant or editor.
  role: string;
  depth: number;
}

export const YOU = "you";
export const agentLane = (name: string): string => `agent:${name}`;
export const mateLane = (peer: string): string => `mate:${peer}`;

const NODE_STATUS: Record<string, LaneStatus> = {
  working: "working",
  tool: "working",
  approval: "waiting",
  error: "error",
  exited: "exited",
  idle: "idle",
  ready: "idle",
};

// The number of paths in an agent's lease summary: "a/**, b.go (exclusive); c/**".
export function leaseCount(leases: string): number {
  if (leases.trim() === "") return 0;
  return leases
    .split(";")
    .flatMap((group) => group.replace(/\(exclusive\)/g, "").split(","))
    .filter((p) => p.trim() !== "").length;
}

function fromNode(n: AgentNode | undefined, line: AgentLine | undefined, waiting: boolean): LaneStatus {
  // An open approval wins: it is the state a person must act on.
  if (waiting) return "waiting";
  if (n !== undefined && n.status in NODE_STATUS) return NODE_STATUS[n.status]!;
  return line?.busy === true ? "working" : "idle";
}

export function buildLanes({
  roster,
  agents,
  shares,
  waiting,
}: {
  roster: AgentLine[];
  // The agents tree, or null before it loads (a paired Deck has none).
  agents: AgentNode[] | null;
  // Teammate to role, from ThreadShares.
  shares: Record<string, string>;
  // The ids of agents with an open approval in this room.
  waiting: string[];
}): Lane[] {
  const lanes: Lane[] = [
    { id: YOU, kind: "you", name: "you", agentKind: "", status: "idle", model: "", effort: "", contextPct: null, leases: 0, role: "", depth: 0 },
  ];
  const nodes = agents ?? [];
  const used = new Set<AgentNode>();
  const agent = (name: string, line: AgentLine | undefined, n: AgentNode | undefined): Lane => ({
    id: agentLane(name),
    kind: "agent",
    name,
    agentKind: line?.kind ?? n?.kind ?? "",
    status: fromNode(n, line, waiting.includes(name)),
    model: n?.model || line?.model || "",
    effort: n?.effort || line?.effort || "",
    contextPct: n !== undefined && n.contextWindow > 0 ? Math.min(100, Math.round((n.contextTokens / n.contextWindow) * 100)) : null,
    leases: n === undefined ? 0 : leaseCount(n.leases),
    role: "",
    depth: n?.depth ?? 0,
  });
  for (const line of roster) {
    const n = nodes.find((x) => x.name === line.id || x.name === line.name);
    if (n !== undefined) used.add(n);
    lanes.push(agent(line.id, line, n));
  }
  // Subagents are in the tree only. One that exited is gone from the room.
  for (const n of nodes) {
    if (used.has(n) || n.status === "exited") continue;
    lanes.push(agent(n.name, undefined, n));
  }
  for (const [peer, role] of Object.entries(shares).sort(([a], [b]) => a.localeCompare(b))) {
    if (role === "") continue;
    lanes.push({ id: mateLane(peer), kind: "mate", name: peer, agentKind: "", status: "idle", model: "", effort: "", contextPct: null, leases: 0, role, depth: 0 });
  }
  return lanes;
}

// Claude Code's idle collapse: past six agents, the idle ones fold into
// "+N idle". An agent that works, waits or failed always shows.
export function collapseIdle(lanes: Lane[], max = 6): { shown: Lane[]; idle: Lane[] } {
  const shown: Lane[] = [];
  const idle: Lane[] = [];
  const agents = lanes.filter((l) => l.kind === "agent").length;
  let seen = 0;
  for (const l of lanes) {
    if (l.kind !== "agent") {
      shown.push(l);
      continue;
    }
    const quiet = l.status === "idle" || l.status === "exited";
    if (agents > max && quiet && seen >= max) idle.push(l);
    else {
      shown.push(l);
      seen++;
    }
  }
  return { shown, idle };
}

// A teammate's post enters the owner's thread as "[peer] text".
const TEAMMATE = /^\[([^\]\s]+)\] /;

// The lane a message belongs to: an agent's reply to its agent, the person's
// turn to "you", a teammate's post to that teammate. System notes have none.
export function laneOf(m: Message): string | null {
  if (m.role === "assistant") return agentLane(m.agent ?? "agent");
  if (m.role !== "user") return null;
  const text = m.parts.find((p) => p.type === "text")?.text ?? "";
  const mate = TEAMMATE.exec(text);
  return mate?.[1] === undefined ? YOU : mateLane(mate[1]);
}

export function filterLane(messages: Message[], lane: string | null): Message[] {
  return lane === null ? messages : messages.filter((m) => laneOf(m) === lane);
}

// The short id of the room's corner index: "ses_a7f3…" reads "a7f".
export function shortId(id: string): string {
  const tail = id.slice(id.lastIndexOf("_") + 1);
  return (tail === "" ? id : tail).slice(0, 3);
}
