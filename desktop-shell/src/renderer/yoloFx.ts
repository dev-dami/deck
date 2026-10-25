import type { Mode } from "@web/protocol";

// The Yolo shockwave, after the terminal's own (internal/ui/yolo): the same
// timeline, spring and colour band, so the two feel the same. A front leaves
// the middle of the window and reaches the corners at TRAVEL; whatever it
// crosses is kicked outwards on a damped spring and settles back; a band of
// colour rides behind the front and fades; a big YOLO pops in the middle.
// Times are milliseconds; the spring's constants are seconds, as in Go.
//
//	0ms     the wave leaves the centre with a white-hot front
//	80ms    YOLO pops in the middle
//	900ms   the front reaches the corners
//	1150ms  YOLO goes
//	1450ms  everything is back in place

export const DURATION = 1450;
export const TRAVEL = 900;
export const SETTLE_AT = 1150;
export const WORD_ON = 80;
export const WORD_OFF = 1150;
export const POP_FOR = 140;

// The spring, timed from the moment the front reaches a point: a kick of
// PUSH cells outwards, swinging with period SWING and dying away over DAMP.
// Colour flares and fades over GLOW; the crest lasts CREST.
export const PUSH = 4;
export const SWING = 0.34;
export const DAMP = 0.2;
export const GLOW = 0.17;
export const CREST = 0.04;

// Degrees of hue per cell of radius, and how fast the colours run outwards
// in degrees per second.
export const HUE_SPREAD = 9;
export const HUE_SPEED = 520;

// One terminal cell in CSS pixels: the hue spread and the push are per cell.
export const CELL_PX = 8;
// How far a region of the window moves at the spring's peak.
export const REGION_PUSH_PX = 18;

// Tiers of the colour band, from the front backwards; NONE is the window's
// own colours.
export const TIER_FRONT = 0;
export const TIER_STRONG = 1;
export const TIER_MEDIUM = 2;
export const TIER_GLOW = 3;
export const TIER_NONE = -1;
export type Tier = typeof TIER_FRONT | typeof TIER_STRONG | typeof TIER_MEDIUM | typeof TIER_GLOW | typeof TIER_NONE;

// The intensity at which each tier starts, strongest first.
const TIER_FLOOR: ReadonlyArray<readonly [Tier, number]> = [
  [TIER_STRONG, 0.62],
  [TIER_MEDIUM, 0.32],
  [TIER_GLOW, 0.12],
];

export const clamp01 = (p: number): number => Math.max(0, Math.min(1, p));
export const easeOut = (p: number): number => 1 - (1 - clamp01(p)) ** 3;
export const smooth = (p: number): number => {
  const q = clamp01(p);
  return q * q * (3 - 2 * q);
};

// Whether the effect is over at t ms after its start, or not begun.
export const done = (t: number): boolean => t < 0 || t >= DURATION;

// How much of the motion is left: 1 until SETTLE_AT, then down to 0 at DURATION.
export function envelope(t: number): number {
  if (done(t)) return 0;
  return t > SETTLE_AT ? 1 - smooth((t - SETTLE_AT) / (DURATION - SETTLE_AT)) : 1;
}

// The front's radius at t, for a window whose corners are reach away.
export function frontRadius(t: number, reach: number): number {
  return easeOut(t / TRAVEL) * reach;
}

// When the front reaches radius r: easeOut inverted.
export function arrival(r: number, reach: number): number {
  return (1 - Math.cbrt(1 - clamp01(r / reach))) * TRAVEL;
}

// The spring at dt seconds after the front passed, from -1 to 1 before the
// envelope; zero before the front arrives.
export function spring(dt: number): number {
  if (dt <= 0) return 0;
  return Math.sin((dt * 2 * Math.PI) / SWING) * Math.exp(-dt / DAMP);
}

// The spring's displacement in cells at dt seconds after the front passed.
export const kick = (dt: number): number => PUSH * spring(dt);

// The spring's highest value, to scale a push in pixels to its peak.
export const SPRING_PEAK = (() => {
  let best = 0;
  for (let dt = 0; dt < SWING; dt += 0.0005) best = Math.max(best, spring(dt));
  return best;
})();

// How strongly the band shows at a point the front passed since seconds ago.
export function intensity(since: number, env: number): number {
  return since < 0 ? 0 : env * Math.exp(-since / GLOW);
}

// The colour tier for a point the front reached since seconds ago.
export function tier(since: number, env: number): Tier {
  if (since < 0) return TIER_NONE;
  if (since < CREST && env > 0.5) return TIER_FRONT;
  const i = intensity(since, env);
  for (const [t, floor] of TIER_FLOOR) if (i > floor) return t;
  return TIER_NONE;
}

