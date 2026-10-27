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

// A stable colour for an agent: its kind's tint from the server theme, or
// the colour of the profile it came from (tint, agentTint), made lighter
// for each further instance so two agents of one kind differ.
export function agentColour(agentId: string, kind: string, tint = ""): string {
  const base = tint || kindColour(kind);
  const step = (instanceOf(agentId) - 1) % 4;
  return step === 0 ? base : `color-mix(in oklab, ${base}, white ${step * 18}%)`;
}

// Something that names a member and may carry its role: a roster line
// (AgentLine) or a node of the agents tree (crew.ts).
export interface Roled {
  id?: string;
  name: string;
  role?: string;
  color?: string;
}

// A field a member carries from its profile, by id or name, from the first
// list that has it; "" when none does.
function fromProfile(key: "role" | "color", agent: string, lists: readonly (readonly Roled[] | undefined)[]): string {
  for (const list of lists) {
    const v = (list ?? []).find((l) => l.id === agent || l.name === agent)?.[key] ?? "";
    if (v !== "") return v;
  }
  return "";
}

// The one-line role a member carries from its profile
// (docs/design/agents-and-mentions.md), by id or name, from the first list
// that has it: the roster, then the agents tree. "" when it has none. The
// lanes, the timeline, the agents tree and the graph all show it after the
// glyph and the name (ui/AgentRole.tsx).
export function agentRole(agent: string, ...lists: readonly (readonly Roled[] | undefined)[]): string {
  return fromProfile("role", agent, lists);
}

// The CSS colour a member carries from its profile, found as agentRole
// finds the role; "" for the kind's hue.
export function agentTint(agent: string, ...lists: readonly (readonly Roled[] | undefined)[]): string {
  return fromProfile("color", agent, lists);
}
