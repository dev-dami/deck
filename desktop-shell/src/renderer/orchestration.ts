// Orchestration levels (internal/deck/config/orchestration.go): how much a
// thread's agents organise the work themselves. The Deck keeps each
// thread's level; Settings › Agents picks the one new threads start at.

export type Level = "off" | "assist" | "auto" | "full";

export const DEFAULT_LEVEL: Level = "off";

// The levels, from least to most, with what each lets agents do.
export const LEVELS: ReadonlyArray<{ level: Level; label: string; blurb: string }> = [
  { level: "off", label: "Off", blurb: "Agents do what they are told. Helpers only when you ask for them." },
  { level: "assist", label: "Assist", blurb: "Agents may start a couple of helpers when it clearly helps." },
  { level: "auto", label: "Auto", blurb: "Agents plan on the task board, split the work and review each other. Ready tasks run by themselves." },
  { level: "full", label: "Full", blurb: "Deeper and wider teams, races between agents, and every helper's work reviewed." },
];

export function isLevel(v: unknown): v is Level {
  return LEVELS.some((l) => l.level === v);
}

// The level a Board response names; a Deck from before levels sends only
// orchestrate, which was on (auto) or off.
export function levelOf(level: unknown, orchestrate: boolean): Level {
  if (isLevel(level)) return level;
  return orchestrate ? "auto" : "off";
}

// Whether the level runs the board's ready tasks by itself.
export function schedules(level: Level): boolean {
  return level === "auto" || level === "full";
}

export function levelInfo(level: Level): { level: Level; label: string; blurb: string } {
  return LEVELS.find((l) => l.level === level) ?? LEVELS[2]!;
}
