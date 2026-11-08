// A model's reasoning effort as a glyph (ui/EffortGlyph.tsx, after T3 Code's
// effort bars): rising bars, one for each level the model takes, filled up
// to the one it runs at. The words stay in the pickers' rows, the tooltip
// and for assistive tech.

// Levels that turn reasoning off: the scale starts after them, so at one of
// them no bar is filled.
const OFF = new Set(["none", "off"]);

// The scale drawn for a level the catalog gave no list for (a model set by
// hand, or one the CLI lists without its efforts): Claude Code's, the
// longest an agent offers.
export const FALLBACK_LEVELS = ["low", "medium", "high", "xhigh", "max"];

// More bars than this no longer fit the glyph's height in whole pixels.
export const MAX_BARS = 7;

const NAMES: Record<string, string> = {
  none: "None",
  off: "Off",
  minimal: "Minimal",
  low: "Low",
  medium: "Medium",
  high: "High",
  xhigh: "Extra high",
  max: "Max",
};

// A level as people read it: "xhigh" is "Extra high"; "" is the agent's own.
export function effortName(level: string): string {
  if (level === "") return "Default";
  return NAMES[level] ?? level.charAt(0).toUpperCase() + level.slice(1);
}

// The levels the effort picker offers for a model that lists efforts
// (lightest first, as the catalog gives them) and runs at effort: the
// list, or, when it lists none but a level is set, the fallback scale with
// that level in it.
// [] means the model takes no effort, and the composer shows no glyph.
export function effortLevels(efforts: readonly string[], effort: string): string[] {
  if (efforts.length > 0) return efforts.includes(effort) || effort === "" ? [...efforts] : [...efforts, effort];
  if (effort === "") return [];
  return FALLBACK_LEVELS.includes(effort) ? [...FALLBACK_LEVELS] : [...FALLBACK_LEVELS, effort];
}

// The glyph for effort on levels: how many bars and how many of them are
// filled. An off level fills none; the agent's default ("") fills none
// and says so, so it is drawn as a scale at rest, not as "off".
export function effortGlyph(levels: readonly string[], effort: string): { count: number; filled: number; auto: boolean } {
  const startsOff = levels.length > 0 && OFF.has(levels[0] as string);
  const scale = startsOff ? levels.slice(1) : [...levels];
  const count = Math.min(MAX_BARS, Math.max(1, scale.length));
  if (effort === "") return { count, filled: 0, auto: true };
  const i = scale.indexOf(effort);
  return { count, filled: i < 0 ? 0 : Math.min(count, i + 1), auto: false };
}

// Bar heights that rise by one whole-pixel step, from about half height up
// to max (T3 Code's rule: rounding each bar alone mixes 1px and 2px steps,
// so the step is rounded once).
export function barHeights(count: number, max: number): number[] {
  const step = Math.max(1, Math.round(max / 2 / Math.max(count - 1, 1)));
  return Array.from({ length: count }, (_, i) => max - (count - 1 - i) * step);
}
