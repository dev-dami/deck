import type { AgentLine } from "@web/protocol";
import { kindColour } from "@web/theme";

// The kind of an agent id: from the roster when it lists the agent, else
// the id without its instance number ("codex-2" is a codex).
export function agentKind(agentId: string, roster: AgentLine[]): string {
  const line = roster.find((l) => l.id === agentId);
  if (line !== undefined) return line.kind;
  return agentId.replace(/[-_]?\d+$/, "");
}

// The instance number at the end of an id; 1 when there is none.
export function instanceOf(agentId: string): number {
  const m = /(\d+)$/.exec(agentId);
  return m?.[1] === undefined ? 1 : Math.max(1, Number(m[1]));
}

// A stable colour for an agent: its kind's tint from the server theme, made
// lighter for each further instance so two agents of one kind differ.
export function agentColour(agentId: string, kind: string): string {
  const base = kindColour(kind);
  const step = (instanceOf(agentId) - 1) % 4;
  return step === 0 ? base : `color-mix(in oklab, ${base}, white ${step * 18}%)`;
}
