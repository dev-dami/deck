// The backdrop's maths, with no DOM: colour, noise, the halftone screen, the
// two-ink separation, palette quantisation and the stitch grid. The canvas
// renderers (scene.ts, halftone.ts, stitch.ts) only draw what these return,
// so everything here is tested in core.test.ts.

export type RGB = readonly [number, number, number];

export const clamp01 = (v: number): number => (v < 0 ? 0 : v > 1 ? 1 : v);

export function hex(h: string): RGB {
  const s = h.replace("#", "");
  const full = s.length === 3 ? [...s].map((c) => c + c).join("") : s;
  const n = Number.parseInt(full.slice(0, 6), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

export function css([r, g, b]: RGB, a = 1): string {
  const c = (v: number) => Math.round(Math.max(0, Math.min(255, v)));
  return a >= 1 ? `rgb(${c(r)} ${c(g)} ${c(b)})` : `rgb(${c(r)} ${c(g)} ${c(b)} / ${a})`;
}

export function mix(a: RGB, b: RGB, t: number): RGB {
  return [a[0] + (b[0] - a[0]) * t, a[1] + (b[1] - a[1]) * t, a[2] + (b[2] - a[2]) * t];
}

// Relative luminance of an sRGB colour, 0..1 (WCAG's formula).
export function luminance([r, g, b]: RGB): number {
  const lin = (v: number) => {
    const c = v / 255;
    return c <= 0.04045 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin(r) + 0.7152 * lin(g) + 0.0722 * lin(b);
}

// Perceived lightness, 0..1, cheap: the luma of the gamma-encoded values.
// The screens use it, as a printer's densitometer would read a proof.
export function lightness([r, g, b]: RGB): number {
  return (0.299 * r + 0.587 * g + 0.114 * b) / 255;
}

// ── Deterministic noise ──────────────────────────────────────────────────
// The same seed draws the same picture, so a re-render after a resize does
// not shimmer.

export function hash2(x: number, y: number, seed = 0): number {
  let h = (Math.imul(x | 0, 374761393) + Math.imul(y | 0, 668265263) + Math.imul(seed | 0, 1442695041)) | 0;
  h = Math.imul(h ^ (h >>> 13), 1274126177);
  h ^= h >>> 16;
  return (h >>> 0) / 4294967296;
}

const smooth = (t: number) => t * t * (3 - 2 * t);

// Value noise, 0..1, smooth between integer lattice points.
export function noise2(x: number, y: number, seed = 0): number {
  const xi = Math.floor(x);
  const yi = Math.floor(y);
  const tx = smooth(x - xi);
  const ty = smooth(y - yi);
  const a = hash2(xi, yi, seed);
  const b = hash2(xi + 1, yi, seed);
  const c = hash2(xi, yi + 1, seed);
  const d = hash2(xi + 1, yi + 1, seed);
  return a + (b - a) * tx + (c - a) * ty + (a - b - c + d) * tx * ty;
}

// Fractal noise: a few octaves of value noise, still 0..1.
export function fbm(x: number, y: number, seed = 0, octaves = 4): number {
  let sum = 0;
  let amp = 0.5;
  let norm = 0;
  let f = 1;
  for (let i = 0; i < octaves; i++) {
    sum += amp * noise2(x * f, y * f, seed + i * 17);
    norm += amp;
    amp *= 0.5;
    f *= 2;
  }
  return sum / norm;
}

// ── Fitting a picture ────────────────────────────────────────────────────

// object-fit: cover. The source rectangle of an sw×sh image that fills a
// dw×dh frame, centred.
export function coverRect(sw: number, sh: number, dw: number, dh: number): { x: number; y: number; w: number; h: number } {
  const scale = Math.max(dw / sw, dh / sh);
  const w = dw / scale;
  const h = dh / scale;
  return { x: (sw - w) / 2, y: (sh - h) / 2, w, h };
}

// Samples an RGBA buffer at a fractional point, bilinearly, clamped to the
// edges. Out: [r, g, b, a] in 0..255.
export function sample(data: ArrayLike<number>, w: number, h: number, x: number, y: number): [number, number, number, number] {
  const fx = Math.max(0, Math.min(w - 1, x - 0.5));
  const fy = Math.max(0, Math.min(h - 1, y - 0.5));
  const x0 = Math.floor(fx);
  const y0 = Math.floor(fy);
  const x1 = Math.min(w - 1, x0 + 1);
  const y1 = Math.min(h - 1, y0 + 1);
  const tx = fx - x0;
  const ty = fy - y0;
  const out: [number, number, number, number] = [0, 0, 0, 0];
  for (let c = 0; c < 4; c++) {
    const a = data[(y0 * w + x0) * 4 + c] ?? 0;
    const b = data[(y0 * w + x1) * 4 + c] ?? 0;
    const d = data[(y1 * w + x0) * 4 + c] ?? 0;
    const e = data[(y1 * w + x1) * 4 + c] ?? 0;
    out[c] = (a * (1 - tx) + b * tx) * (1 - ty) + (d * (1 - tx) + e * tx) * ty;
  }
  return out;
}

// ── The halftone screen ──────────────────────────────────────────────────

// The centres of a dot screen of `pitch` px turned by `angle` degrees,
// covering a w×h frame with one dot of margin, as x, y pairs. A screen is a
// grid, so the same pitch and angle always land the same dots.
export function screen(w: number, h: number, pitch: number, angle: number): Float32Array {
  const t = (angle * Math.PI) / 180;
  const cos = Math.cos(t);
  const sin = Math.sin(t);
  // The frame's corners in the screen's own (unrotated) coordinates.
  const corners: [number, number][] = [
    [0, 0],
    [w, 0],
    [0, h],
    [w, h],
  ];
  let umin = Infinity;
  let umax = -Infinity;
  let vmin = Infinity;
  let vmax = -Infinity;
  for (const [x, y] of corners) {
    const u = x * cos + y * sin;
    const v = -x * sin + y * cos;
    umin = Math.min(umin, u);
    umax = Math.max(umax, u);
    vmin = Math.min(vmin, v);
    vmax = Math.max(vmax, v);
  }
  const i0 = Math.floor(umin / pitch) - 1;
  const i1 = Math.ceil(umax / pitch) + 1;
  const j0 = Math.floor(vmin / pitch) - 1;
  const j1 = Math.ceil(vmax / pitch) + 1;
  const out: number[] = [];
  for (let j = j0; j <= j1; j++) {
    for (let i = i0; i <= i1; i++) {
      const u = i * pitch;
      const v = j * pitch;
      const x = u * cos - v * sin;
      const y = u * sin + v * cos;
      if (x >= -pitch && x <= w + pitch && y >= -pitch && y <= h + pitch) out.push(x, y);
    }
  }
  return Float32Array.from(out);
}

// The radius of a round dot that covers `coverage` (0..1) of its cell. Past
// π/4 the dots touch and the screen closes up into ink, as on press.
export function dotRadius(coverage: number, pitch: number): number {
  return pitch * Math.sqrt(clamp01(coverage) / Math.PI);
}

// A press's tone curve: a little dot gain in the mid-tones and a clean
// paper white, so highlights drop out and shadows fill.
export function toneCurve(c: number, gain = 0.12, floor = 0.06): number {
  const v = clamp01((c - floor) / (1 - floor));
  return clamp01(v + gain * v * (1 - v) * 4 * 0.5);
}

// Two inks on paper (light). Paper is white; black ink absorbs everything;
// the fluorescent pink lets red through and holds back most green. So red
// says how much black there is, and what green loses beyond that is pink.
// Out: coverage of each plate, 0..1.
export function separateOnPaper(rgb: RGB, pink: RGB): { key: number; spot: number } {
  const r = rgb[0] / 255;
  const g = rgb[1] / 255;
  const key = clamp01(1 - r / Math.max(pink[0] / 255, 0.001));
  const left = 1 - key;
  const gp = left <= 0.001 ? 1 : clamp01(g / left);
  const spot = clamp01((1 - gp) / Math.max(1 - pink[1] / 255, 0.001));
  return { key, spot };
}

// Two inks on black stock (dark): white ink and the pink, both opaque. Red
// rises with either ink, green mostly with the white, so their difference
// is the pink. Out: coverage of each plate, 0..1.
export function separateOnBlack(rgb: RGB, pink: RGB): { key: number; spot: number } {
  const r = rgb[0] / 255;
  const g = rgb[1] / 255;
  const pr = pink[0] / 255;
  const pg = pink[1] / 255;
  const spot = clamp01((r - g) / Math.max(pr - pg, 0.001));
  const key = clamp01(Math.min(r - spot * pr, g - spot * pg) / Math.max(1 - spot * Math.max(pr, pg), 0.001));
  return { key, spot };
}

// ── Palette quantisation (the stitch's threads) ──────────────────────────

// "Redmean": a cheap weighted distance that tracks perception far better
// than plain RGB.
export function distance(a: RGB, b: RGB): number {
  const rm = (a[0] + b[0]) / 2;
  const dr = a[0] - b[0];
  const dg = a[1] - b[1];
  const db = a[2] - b[2];
  return (2 + rm / 256) * dr * dr + 4 * dg * dg + (2 + (255 - rm) / 256) * db * db;
}

export function nearest(c: RGB, palette: readonly RGB[]): number {
  let best = 0;
  let bd = Infinity;
  for (let i = 0; i < palette.length; i++) {
    const d = distance(c, palette[i] as RGB);
    if (d < bd) {
      bd = d;
      best = i;
    }
  }
  return best;
}

// Quantises a picture of RGBA pixels to the palette. A pixel under half
// opaque is left bare (-1): the fabric shows. Then only the `keep` threads
// the picture uses most stay, and each cell takes the nearest of those,
// as a pattern is charted with a small box of floss.
export function quantise(data: ArrayLike<number>, palette: readonly RGB[], keep = palette.length): Int16Array {
  const n = Math.floor(data.length / 4);
  const out = new Int16Array(n);
  const counts = new Array<number>(palette.length).fill(0);
  for (let i = 0; i < n; i++) {
    if ((data[i * 4 + 3] ?? 0) < 128) {
      out[i] = -1;
      continue;
    }
    const k = nearest([data[i * 4] ?? 0, data[i * 4 + 1] ?? 0, data[i * 4 + 2] ?? 0], palette);
    out[i] = k;
    counts[k] = (counts[k] ?? 0) + 1;
  }
  if (keep >= palette.length) return out;
  const kept = counts
    .map((c, i) => [c, i] as const)
    .filter(([c]) => c > 0)
    .sort((a, b) => b[0] - a[0])
    .slice(0, keep)
    .map(([, i]) => i);
  const sub = kept.map((i) => palette[i] as RGB);
  for (let i = 0; i < n; i++) {
    if (out[i] === -1) continue;
    out[i] = kept[nearest([data[i * 4] ?? 0, data[i * 4 + 1] ?? 0, data[i * 4 + 2] ?? 0], sub)] ?? 0;
  }
  return out;
}

// ── The stitch grid ──────────────────────────────────────────────────────

// How many cells of `cell` px cover a w×h frame, and the offset that
// centres the grid, so the fabric's holes fall evenly at both edges.
export function grid(w: number, h: number, cell: number): { cols: number; rows: number; ox: number; oy: number } {
  const cols = Math.ceil(w / cell);
  const rows = Math.ceil(h / cell);
  return { cols, rows, ox: (w - cols * cell) / 2, oy: (h - rows * cell) / 2 };
}

// Suit shapes in a unit box, u and v in -1..1 (v down). True inside.
export function inSuit(suit: Suit, u: number, v: number): boolean {
  const circle = (cx: number, cy: number, r: number) => (u - cx) ** 2 + (v - cy) ** 2 <= r * r;
  const stem = () => v > 0.15 && v <= 1 && Math.abs(u) <= 0.12 + (v - 0.15) * 0.45;
  switch (suit) {
    case "club":
      return circle(0, -0.5, 0.42) || circle(-0.5, 0.12, 0.42) || circle(0.5, 0.12, 0.42) || circle(0, 0.05, 0.2) || stem();
    case "spade": {
      // A heart upside down, with a stem.
      const body = inSuit("heart", u, -v * 1.05 + 0.18);
      return body || stem();
    }
    case "heart": {
      if (v < -0.2) return circle(-0.45, -0.38, 0.5) || circle(0.45, -0.38, 0.5);
      return Math.abs(u) <= (0.93 * (0.95 - v)) / 1.15 && v <= 0.95;
    }
    case "diamond":
      return Math.abs(u) / 0.78 + Math.abs(v) <= 1;
  }
}

export type Suit = "club" | "spade" | "heart" | "diamond";

// The pips of a card's face, as centres in 0..1 of the pip field (x, y).
// The classic layouts: two columns of four with two in the middle for a
// ten, and so on.
export function pips(rank: number): [number, number][] {
  const L = 0;
  const C = 0.5;
  const R = 1;
  const col = (x: number, ys: number[]) => ys.map((y) => [x, y] as [number, number]);
  switch (rank) {
    case 1:
      return [[C, 0.5]];
    case 2:
      return col(C, [0, 1]);
    case 3:
      return col(C, [0, 0.5, 1]);
    case 4:
      return [...col(L, [0, 1]), ...col(R, [0, 1])];
    case 5:
      return [...col(L, [0, 1]), ...col(R, [0, 1]), [C, 0.5]];
    case 6:
      return [...col(L, [0, 0.5, 1]), ...col(R, [0, 0.5, 1])];
    case 7:
      return [...col(L, [0, 0.5, 1]), ...col(R, [0, 0.5, 1]), [C, 0.25]];
    case 8:
      return [...col(L, [0, 0.5, 1]), ...col(R, [0, 0.5, 1]), [C, 0.25], [C, 0.75]];
    case 9:
      return [...col(L, [0, 1 / 3, 2 / 3, 1]), ...col(R, [0, 1 / 3, 2 / 3, 1]), [C, 0.5]];
    case 10:
      return [...col(L, [0, 1 / 3, 2 / 3, 1]), ...col(R, [0, 1 / 3, 2 / 3, 1]), [C, 1 / 6], [C, 5 / 6]];
    default:
      return [];
  }
}

// A 3×5 pixel font for a card's corner index: the ranks and nothing else.
const GLYPHS: Record<string, string[]> = {
  A: [".#.", "#.#", "###", "#.#", "#.#"],
  K: ["#.#", "#.#", "##.", "#.#", "#.#"],
  Q: [".#.", "#.#", "#.#", "##.", ".##"],
  J: ["..#", "..#", "..#", "#.#", ".#."],
  "0": ["###", "#.#", "#.#", "#.#", "###"],
  "1": [".#.", "##.", ".#.", ".#.", "###"],
  "2": ["##.", "..#", ".#.", "#..", "###"],
  "3": ["##.", "..#", ".#.", "..#", "##."],
  "4": ["#.#", "#.#", "###", "..#", "..#"],
  "5": ["###", "#..", "##.", "..#", "##."],
  "6": [".##", "#..", "###", "#.#", "###"],
  "7": ["###", "..#", ".#.", ".#.", ".#."],
  "8": ["###", "#.#", "###", "#.#", "###"],
  "9": ["###", "#.#", "###", "..#", "##."],
};

// The cells of a glyph, as x, y offsets in a 3×5 box.
export function glyph(ch: string): [number, number][] {
  const out: [number, number][] = [];
  (GLYPHS[ch] ?? []).forEach((row, y) => {
    for (let x = 0; x < row.length; x++) if (row[x] === "#") out.push([x, y]);
  });
  return out;
}

// The cells of a rank's index, "10" as two glyphs side by side (1 then 0),
// packed one cell apart.
export function rankCells(rank: string): [number, number][] {
  const out: [number, number][] = [];
  [...rank].forEach((ch, i) => {
    for (const [x, y] of glyph(ch)) out.push([x + i * 4, y]);
  });
  return out;
}

// ── The default stitch art: cards and dice on the cloth ──────────────────

// Threads by role. The renderer maps each to a colour (inks.ts).
export const T = {
  Blank: -1,
  Card: 0, // the card stock: white floss
  Ink: 1, // black: clubs, spades
  Red: 2, // hearts, diamonds, a card's back
  RedDark: 3, // the back's lattice, a red die's edge
  Edge: 4, // a card's grey edge
  Shadow: 5, // a darker cloth tone under things
  DieRed: 6,
  Cream: 7, // the back's border
} as const;
export const ROLES = 8;

export interface Placed {
  cx: number; // centre, in cells
  cy: number;
  w: number; // size, in cells
  h: number;
  angle: number; // degrees
}

export interface CardArt extends Placed {
  kind: "card";
  rank: string; // "10", "A"…
  suit: Suit;
  back?: boolean;
}

export interface DieArt extends Placed {
  kind: "die";
  face: number;
  red?: boolean;
}

export type Piece = CardArt | DieArt;

// Where the cards and dice lie on a cols×rows cloth. They fall to the right
// and low, like a hand laid down at the edge of a table; a die rolls off to
// the left. Sizes follow the cloth's height so a card always reads as a
// card, never a stamp: about half the height.
export function tableLayout(cols: number, rows: number): Piece[] {
  const ch = Math.round(Math.min(rows * 0.5, cols * 0.42));
  const cw = Math.round(ch * (2.5 / 3.5));
  const die = Math.max(9, Math.round(ch * 0.27));
  const right = cols * 0.74;
  const low = rows * 0.6;
  return [
    { kind: "card", rank: "A", suit: "spade", back: true, cx: right - cw * 0.95, cy: low - ch * 0.04, w: cw, h: ch, angle: -14 },
    { kind: "card", rank: "A", suit: "spade", cx: right + cw * 0.95, cy: low + ch * 0.1, w: cw, h: ch, angle: 11 },
    { kind: "card", rank: "10", suit: "club", cx: right, cy: low - ch * 0.08, w: cw, h: ch, angle: -2 },
    { kind: "die", face: 5, cx: right - cw * 1.55, cy: low + ch * 0.55, w: die, h: die, angle: 18 },
    { kind: "die", face: 3, red: true, cx: cols * 0.17, cy: rows * 0.72, w: die, h: die, angle: -9 },
  ];
}

// Into a piece's own frame: u, v in cells from its top-left corner.
function local(p: Placed, x: number, y: number): [number, number] {
  const t = (-p.angle * Math.PI) / 180;
  const dx = x - p.cx;
  const dy = y - p.cy;
  return [dx * Math.cos(t) - dy * Math.sin(t) + p.w / 2, dx * Math.sin(t) + dy * Math.cos(t) + p.h / 2];
}

// Inside a w×h rectangle with corners of radius r.
function inRounded(u: number, v: number, w: number, h: number, r: number): boolean {
  if (u < 0 || v < 0 || u > w || v > h) return false;
  const cx = Math.min(Math.max(u, r), w - r);
  const cy = Math.min(Math.max(v, r), h - r);
  return (u - cx) ** 2 + (v - cy) ** 2 <= r * r;
}

// The thread of one cell of a card, or Blank outside it.
function cardCell(c: CardArt, u: number, v: number): number {
  const r = Math.max(1.5, c.w * 0.09);
  if (!inRounded(u, v, c.w, c.h, r)) return T.Blank;
  if (!inRounded(u - 1, v - 1, c.w - 2, c.h - 2, Math.max(0.5, r - 1))) return T.Edge;
  const red = c.suit === "heart" || c.suit === "diamond";
  if (c.back === true) {
    // A border of cream, then a lattice of diamonds in two reds.
    if (!inRounded(u - 3, v - 3, c.w - 6, c.h - 6, Math.max(0.5, r - 2))) return T.Cream;
    const a = Math.floor(u) + Math.floor(v);
    const b = Math.floor(u) - Math.floor(v);
    return ((a % 4) + 4) % 4 === 0 || ((b % 4) + 4) % 4 === 0 ? T.RedDark : T.Red;
  }
  const ink = red ? T.Red : T.Ink;
  // The corner index, top left and (turned) bottom right, with a small pip.
  const iu = Math.floor(u) - 2;
  const iv = Math.floor(v) - 2;
  const ju = Math.floor(c.w - u) - 2;
  const jv = Math.floor(c.h - v) - 2;
  const cells = rankCells(c.rank);
  if (cells.some(([x, y]) => (x === iu && y === iv) || (x === ju && y === jv))) return ink;
  const small = 2.6;
  for (const [pu, pv] of [
    [3.5, 10],
    [c.w - 3.5, c.h - 10],
  ] as const) {
    const turned = pu > c.w / 2;
    if (inSuit(c.suit, (u - pu) / small, ((v - pv) / small) * (turned ? -1 : 1))) return ink;
  }
  // The pips, in a field inset from the indexes.
  const n = c.rank === "A" ? 1 : Number(c.rank);
  const big = c.rank === "A";
  const size = big ? c.w * 0.3 : Math.max(2.6, c.w * 0.105);
  const fx0 = c.w * 0.3;
  const fx1 = c.w * 0.7;
  const fy0 = c.h * 0.17;
  const fy1 = c.h * 0.83;
  for (const [px, py] of pips(n)) {
    const cx = fx0 + (fx1 - fx0) * px;
    const cy = fy0 + (fy1 - fy0) * py;
    const turned = py > 0.5;
    if (inSuit(c.suit, (u - cx) / size, ((v - cy) / size) * (turned ? -1 : 1))) return ink;
  }
  return T.Card;
}

function dieCell(d: DieArt, u: number, v: number): number {
  const r = d.w * 0.22;
  if (!inRounded(u, v, d.w, d.h, r)) return T.Blank;
  const body = d.red === true ? T.DieRed : T.Card;
  const pip = d.red === true ? T.Card : T.Ink;
  if (!inRounded(u - 0.8, v - 0.8, d.w - 1.6, d.h - 1.6, Math.max(0.5, r - 0.8))) return d.red === true ? T.RedDark : T.Edge;
  const spots: Record<number, [number, number][]> = {
    1: [[0.5, 0.5]],
    2: [
      [0.27, 0.27],
      [0.73, 0.73],
    ],
    3: [
      [0.27, 0.27],
      [0.5, 0.5],
      [0.73, 0.73],
    ],
    4: [
      [0.27, 0.27],
      [0.73, 0.27],
      [0.27, 0.73],
      [0.73, 0.73],
    ],
    5: [
      [0.27, 0.27],
      [0.73, 0.27],
      [0.5, 0.5],
      [0.27, 0.73],
      [0.73, 0.73],
    ],
    6: [
      [0.27, 0.25],
      [0.73, 0.25],
      [0.27, 0.5],
      [0.73, 0.5],
      [0.27, 0.75],
      [0.73, 0.75],
    ],
  };
  const pr = d.w * 0.11;
  for (const [sx, sy] of spots[d.face] ?? []) if ((u - sx * d.w) ** 2 + (v - sy * d.h) ** 2 <= pr * pr) return pip;
  return body;
}

// Charts the default art: one thread role per cell (T), Blank for bare
// cloth. Later pieces lie on earlier ones, and each casts a one-cell
// shadow down and to the right in a darker cloth tone.
export function chartTable(cols: number, rows: number, pieces = tableLayout(cols, rows)): Int8Array {
  const out = new Int8Array(cols * rows).fill(T.Blank);
  for (const p of pieces) {
    const reach = Math.hypot(p.w, p.h) / 2 + 2;
    const x0 = Math.max(0, Math.floor(p.cx - reach));
    const x1 = Math.min(cols - 1, Math.ceil(p.cx + reach));
    const y0 = Math.max(0, Math.floor(p.cy - reach));
    const y1 = Math.min(rows - 1, Math.ceil(p.cy + reach));
    const at = (x: number, y: number) => {
      const [u, v] = local(p, x + 0.5, y + 0.5);
      return p.kind === "card" ? cardCell(p, u, v) : dieCell(p, u, v);
    };
    // The shadow first, where the piece does not cover.
    for (let y = y0; y <= y1; y++)
      for (let x = x0; x <= x1; x++) {
        if (at(x, y) !== T.Blank) continue;
        if (at(x - 1, y - 1) !== T.Blank && out[y * cols + x] === T.Blank) out[y * cols + x] = T.Shadow;
      }
    for (let y = y0; y <= y1; y++)
      for (let x = x0; x <= x1; x++) {
        const t = at(x, y);
        if (t !== T.Blank) out[y * cols + x] = t;
      }
  }
  return out;
}

// ── The Deck mark ────────────────────────────────────────────────────────

// The prompt (">_") as the watermark draws it, in a frame of height h: the
// chevron as a polyline and the cursor as a capsule. The proportions are
// the app icon's (scripts/generate-icon.mjs), on its 824 grid.
export function markGeometry(x: number, y: number, h: number): {
  chevron: [number, number][];
  stroke: number;
  cursor: { x: number; y: number; w: number; h: number };
} {
  const k = h / 268; // the prompt spans 278..546 on the icon's grid
  const at = (px: number, py: number): [number, number] => [x + (px - 234) * k, y + (py - 278) * k];
  const [cx, cy] = at(457, 482);
  return {
    chevron: [at(234, 278), at(404, 412), at(234, 546)],
    stroke: 76 * k,
    cursor: { x: cx, y: cy, w: 170 * k, h: 64 * k },
  };
}
