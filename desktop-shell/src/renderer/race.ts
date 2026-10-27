import type { RaceEntrant } from "@web/protocol";
import { type AgentModels, DEFAULT_LABEL } from "./agentCatalog";

// The race builder (/race, and the Tasks tab): a race is a list of
// entrants, each an agent kind or a named agent with the model and effort it
// runs. Kinds mix and repeat: codex, claude and claude again on the same
// model is a race of three. Deck starts it with exactly these entrants
// (StartRace, race.start), each on a worktree of its own.

export const RACE_MIN = 2;
export const RACE_MAX = 6;

// What the builder says to a Deck from before races.
export const NO_RACE = "This Deck cannot start races. Update Deck to race agents.";

export interface Entrant {
  // A kind ("claude") or, when profile is set, a named agent's name.
  agent: string;
  // The kind it runs: the agent itself, or the named agent's kind.
  kind: string;
  // "" is the agent's own model and effort.
  model: string;
  effort: string;
  // The agent is a named agent (a profile).
  profile: boolean;
}

export interface RaceDraft {
  brief: string;
  entrants: Entrant[];
  // One more agent, of the first entrant's kind, ranks the entries.
  judge: boolean;
}

export const newRace = (brief = ""): RaceDraft => ({ brief: brief.trim(), entrants: [], judge: true });

export const canAdd = (d: RaceDraft): boolean => d.entrants.length < RACE_MAX;

// Adds e at the end; a full race stays as it is.
export function addEntrant(d: RaceDraft, e: Entrant): RaceDraft {
  return canAdd(d) ? { ...d, entrants: [...d.entrants, e] } : d;
}

export function replaceEntrant(d: RaceDraft, i: number, e: Entrant): RaceDraft {
  if (i < 0 || i >= d.entrants.length) return d;
  return { ...d, entrants: d.entrants.map((x, j) => (j === i ? e : x)) };
}

export function removeEntrant(d: RaceDraft, i: number): RaceDraft {
  return { ...d, entrants: d.entrants.filter((_, j) => j !== i) };
}

// What keeps the race from starting, for the Start button's tooltip; null
// when it can start.
export function raceProblem(d: RaceDraft): string | null {
  if (d.entrants.length < RACE_MIN) return `Add ${RACE_MIN - d.entrants.length === 1 ? "one more agent" : `at least ${RACE_MIN} agents`}.`;
  if (d.entrants.length > RACE_MAX) return `A race takes at most ${RACE_MAX} agents.`;
  if (d.brief.trim() === "") return "Say what the race is for.";
  return null;
}

// The entrants as Deck takes them: an empty model or effort is left out.
export function toWire(d: RaceDraft): RaceEntrant[] {
  return d.entrants.map((e) => ({
    agent: e.agent,
    ...(e.model === "" ? {} : { model: e.model }),
    ...(e.effort === "" ? {} : { effort: e.effort }),
  }));
}

// The name a picker gives model of kind: its label in the catalog, the id
// when the catalog does not list it, Default for "".
export function modelLabel(agents: readonly AgentModels[] | null, kind: string, model: string): string {
  if (model === "") return DEFAULT_LABEL;
  return agents?.find((a) => a.kind === kind)?.models.find((m) => m.id === model)?.label || model;
}

// An entrant's chip, after its agent: "opus · high", "Default".
export function entrantDetail(e: Entrant, agents: readonly AgentModels[] | null): string {
  return [modelLabel(agents, e.kind, e.model), e.effort].filter((x) => x !== "").join(" · ");
}

// The entrant the Add picker starts on: the last one, so adding the same
// agent again is one click; else the first kind.
export function nextEntrant(d: RaceDraft, kinds: readonly string[]): Entrant | null {
  const last = d.entrants.at(-1);
  if (last !== undefined) return last;
  const k = kinds[0];
  return k === undefined ? null : { agent: k, kind: k, model: "", effort: "", profile: false };
}

// "3 agents: codex, claude ×2", for the builder's summary line.
export function raceSummary(d: RaceDraft): string {
  const counts = new Map<string, number>();
  for (const e of d.entrants) counts.set(e.agent, (counts.get(e.agent) ?? 0) + 1);
  const who = [...counts].map(([a, n]) => (n > 1 ? `${a} ×${n}` : a)).join(", ");
  const n = d.entrants.length;
  return n === 0 ? "No agents yet" : `${n} ${n === 1 ? "agent" : "agents"}: ${who}`;
}
