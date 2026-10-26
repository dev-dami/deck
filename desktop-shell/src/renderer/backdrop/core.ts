// The backdrop's maths, with no DOM: colour, noise, fitting a picture, the
// dither, the halftone screen, the riso's blocks, thread quantisation and
// the stitch chart. The canvas renderers (scene.ts, halftone.ts,
// stitch.ts) only draw what these return, so everything here is tested in
// core.test.ts.

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

// Perceived lightness, 0..1: the luma of the gamma-encoded values, as a
// densitometer reads a proof.
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
export function fbm(x: number, y: number, seed = 0, octaves = 3): number {
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

export interface Rect {
  x: number;
  y: number;
  w: number;
  h: number;
}

// Where an sw×sh picture lands in a dw×dh frame: "cover" fills the frame,
// "contain" fits inside it. ax and ay (0..1) say which part of the slack
// goes left or above, so 0.5 centres and 1 pushes to the right or bottom;
// zoom scales past the fit.
export function place(sw: number, sh: number, dw: number, dh: number, fit: "cover" | "contain", ax = 0.5, ay = 0.5, zoom = 1): Rect {
  const s = (fit === "cover" ? Math.max(dw / sw, dh / sh) : Math.min(dw / sw, dh / sh)) * zoom;
  const w = sw * s;
  const h = sh * s;
  return { x: (dw - w) * ax, y: (dh - h) * ay, w, h };
}

// The luma of RGBA pixels, 0..1, one value a pixel.
export function luma(data: ArrayLike<number>): Float32Array {
  const n = Math.floor(data.length / 4);
  const out = new Float32Array(n);
  for (let i = 0; i < n; i++) {
    out[i] = (0.299 * (data[i * 4] ?? 0) + 0.587 * (data[i * 4 + 1] ?? 0) + 0.114 * (data[i * 4 + 2] ?? 0)) / 255;
  }
  return out;
}

// Reads a one-channel w×h grid at a fractional point, bilinearly, clamped
// to its edges; a pixel's value sits at its centre.
export function sampleGrey(v: ArrayLike<number>, w: number, h: number, x: number, y: number): number {
  const fx = Math.max(0, Math.min(w - 1, x - 0.5));
  const fy = Math.max(0, Math.min(h - 1, y - 0.5));
  const x0 = Math.floor(fx);
  const y0 = Math.floor(fy);
  const x1 = Math.min(w - 1, x0 + 1);
  const y1 = Math.min(h - 1, y0 + 1);
  const tx = fx - x0;
  const ty = fy - y0;
  const at = (xx: number, yy: number) => v[yy * w + xx] ?? 0;
  return (at(x0, y0) * (1 - tx) + at(x1, y0) * tx) * (1 - ty) + (at(x0, y1) * (1 - tx) + at(x1, y1) * tx) * ty;
}

// The tones at the lo and hi fractions of a picture's histogram: its black
// and white points, so a faded albumen print fills the whole range.
export function autoLevels(v: Float32Array, lo = 0.01, hi = 0.99): [number, number] {
  const bins = new Uint32Array(256);
  for (let i = 0; i < v.length; i++) {
    const b = ((v[i] as number) * 255 + 0.5) | 0;
    bins[b < 0 ? 0 : b > 255 ? 255 : b]! += 1;
  }
  const at = (q: number) => {
    const target = q * v.length;
    let sum = 0;
    for (let i = 0; i < 256; i++) {
      sum += bins[i] ?? 0;
      if (sum >= target) return i / 255;
    }
    return 1;
  };
  const black = at(lo);
  const white = at(hi);
  return white - black < 0.05 ? [0, 1] : [black, white];
}

// Stretches the tones between black and white to 0..1, then bends the mid
// tones by gamma (below 1 lightens, above 1 darkens). In place.
export function levels(v: Float32Array, black: number, white: number, gamma = 1): Float32Array {
  const span = Math.max(1e-6, white - black);
  // A table of the curve, so the power is taken 1024 times, not once a pixel.
  const curve = new Float32Array(1025);
  for (let k = 0; k <= 1024; k++) curve[k] = clamp01((k / 1024 - black) / span) ** gamma;
  for (let i = 0; i < v.length; i++) {
    const k = ((v[i] as number) * 1024 + 0.5) | 0;
    v[i] = curve[k < 0 ? 0 : k > 1024 ? 1024 : k] as number;
  }
  return v;
}

// ── Dither ───────────────────────────────────────────────────────────────

// Bill Atkinson's error diffusion, as MacPaint printed: each pixel goes to
// paper (1) or ink (0) and passes three quarters of its error to six
// neighbours. Losing a quarter keeps the highlights and shadows clean,
// which is what makes it read as a print and not as noise.
export function atkinson(v: Float32Array, w: number, h: number, threshold = 0.5): Uint8Array {
  // The error, in a buffer with a margin of two columns each side and two
  // rows below, so the six pushes need no bounds checks.
  const W = w + 4;
  const e = new Float32Array(W * (h + 2));
  for (let y = 0; y < h; y++) e.set(v.subarray(y * w, y * w + w), y * W + 2);
  const out = new Uint8Array(w * h);
  for (let y = 0; y < h; y++) {
    let i = y * W + 2;
    let o = y * w;
    for (let x = 0; x < w; x++, i++, o++) {
      const old = e[i] as number;
      const on = old >= threshold ? 1 : 0;
      out[o] = on;
      const d = (old - on) * 0.125;
      e[i + 1]! += d;
      e[i + 2]! += d;
      e[i + W - 1]! += d;
      e[i + W]! += d;
      e[i + W + 1]! += d;
      e[i + 2 * W]! += d;
    }
  }
  return out;
}

// The same diffusion in colour: each pixel takes the nearest of a few inks
// and the error of each channel spreads as above. Out: an ink index a
// pixel. rgb holds three values a pixel, 0..255.
export function atkinsonPalette(rgb: Float32Array, w: number, h: number, palette: readonly RGB[]): Uint8Array {
  const W = w + 4;
  const e = new Float32Array(W * (h + 2) * 3);
  for (let y = 0; y < h; y++) e.set(rgb.subarray(y * w * 3, (y * w + w) * 3), (y * W + 2) * 3);
  const pr = palette.map((p) => p[0]);
  const pg = palette.map((p) => p[1]);
  const pb = palette.map((p) => p[2]);
  const n = palette.length;
  const out = new Uint8Array(w * h);
  const offs = [3, 6, (W - 1) * 3, W * 3, (W + 1) * 3, 2 * W * 3];
  for (let y = 0; y < h; y++) {
    let i = (y * W + 2) * 3;
    let o = y * w;
    for (let x = 0; x < w; x++, i += 3, o++) {
      const r = e[i] as number;
      const g = e[i + 1] as number;
      const b = e[i + 2] as number;
      // Redmean, inline: the nearest ink.
      let best = 0;
      let bd = Infinity;
      for (let k = 0; k < n; k++) {
        const rm = (r + (pr[k] as number)) * 0.5;
        const dr = r - (pr[k] as number);
        const dg = g - (pg[k] as number);
        const db = b - (pb[k] as number);
        const d = (2 + rm / 256) * dr * dr + 4 * dg * dg + (2 + (255 - rm) / 256) * db * db;
        if (d < bd) {
          bd = d;
          best = k;
        }
      }
      out[o] = best;
      const er = (r - (pr[best] as number)) * 0.125;
      const eg = (g - (pg[best] as number)) * 0.125;
      const eb = (b - (pb[best] as number)) * 0.125;
      for (let k = 0; k < 6; k++) {
        const j = i + (offs[k] as number);
        e[j]! += er;
        e[j + 1]! += eg;
        e[j + 2]! += eb;
      }
    }
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
  let umin = Infinity;
  let umax = -Infinity;
  let vmin = Infinity;
  let vmax = -Infinity;
  for (const [x, y] of [
    [0, 0],
    [w, 0],
    [0, h],
    [w, h],
  ] as const) {
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

// The riso's hard shapes of solid spot ink, in a W×H frame: a disc behind
// the subject's head (the upper third of its box), and a bar along the
// foot from under the disc to the margin, as a zine pastes blocks of
// colour over a print. They follow the subject, which sits right of the
// sidebar, so the left of the frame stays stock.
export type Block = { kind: "disc"; x: number; y: number; r: number } | { kind: "bar"; x: number; y: number; w: number; h: number };

export function risoBlocks(W: number, H: number, subject: Rect, margin = 0): Block[] {
  const x = subject.x + subject.w / 2;
  const y = subject.y + subject.h * 0.32;
  // Whole inside the margin: a disc the press cuts reads as a misprint.
  const inset = margin + Math.min(W, H) * 0.02;
  const r = Math.max(0, Math.min(subject.h * 0.36, subject.w * 0.5, W * 0.24, y - inset, H - inset - y, W - inset - x));
  const h = Math.max(8, H * 0.028);
  const by = Math.min(H * 0.9, H - margin - h * 2);
  const bx = Math.max(margin, x - r * 1.15);
  const blocks: Block[] = [{ kind: "disc", x, y, r }];
  if (by > y + r * 0.6 && W - margin - bx > r) blocks.push({ kind: "bar", x: bx, y: by, w: W - margin - bx, h });
  return blocks;
}

export function inBlock(b: Block, x: number, y: number): boolean {
  return b.kind === "disc" ? (x - b.x) ** 2 + (y - b.y) ** 2 <= b.r * b.r : x >= b.x && x <= b.x + b.w && y >= b.y && y <= b.y + b.h;
}

// ── Threads ──────────────────────────────────────────────────────────────

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

// Charts a picture for cross-stitch, one cell a pixel, by tone: the
// lightest tones stay bare cloth (-1), the rest take one of `threads`
// shades from dark (0) to light. `bare` is the tone above which the cloth
// shows; on dark cloth (`dark`) it is the darkest tones that stay bare.
export function chartTones(v: Float32Array, threads: number, bare: number, dark: boolean): Int8Array {
  const out = new Int8Array(v.length);
  for (let i = 0; i < v.length; i++) {
    const t = v[i] ?? 0;
    if (dark ? t < bare : t > bare) {
      out[i] = -1;
      continue;
    }
    const k = dark ? (t - bare) / (1 - bare) : t / bare;
    out[i] = Math.min(threads - 1, Math.max(0, Math.floor(clamp01(k) * threads)));
  }
  return out;
}

// How many cells of `cell` px cover a w×h frame, and the offset that
// centres the grid, so the fabric's holes fall evenly at both edges.
export function grid(w: number, h: number, cell: number): { cols: number; rows: number; ox: number; oy: number } {
  const cols = Math.ceil(w / cell);
  const rows = Math.ceil(h / cell);
  return { cols, rows, ox: (w - cols * cell) / 2, oy: (h - rows * cell) / 2 };
}

// ── The stitch's own art: cards and dice ─────────────────────────────────

export type Suit = "club" | "spade" | "heart" | "diamond";

// Suit shapes in a unit box, u and v in -1..1 (v down). True inside.
export function inSuit(suit: Suit, u: number, v: number): boolean {
  const circle = (cx: number, cy: number, r: number) => (u - cx) ** 2 + (v - cy) ** 2 <= r * r;
  const stem = () => v > 0.15 && v <= 1 && Math.abs(u) <= 0.12 + (v - 0.15) * 0.45;
  switch (suit) {
    case "club":
      return circle(0, -0.5, 0.42) || circle(-0.5, 0.12, 0.42) || circle(0.5, 0.12, 0.42) || circle(0, 0.05, 0.2) || stem();
    case "spade":
      // A heart upside down, with a stem.
      return inSuit("heart", u, -v * 1.05 + 0.18) || stem();
    case "heart":
      if (v < -0.2) return circle(-0.45, -0.38, 0.5) || circle(0.45, -0.38, 0.5);
      return Math.abs(u) <= (0.93 * (0.95 - v)) / 1.15 && v <= 0.95;
    case "diamond":
      return Math.abs(u) / 0.78 + Math.abs(v) <= 1;
  }
}

// The pips of a card's face, as centres in 0..1 of the pip field (x, y),
// in the classic layouts.
export function pips(rank: number): [number, number][] {
  const col = (x: number, ys: number[]) => ys.map((y) => [x, y] as [number, number]);
  switch (rank) {
    case 1:
      return [[0.5, 0.5]];
    case 2:
      return col(0.5, [0, 1]);
    case 3:
      return col(0.5, [0, 0.5, 1]);
    case 4:
      return [...col(0, [0, 1]), ...col(1, [0, 1])];
    case 5:
      return [...col(0, [0, 1]), ...col(1, [0, 1]), [0.5, 0.5]];
    case 6:
      return [...col(0, [0, 0.5, 1]), ...col(1, [0, 0.5, 1])];
    case 7:
      return [...col(0, [0, 0.5, 1]), ...col(1, [0, 0.5, 1]), [0.5, 0.25]];
    case 8:
      return [...col(0, [0, 0.5, 1]), ...col(1, [0, 0.5, 1]), [0.5, 0.25], [0.5, 0.75]];
    case 9:
      return [...col(0, [0, 1 / 3, 2 / 3, 1]), ...col(1, [0, 1 / 3, 2 / 3, 1]), [0.5, 0.5]];
    case 10:
      return [...col(0, [0, 1 / 3, 2 / 3, 1]), ...col(1, [0, 1 / 3, 2 / 3, 1]), [0.5, 1 / 6], [0.5, 5 / 6]];
    default:
      return [];
  }
}

// A 5×7 pixel font for a card's corner index: the ranks and nothing else.
const GLYPHS: Record<string, string[]> = {
  A: [".###.", "#...#", "#...#", "#####", "#...#", "#...#", "#...#"],
  K: ["#...#", "#..#.", "#.#..", "##...", "#.#..", "#..#.", "#...#"],
  Q: [".###.", "#...#", "#...#", "#...#", "#.#.#", "#..#.", ".##.#"],
  J: ["..###", "...#.", "...#.", "...#.", "#..#.", "#..#.", ".##.."],
  "0": [".###.", "#...#", "#..##", "#.#.#", "##..#", "#...#", ".###."],
  "1": ["..#..", ".##..", "..#..", "..#..", "..#..", "..#..", ".###."],
  "2": [".###.", "#...#", "....#", "...#.", "..#..", ".#...", "#####"],
  "3": ["#####", "...#.", "..#..", "...#.", "....#", "#...#", ".###."],
  "4": ["...#.", "..##.", ".#.#.", "#..#.", "#####", "...#.", "...#."],
  "5": ["#####", "#....", "####.", "....#", "....#", "#...#", ".###."],
  "6": ["..##.", ".#...", "#....", "####.", "#...#", "#...#", ".###."],
  "7": ["#####", "....#", "...#.", "..#..", ".#...", ".#...", ".#..."],
  "8": [".###.", "#...#", "#...#", ".###.", "#...#", "#...#", ".###."],
  "9": [".###.", "#...#", "#...#", ".####", "....#", "...#.", ".##.."],
};

// The cells of a rank's index, "10" as two glyphs side by side, one cell
// apart, as x, y offsets.
export function rankCells(rank: string): [number, number][] {
  const out: [number, number][] = [];
  [...rank].forEach((ch, i) => {
    (GLYPHS[ch] ?? []).forEach((row, y) => {
      for (let x = 0; x < row.length; x++) if (row[x] === "#") out.push([x + i * 6, y]);
    });
  });
  return out;
}

// Threads by role. The renderer maps each to a floss (inks.ts).
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
  rank: string;
  suit: Suit;
  back?: boolean;
}

export interface DieArt extends Placed {
  kind: "die";
  face: number;
  red?: boolean;
}

export type Piece = CardArt | DieArt;

// Where the cards and dice lie on a cols×rows cloth: a hand of three
// fanned from one pivot low on the right, as it is held, and a pair of
// dice thrown at its foot. Sizes follow the cloth, so a card always reads
// as a card, never a stamp.
// `clear` is a band of cells on the left kept bare, where the sidebar is.
export function tableLayout(cols: number, rows: number, clear = 0): Piece[] {
  // As tall as the cloth allows, up to the 36 cells a face reads at, and
  // narrow enough that the hand and the dice fit across.
  const ch = Math.round(Math.min(cols * 0.3, rows * 0.8, Math.max(rows * 0.4, 36)));
  const cw = Math.round(ch * (2.5 / 3.5));
  const die = Math.max(8, Math.round(ch * 0.27));
  const px = cols - cw * 1.75;
  const py = rows * 0.58 + ch * 0.95;
  const fan = (angle: number): Placed => {
    const t = (angle * Math.PI) / 180;
    return { cx: px + Math.sin(t) * ch * 0.95, cy: py - Math.cos(t) * ch * 0.95, w: cw, h: ch, angle };
  };
  return [
    { kind: "card", rank: "A", suit: "spade", back: true, ...fan(-26) },
    { kind: "card", rank: "A", suit: "spade", ...fan(-6) },
    { kind: "card", rank: "A", suit: "heart", ...fan(14) },
    // Close by the hand, and never in the band kept bare.
    { kind: "die", face: 5, cx: Math.max(px - cw * 1.85, clear + die), cy: Math.min(rows * 0.84, rows - die * 1.7), w: die, h: die, angle: 14 },
    {
      kind: "die",
      face: 3,
      red: true,
      cx: Math.max(px - cw * 1.15, clear + die * 2.2),
      cy: Math.min(rows * 0.9, rows - die * 0.85),
      w: die,
      h: die,
      angle: -10,
    },
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

// A card's face, worked out once: its corner index's cells (as x * 16 + y)
// and its pips' centres, in cells.
interface Face {
  idx: Set<number>;
  pips: { x: number; y: number; turned: boolean }[];
  size: number;
}

function faceOf(c: CardArt): Face {
  const ace = c.rank === "A";
  const fx0 = c.w * 0.3;
  const fx1 = c.w * 0.7;
  const fy0 = c.h * 0.17;
  const fy1 = c.h * 0.83;
  return {
    idx: new Set(rankCells(c.rank).map(([x, y]) => x * 16 + y)),
    pips: pips(ace ? 1 : Number(c.rank)).map(([px, py]) => ({ x: fx0 + (fx1 - fx0) * px, y: fy0 + (fy1 - fy0) * py, turned: py > 0.5 })),
    size: ace ? c.w * 0.3 : Math.max(2.6, c.w * 0.105),
  };
}

function cardCell(c: CardArt, face: Face, u: number, v: number): number {
  const r = Math.max(1.5, c.w * 0.09);
  if (!inRounded(u, v, c.w, c.h, r)) return T.Blank;
  if (!inRounded(u - 1, v - 1, c.w - 2, c.h - 2, Math.max(0.5, r - 1))) return T.Edge;
  if (c.back === true) {
    // A border of cream, then a lattice of diamonds in two reds.
    if (!inRounded(u - 3, v - 3, c.w - 6, c.h - 6, Math.max(0.5, r - 2))) return T.Cream;
    const a = Math.floor(u) + Math.floor(v);
    const b = Math.floor(u) - Math.floor(v);
    return ((a % 4) + 4) % 4 === 0 || ((b % 4) + 4) % 4 === 0 ? T.RedDark : T.Red;
  }
  const ink = c.suit === "heart" || c.suit === "diamond" ? T.Red : T.Ink;
  // The corner index, top left and (turned) bottom right, each over a
  // small pip.
  const iu = Math.floor(u) - 2;
  const iv = Math.floor(v) - 2;
  const ju = Math.floor(c.w - u) - 2;
  const jv = Math.floor(c.h - v) - 2;
  if ((iu >= 0 && iv >= 0 && face.idx.has(iu * 16 + iv)) || (ju >= 0 && jv >= 0 && face.idx.has(ju * 16 + jv))) return ink;
  const small = 2.4;
  for (const [pu, pv, turned] of [
    [4.5, 12.5, false],
    [c.w - 4.5, c.h - 12.5, true],
  ] as const) {
    if (inSuit(c.suit, (u - pu) / small, ((v - pv) / small) * (turned ? -1 : 1))) return ink;
  }
  // The pips, in a field inset from the indexes.
  const k = face.size;
  for (const p of face.pips) {
    if (Math.abs(u - p.x) > k || Math.abs(v - p.y) > k) continue;
    if (inSuit(c.suit, (u - p.x) / k, ((v - p.y) / k) * (p.turned ? -1 : 1))) return ink;
  }
  return T.Card;
}

const SPOTS: Record<number, [number, number][]> = {
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

function dieCell(d: DieArt, u: number, v: number): number {
  const r = d.w * 0.22;
  if (!inRounded(u, v, d.w, d.h, r)) return T.Blank;
  const red = d.red === true;
  if (!inRounded(u - 0.8, v - 0.8, d.w - 1.6, d.h - 1.6, Math.max(0.5, r - 0.8))) return red ? T.RedDark : T.Edge;
  const pr = d.w * 0.11;
  for (const [sx, sy] of SPOTS[d.face] ?? []) if ((u - sx * d.w) ** 2 + (v - sy * d.h) ** 2 <= pr * pr) return red ? T.Card : T.Ink;
  return red ? T.DieRed : T.Card;
}

// Charts the cards and dice: one thread role per cell (T), Blank for bare
// cloth. Later pieces lie on earlier ones, and each casts a one-cell
// shadow down and to the right.
export function chartTable(cols: number, rows: number, pieces = tableLayout(cols, rows)): Int8Array {
  const out = new Int8Array(cols * rows).fill(T.Blank);
  for (const p of pieces) {
    const reach = Math.hypot(p.w, p.h) / 2 + 2;
    const x0 = Math.max(0, Math.floor(p.cx - reach));
    const x1 = Math.min(cols - 1, Math.ceil(p.cx + reach));
    const y0 = Math.max(0, Math.floor(p.cy - reach));
    const y1 = Math.min(rows - 1, Math.ceil(p.cy + reach));
    const face = p.kind === "card" ? faceOf(p) : null;
    // The piece over its box, one cell wider up and left for the shadow.
    const bw = x1 - x0 + 2;
    const box = new Int8Array(bw * (y1 - y0 + 2));
    for (let y = y0 - 1; y <= y1; y++)
      for (let x = x0 - 1; x <= x1; x++) {
        const [u, v] = local(p, x + 0.5, y + 0.5);
        box[(y - y0 + 1) * bw + (x - x0 + 1)] = p.kind === "card" && face !== null ? cardCell(p, face, u, v) : dieCell(p as DieArt, u, v);
      }
    for (let y = y0; y <= y1; y++)
      for (let x = x0; x <= x1; x++) {
        const t = box[(y - y0 + 1) * bw + (x - x0 + 1)] as number;
        if (t !== T.Blank) out[y * cols + x] = t;
        else if (box[(y - y0) * bw + (x - x0)] !== T.Blank && out[y * cols + x] === T.Blank) out[y * cols + x] = T.Shadow;
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
  cursor: Rect;
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
