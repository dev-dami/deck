// A model's reasoning effort as a dial (ui/EffortDial.tsx; design-glass.md,
// section 12): a ring of segments, one for each level the model takes, lit
// up to the one it runs at, round a cap whose pointer turns to that level.
// Turning it spins the cap. The words stay in the pickers' rows, the
// tooltip and for assistive tech.

// Levels that turn reasoning off: the scale starts after them, so at one of
// them no segment is lit.
const OFF = new Set(["none", "off"]);

// The scale drawn for a level the catalog gave no list for (a model set by
// hand, or one the CLI lists without its efforts): Claude Code's, the
// longest an agent offers.
export const FALLBACK_LEVELS = ["low", "medium", "high", "xhigh", "max"];

// More segments than this no longer read on a 20px ring.
export const MAX_SEGMENTS = 7;

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
// [] means the model takes no effort, and the composer shows no dial.
export function effortLevels(efforts: readonly string[], effort: string): string[] {
  if (efforts.length > 0) return efforts.includes(effort) || effort === "" ? [...efforts] : [...efforts, effort];
  if (effort === "") return [];
  return FALLBACK_LEVELS.includes(effort) ? [...FALLBACK_LEVELS] : [...FALLBACK_LEVELS, effort];
}

// The dial for effort on levels: how many segments, how many of them are
// lit, and where the pointer stands, in degrees clockwise from twelve
// o'clock. The ring runs from -135° to 135°, its gap at the bottom. The
// agent's default ("") lights none and rests the pointer in the gap
// (-180°, so turning up spins it clockwise the long way round); an off
// level rests it just before the first segment.
export interface Dial {
  count: number;
  lit: number;
  auto: boolean;
  angle: number;
}

// The ring's sweep and the gap between two segments, in degrees.
export const SWEEP = 270;
export const GAP = 14;
const START = -SWEEP / 2;

export function effortDial(levels: readonly string[], effort: string): Dial {
  const startsOff = levels.length > 0 && OFF.has(levels[0] as string);
  const scale = startsOff ? levels.slice(1) : [...levels];
  const count = Math.min(MAX_SEGMENTS, Math.max(1, scale.length));
  if (effort === "") return { count, lit: 0, auto: true, angle: -180 };
  const i = scale.indexOf(effort);
  const lit = i < 0 ? 0 : Math.min(count, i + 1);
  return { count, lit, auto: false, angle: lit === 0 ? START - GAP : segmentCentre(count, lit - 1) };
}

// The ring's segments, each as its start and end angle.
export function dialSegments(count: number): Array<[number, number]> {
  const seg = (SWEEP - (count - 1) * GAP) / count;
  return Array.from({ length: count }, (_, i) => {
    const a = START + i * (seg + GAP);
    return [a, a + seg];
  });
}

function segmentCentre(count: number, i: number): number {
  const [a, b] = dialSegments(count)[i] as [number, number];
  return (a + b) / 2;
}

// An SVG arc path round (c, c) at radius r from angle a to b (degrees,
// clockwise from twelve o'clock).
export function arcPath(c: number, r: number, a: number, b: number): string {
  const pt = (deg: number) => {
    const rad = (deg * Math.PI) / 180;
    return `${(c + r * Math.sin(rad)).toFixed(3)} ${(c - r * Math.cos(rad)).toFixed(3)}`;
  };
  return `M ${pt(a)} A ${r} ${r} 0 ${b - a > 180 ? 1 : 0} 1 ${pt(b)}`;
}

// The level steps away from effort on the dial's stops: the agent's
// default, then each level, lightest first. It stops at either end.
export function effortStep(levels: readonly string[], effort: string, steps: number): string {
  const stops = ["", ...levels];
  const at = Math.max(0, stops.indexOf(effort));
  return stops[Math.min(stops.length - 1, Math.max(0, at + steps))] as string;
}
