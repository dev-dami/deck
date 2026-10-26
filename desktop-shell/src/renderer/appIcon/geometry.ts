// The app icon's shapes, in pixels of the image being drawn: the tile each
// platform expects, and Deck's mark (the prompt chevron and the cursor bar)
// as three capsules. Pure, so the tests can check them without a canvas.

export type IconPlatform = "mac" | "other";

export interface Tile {
  x: number;
  y: number;
  // The tile is square: w is its side.
  w: number;
  r: number;
}

// A line from a to b with round ends, r wide on each side.
export interface Capsule {
  ax: number;
  ay: number;
  bx: number;
  by: number;
  r: number;
}

// The two arms of the chevron, then the cursor bar.
export interface Glyph {
  chevron: [Capsule, Capsule];
  cursor: Capsule;
}

// The detail an icon can carry at a size: "micro" is drawn on the pixel
// grid with a heavier mark, "small" drops textures, "full" has them all.
export type Tier = "micro" | "small" | "full";

export function tierFor(size: number): Tier {
  if (size <= 24) return "micro";
  if (size <= 64) return "small";
  return "full";
}

// macOS (Big Sur and later) draws an app icon as an 824px rounded square on
// a 1024px canvas, 100px in from each edge, and leaves the margin for the
// shadow. Windows and Linux show the image as it is, so the tile fills it.
export function tileFor(size: number, platform: IconPlatform): Tile {
  if (platform === "mac") {
    const inset = size <= 32 ? Math.round((size * 100) / 1024) : (size * 100) / 1024;
    const w = size - inset * 2;
    return { x: inset, y: inset, w, r: w * 0.225 };
  }
  // A hair of margin at large sizes, none where every pixel counts.
  const inset = size <= 48 ? 0 : size * 0.02;
  const w = size - inset * 2;
  return { x: inset, y: inset, w, r: size <= 24 ? Math.max(2, Math.round(w * 0.2)) : w * 0.2 };
}

// The mark in the units of the original icon (build/icon.png), where the
// tile is 928 wide: a chevron from (262,312) through (457,464) to
// (262,614), 84 wide, and a cursor from (551,578) to (670,578), 72 high.
const MARK = {
  a: [262, 312],
  b: [457, 464],
  c: [262, 614],
  stroke: 84,
  cursor: { x1: 551, x2: 670, y: 578, h: 72 },
} as const;
const UNIT = 928;

// The mark for a tile. At "micro" sizes it is redrawn on whole pixels with
// 45° arms and a 2px stroke at least, so a 16px icon stays a chevron and a
// bar; at "small" sizes it grows and thickens a little.
export function glyphFor(tile: Tile, size: number): Glyph {
  const tier = tierFor(size);
  if (tier === "micro") return microGlyph(tile);
  const grow = tier === "small" ? 1.06 : 1;
  const weight = tier === "small" ? 1.18 : 1;
  const u = tile.w / UNIT;
  // Grows around the centre of the tile.
  const px = (x: number) => tile.x + (UNIT / 2 + (x - UNIT / 2) * grow) * u;
  const py = (y: number) => tile.y + (UNIT / 2 + (y - UNIT / 2) * grow) * u;
  const r = (MARK.stroke / 2) * grow * weight * u;
  const cr = (MARK.cursor.h / 2) * grow * weight * u;
  const arm = (p: readonly number[], q: readonly number[]): Capsule => ({
    ax: px(p[0] as number),
    ay: py(p[1] as number),
    bx: px(q[0] as number),
    by: py(q[1] as number),
    r,
  });
  return {
    chevron: [arm(MARK.a, MARK.b), arm(MARK.b, MARK.c)],
    cursor: { ax: px(MARK.cursor.x1), ay: py(MARK.cursor.y), bx: px(MARK.cursor.x2), by: py(MARK.cursor.y), r: cr },
  };
}

// A 16 to 24px mark. Strokes sit on half pixels when they are an odd
// number of pixels wide, on whole pixels when even, so their edges are
// sharp.
function microGlyph(tile: Tile): Glyph {
  const w = tile.w;
  const stroke = Math.max(2, Math.round(w * 0.15));
  const r = stroke / 2;
  const snap = (v: number) => (stroke % 2 === 0 ? Math.round(v) : Math.floor(v) + 0.5);
  // The chevron's arms run at 45°: half its height is its depth.
  const half = Math.max(3, Math.round(w * 0.22));
  const cx = snap(tile.x + w * 0.27 + r);
  const cy = tile.y + w / 2;
  const tipX = cx + half;
  const top = snap(cy - half);
  const bottom = top + half * 2;
  const mid = top + half;
  // The cursor: a short bar low on the right, a pixel or more past the tip.
  const barY = snap(tile.y + w * 0.66);
  const barX1 = tipX + Math.max(1, Math.round(w * 0.1)) + r;
  const barX2 = Math.min(tile.x + w - Math.max(2, Math.round(w * 0.16)) - r, barX1 + Math.max(2, Math.round(w * 0.16)));
  return {
    chevron: [
      { ax: cx, ay: top, bx: tipX, by: mid, r },
      { ax: tipX, ay: mid, bx: cx, by: bottom, r },
    ],
    cursor: { ax: barX1, ay: barY, bx: barX2, by: barY, r: r * 0.9 },
  };
}