// How long ago, in seconds, a point left a tier, so the band's edges are
// radii: the band ends where intensity falls to the floor of its last tier.
export function tierEnds(env: number): Array<readonly [Tier, number]> {
  const out: Array<readonly [Tier, number]> = [];
  if (env > 0.5) out.push([TIER_FRONT, CREST]);
  for (const [t, floor] of TIER_FLOOR) if (env > floor) out.push([t, GLOW * Math.log(env / floor)]);
  return out;
}

// The hue at radius r (in cells) and time tSec: it rises outwards and runs
// outwards over time.
export function hueAt(r: number, tSec: number): number {
  const h = (r * HUE_SPREAD - HUE_SPEED * tSec) % 360;
  return h < 0 ? h + 360 : h;
}

// One tier of the band as a CSS colour over the window: white-hot on dark,
// near-black on light, as the terminal's word.go does for the front.
export function bandColour(t: Tier, hue: number, dark: boolean): string {
  const hsl = (s: number, l: number, a: number) => `hsl(${hue.toFixed(1)} ${s}% ${l}% / ${a})`;
  if (t === TIER_NONE) return hsl(0, 0, 0);
  if (dark) {
    if (t === TIER_FRONT) return hsl(100, 92, 0.9);
    if (t === TIER_STRONG) return hsl(88, 55, 0.5);
    if (t === TIER_MEDIUM) return hsl(80, 42, 0.32);
    return hsl(90, 68, 0.14);
  }
  if (t === TIER_FRONT) return hsl(35, 9, 0.85);
  if (t === TIER_STRONG) return hsl(88, 55, 0.42);
  if (t === TIER_MEDIUM) return hsl(80, 70, 0.32);
  return hsl(90, 45, 0.12);
}

// Where a region of the window sits at t: on its spring, pushed outwards
// along the line from the window's centre (cx, cy) through its own (x, y).
// since is how long ago the front reached it in seconds, negative before.
export function regionShift(
  t: number,
  cx: number,
  cy: number,
  x: number,
  y: number,
): { dx: number; dy: number; since: number; env: number } {
  const env = envelope(t);
  const vx = x - cx;
  const vy = y - cy;
  const r = Math.hypot(vx, vy);
  const reach = Math.hypot(cx, cy);
  const since = done(t) ? -1 : (t - arrival(r, reach)) / 1000;
  if (since <= 0 || r < 1 || env <= 0) return { dx: 0, dy: 0, since, env };
  const d = (REGION_PUSH_PX * env * spring(since)) / SPRING_PEAK;
  return { dx: (vx / r) * d, dy: (vy / r) * d, since, env };
}

// The block font, five rows, with just the letters YOLO needs.
const FONT: Record<string, readonly string[]> = {
  Y: ["#...#", ".#.#.", "..#..", "..#..", "..#.."],
  O: [".###.", "#...#", "#...#", "#...#", ".###."],
  L: ["#....", "#....", "#....", "#....", "#####"],
};
export const LETTER_W = 5;
export const LETTER_H = 5;
export const LETTER_GAP = 2;
export const WORD_COLS = 4 * LETTER_W + 3 * LETTER_GAP;

// The word YOLO as rows of font pixels.
export const WORD: readonly boolean[][] = Array.from({ length: LETTER_H }, (_, row) => {
  const cells: boolean[] = [];
  [..."YOLO"].forEach((ch, i) => {
    if (i > 0) for (let g = 0; g < LETTER_GAP; g++) cells.push(false);
    for (const c of FONT[ch]![row]!) cells.push(c === "#");
  });
  return cells;
});

export const wordShowing = (t: number): boolean => t >= WORD_ON && t < WORD_OFF;

// The word's colour at t: a white flash settling to a bright tone on dark,
// black settling to near-black on light.
export function wordColour(t: number, dark: boolean): string {
  const p = clamp01((t - WORD_ON) / POP_FOR);
  const hot = dark ? [255, 255, 255] : [0, 0, 0];
  const base = dark ? [244, 247, 246] : [16, 20, 19];
  const c = hot.map((v, i) => Math.round(v + ((base[i] ?? v) - v) * p));
  return `rgb(${c.join(" ")})`;
}

// Whether the open room's mode moving from prev to next plays the effect: it
// must be the same room, and it must go to yolo from a mode that is known and
// is not yolo. An unknown mode (null) is a room still loading, not a switch.
export function playsOn(prev: { room: string; mode: Mode | null } | null, next: { room: string; mode: Mode | null }): boolean {
  return prev !== null && prev.room === next.room && next.room !== "" && next.mode === "yolo" && prev.mode !== null && prev.mode !== "yolo";
}
