import type { AgentLine, Message } from "@web/protocol";
import { agentRole, agentTint } from "./agents";
import type { AgentNode } from "./crew";
import type { Lease } from "./roomMeta";

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
  // The paths the agent holds a lease on.
  leases: string[];
  // A teammate's role (observer, participant or editor), or an agent's
  // one-line role from its profile.
  role: string;
  // The colour of the agent's profile; "" for the kind's hue.
  color: string;
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

// The paths in an agent's lease summary: "a/**, b.go (exclusive); c/**".
export function leasePaths(leases: string): string[] {
  return leases
    .split(";")
    .flatMap((group) => group.replace(/\(exclusive\)/g, "").split(","))
    .map((p) => p.trim())
    .filter((p) => p !== "");
}

// How full an agent's context is, in percent; null when it reports none.
export function contextPct(n: AgentNode | undefined): number | null {
  return n !== undefined && n.contextWindow > 0 ? Math.min(100, Math.round((n.contextTokens / n.contextWindow) * 100)) : null;
}

// The tree node of a roster line.
export function nodeOf(line: AgentLine, nodes: readonly AgentNode[] | null): AgentNode | undefined {
  return nodes?.find((x) => x.name === line.id || x.name === line.name);
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
  leases = [],
}: {
  roster: AgentLine[];
  // The agents tree, or null before it loads (a paired Deck has none).
  agents: AgentNode[] | null;
  // Teammate to role, from ThreadShares.
  shares: Record<string, string>;
  // The ids of agents with an open approval in this room.
  waiting: string[];
  // The room's live leases (lease_changed), when the Deck reports them.
  leases?: readonly Lease[];
}): Lane[] {
  const lanes: Lane[] = [
    { id: YOU, kind: "you", name: "you", agentKind: "", status: "idle", model: "", effort: "", contextPct: null, leases: [], role: "", color: "", depth: 0 },
  ];
  // An agent's live leases, else what the agents tree last said.
  const leased = (name: string, n: AgentNode | undefined): string[] => {
    const live = leases.filter((l) => l.agent === name).flatMap((l) => l.paths);
    return live.length > 0 ? live : leasePaths(n?.leases ?? "");
  };
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
    contextPct: contextPct(n),
    leases: leased(name, n),
    role: agentRole(name, line === undefined ? [] : [line], n === undefined ? [] : [n]),
    color: agentTint(name, line === undefined ? [] : [line], n === undefined ? [] : [n]),
    depth: n?.depth ?? 0,
  });
  for (const line of roster) {
    const n = nodeOf(line, nodes);
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
    lanes.push({ id: mateLane(peer), kind: "mate", name: peer, agentKind: "", status: "idle", model: "", effort: "", contextPct: null, leases: [], role, color: "", depth: 0 });
  }
  return lanes;
}

// How many lines chips of these widths take in a row of width, as
// flex-wrap lays them out with gap between them.
export function lineCount(widths: readonly number[], width: number, gap: number): number {
  let lines = 0;
  let used = 0;
  for (const w of widths) {
    if (lines === 0 || used + gap + w > width) {
      lines++;
      used = w;
    } else used += gap + w;
  }
  return lines;
}

// A lane that never folds: the person, and an agent that works, waits or failed.
export const pinnedLane = (l: Lane): boolean =>
  l.kind === "you" || (l.kind === "agent" && (l.status === "working" || l.status === "waiting" || l.status === "error"));

// The lanes that fit in lines rows of width, and the ones that fold into
// "+N" (a chip of width more). Lanes fold from the end, so teammates fold
// before agents; a pinned lane never folds.
export function fitLanes(
  lanes: readonly Lane[],
  widthOf: (l: Lane) => number,
  width: number,
  { gap, lines, more }: { gap: number; lines: number; more: number },
): { shown: Lane[]; folded: Lane[] } {
  const all = lanes.map(widthOf);
  if (width <= 0 || lineCount(all, width, gap) <= lines) return { shown: [...lanes], folded: [] };
  const keep = lanes.map(() => true);
  for (let i = lanes.length - 1; i >= 0; i--) {
    if (pinnedLane(lanes[i]!)) continue;
    keep[i] = false;
    const widths = [...all.filter((_, j) => keep[j]), more];
    if (lineCount(widths, width, gap) <= lines) break;
  }
  return { shown: lanes.filter((_, i) => keep[i]), folded: lanes.filter((_, i) => !keep[i]) };
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

// The short id of a room's corner index: the last three base36 characters
// of its id, so "ses_01HZ7A3F" reads "a3f". The header, the sidebar rows
// and the plan cards all show this one.
export function shortId(id: string): string {
  const tail = id.toLowerCase().replace(/[^0-9a-z]/g, "").slice(-3);
  return tail === "" ? id.slice(-3) : tail;
}