// The signed distance from (x, y) to a capsule: negative inside.
export function capsuleDistance(c: Capsule, x: number, y: number): number {
  const dx = c.bx - c.ax;
  const dy = c.by - c.ay;
  const len2 = dx * dx + dy * dy;
  const t = len2 === 0 ? 0 : Math.max(0, Math.min(1, ((x - c.ax) * dx + (y - c.ay) * dy) / len2));
  return Math.hypot(x - (c.ax + t * dx), y - (c.ay + t * dy)) - c.r;
}

// Which part of the mark covers (x, y), if any, with margin added around it.
export function partAt(g: Glyph, x: number, y: number, margin = 0): "chevron" | "cursor" | null {
  if (Math.min(capsuleDistance(g.chevron[0], x, y), capsuleDistance(g.chevron[1], x, y)) < margin) return "chevron";
  if (capsuleDistance(g.cursor, x, y) < margin) return "cursor";
  return null;
}

// The box around the mark.
export function glyphBounds(g: Glyph): { x1: number; y1: number; x2: number; y2: number } {
  const caps = [...g.chevron, g.cursor];
  return {
    x1: Math.min(...caps.map((c) => Math.min(c.ax, c.bx) - c.r)),
    y1: Math.min(...caps.map((c) => Math.min(c.ay, c.by) - c.r)),
    x2: Math.max(...caps.map((c) => Math.max(c.ax, c.bx) + c.r)),
    y2: Math.max(...caps.map((c) => Math.max(c.ay, c.by) + c.r)),
  };
}

// Whether (x, y) lies on the tile, a rounded square.
export function insideTile(t: Tile, x: number, y: number): boolean {
  const cx = Math.max(t.x + t.r, Math.min(t.x + t.w - t.r, x));
  const cy = Math.max(t.y + t.r, Math.min(t.y + t.w - t.r, y));
  return Math.hypot(x - cx, y - cy) <= t.r;
}

// The centres of a halftone screen over a box: a grid of pitch `cell`,
// turned by `angle` degrees, as print screens are.
export function screenPoints(
  box: { x1: number; y1: number; x2: number; y2: number },
  cell: number,
  angle: number,
): { x: number; y: number }[] {
  const rad = (angle * Math.PI) / 180;
  const cos = Math.cos(rad);
  const sin = Math.sin(rad);
  const cx = (box.x1 + box.x2) / 2;
  const cy = (box.y1 + box.y2) / 2;
  const reach = Math.hypot(box.x2 - box.x1, box.y2 - box.y1) / 2 + cell;
  const n = Math.ceil(reach / cell);
  const out: { x: number; y: number }[] = [];
  for (let i = -n; i <= n; i++) {
    for (let j = -n; j <= n; j++) {
      const x = cx + (i * cos - j * sin) * cell;
      const y = cy + (i * sin + j * cos) * cell;
      if (x >= box.x1 - cell && x <= box.x2 + cell && y >= box.y1 - cell && y <= box.y2 + cell) out.push({ x, y });
    }
  }
  return out;
}

// The radius of a halftone dot that inks `tone` (0 to 1) of its cell. Past
// about 78% the dots touch and the ink closes up, as on paper.
export function dotRadius(tone: number, cell: number): number {
  const t = Math.max(0, Math.min(1, tone));
  return cell * Math.sqrt(t / Math.PI) * (t > 0.78 ? 1 + (t - 0.78) * 0.9 : 1);
}

// The cross-stitch grid: 25 cells across the tile at 256px and up, 19 at
// 128px, where finer stitches would blur.
export const stitchGrid = (size: number): number => (size >= 256 ? 25 : 19);

export interface StitchCell {
  col: number;
  row: number;
  part: "chevron" | "cursor";
}

// The cross-stitch chart of the mark: every cell of an n × n grid over the
// tile whose centre the mark covers, or nearly covers, gets a stitch, so
// the mark comes out in the steps a stitcher would count.
export function stitchCells(tile: Tile, g: Glyph, n: number): StitchCell[] {
  const cell = tile.w / n;
  const out: StitchCell[] = [];
  for (let row = 0; row < n; row++) {
    for (let col = 0; col < n; col++) {
      const part = partAt(g, tile.x + (col + 0.5) * cell, tile.y + (row + 0.5) * cell, cell * 0.12);
      if (part !== null) out.push({ col, row, part });
    }
  }
  return out;
}

// A small, seeded random source, so an icon draws the same every time.
export function seeded(seed: number): () => number {
  let a = seed >>> 0;
  return () => {
    a = (a + 0x6d2b79f5) >>> 0;
    let t = a;
    t = Math.imul(t ^ (t >>> 15), t | 1);
    t ^= t + Math.imul(t ^ (t >>> 7), t | 61);
    return ((t ^ (t >>> 14)) >>> 0) / 4294967296;
  };
}
