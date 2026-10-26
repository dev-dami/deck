import { describe, expect, test } from "vitest";
import {
  capsuleDistance,
  dotRadius,
  glyphBounds,
  glyphFor,
  insideTile,
  partAt,
  screenPoints,
  seeded,
  stitchCells,
  stitchGrid,
  tierFor,
  tileFor,
} from "./geometry";

const SIZES = [16, 24, 32, 48, 64, 128, 256, 512, 1024];

describe("the tile", () => {
  test("a Mac's sits 100/1024 in from each edge, as the system's icons do", () => {
    const t = tileFor(1024, "mac");
    expect(t).toMatchObject({ x: 100, y: 100, w: 824 });
    expect(t.r).toBeCloseTo(824 * 0.225);
    // Small, it lands on whole pixels.
    expect(Number.isInteger(tileFor(32, "mac").x)).toBe(true);
  });

  test("elsewhere it fills the image, with a hair of margin when large", () => {
    expect(tileFor(32, "other")).toMatchObject({ x: 0, y: 0, w: 32 });
    expect(tileFor(16, "other").r).toBeGreaterThanOrEqual(2);
    const t = tileFor(256, "other");
    expect(t.x).toBeGreaterThan(0);
    expect(t.x * 2 + t.w).toBeCloseTo(256);
  });

  test("insideTile knows its rounded corners", () => {
    const t = tileFor(100, "other");
    expect(insideTile(t, 50, 50)).toBe(true);
    expect(insideTile(t, t.x + 0.5, t.y + 0.5)).toBe(false);
    expect(insideTile(t, t.x + t.r, t.y + 0.5)).toBe(true);
  });
});

describe("the mark", () => {
  test("tiers: micro on the pixel grid, small without textures, full", () => {
    expect([16, 24, 32, 64, 65, 512].map(tierFor)).toEqual(["micro", "micro", "small", "small", "full", "full"]);
  });

  test("stays on the tile, with room around it, at every size and on both platforms", () => {
    for (const platform of ["mac", "other"] as const) {
      for (const size of SIZES) {
        const t = tileFor(size, platform);
        const b = glyphBounds(glyphFor(t, size));
        const where = `${platform} ${size}`;
        expect(b.x1, where).toBeGreaterThan(t.x);
        expect(b.y1, where).toBeGreaterThan(t.y);
        expect(b.x2, where).toBeLessThan(t.x + t.w);
        expect(b.y2, where).toBeLessThan(t.y + t.w);
        // Centred on the tile, give or take a pixel or a tenth of it.
        const slack = Math.max(1.5, t.w * 0.1);
        expect(Math.abs((b.y1 + b.y2) / 2 - (t.y + t.w / 2)), where).toBeLessThan(slack);
      }
    }
  });

  test("the large mark keeps the shipped icon's proportions", () => {
    const t = tileFor(1024, "other");
    const g = glyphFor(t, 1024);
    const u = t.w / 928;
    expect(g.chevron[0].r).toBeCloseTo(42 * u);
    expect(g.cursor.r).toBeCloseTo(36 * u);
    expect(g.chevron[0].bx).toBe(g.chevron[1].ax);
    expect(g.chevron[0].by).toBe(g.chevron[1].ay);
  });

  test("at 16 to 24px the arms run at 45° and the strokes have sharp edges", () => {
    for (const size of [16, 20, 24]) {
      const t = tileFor(size, "other");
      const g = glyphFor(t, size);
      const [a, b] = g.chevron;
      expect(Math.abs(a.bx - a.ax), `${size}`).toBe(Math.abs(a.by - a.ay));
      expect(Math.abs(b.bx - b.ax)).toBe(Math.abs(b.by - b.ay));
      const stroke = a.r * 2;
      expect(stroke).toBeGreaterThanOrEqual(2);
      expect(Number.isInteger(stroke)).toBe(true);
      // An even stroke centres on a pixel edge, an odd one on a pixel centre.
      const frac = (v: number) => v - Math.floor(v);
      expect(frac(a.ay)).toBe(stroke % 2 === 0 ? 0 : 0.5);
      expect(frac(g.cursor.ay)).toBe(stroke % 2 === 0 ? 0 : 0.5);
      // The bar starts past the chevron's tip, with a gap.
      expect(g.cursor.ax - g.cursor.r).toBeGreaterThan(a.bx + a.r);
    }
  });

  test("partAt and capsuleDistance tell the chevron from the bar", () => {
    const t = tileFor(512, "other");
    const g = glyphFor(t, 512);
    expect(partAt(g, g.chevron[0].bx, g.chevron[0].by)).toBe("chevron");
    expect(partAt(g, (g.cursor.ax + g.cursor.bx) / 2, g.cursor.ay)).toBe("cursor");
    expect(partAt(g, t.x + 4, t.y + 4)).toBeNull();
    expect(capsuleDistance(g.cursor, g.cursor.ax, g.cursor.ay)).toBeCloseTo(-g.cursor.r);
    expect(capsuleDistance({ ax: 0, ay: 0, bx: 0, by: 0, r: 1 }, 3, 4)).toBeCloseTo(4);
  });
});

describe("textures", () => {
  test("the halftone screen covers its box, turned", () => {
    const box = { x1: 0, y1: 0, x2: 100, y2: 50 };
    const pts = screenPoints(box, 5, 45);
    expect(pts.length).toBeGreaterThan((100 * 50) / 25 - 10);
    for (const p of pts) {
      expect(p.x).toBeGreaterThanOrEqual(-5);
      expect(p.x).toBeLessThanOrEqual(105);
    }
    // Every part of the box has a dot within a cell of it.
    for (const [x, y] of [
      [0, 0],
      [100, 50],
      [50, 25],
    ] as const) {
      expect(Math.min(...pts.map((p) => Math.hypot(p.x - x, p.y - y)))).toBeLessThan(5);
    }
  });

  test("a halftone dot grows with the tone and closes up past 78%", () => {
    expect(dotRadius(0, 10)).toBe(0);
    expect(dotRadius(-1, 10)).toBe(0);
    const rs = [0.1, 0.3, 0.5, 0.78, 0.9, 1].map((t) => dotRadius(t, 10));
    for (let i = 1; i < rs.length; i++) expect(rs[i]).toBeGreaterThan(rs[i - 1] as number);
    // At full tone, neighbouring dots overlap.
    expect(dotRadius(1, 10) * 2).toBeGreaterThan(10);
    expect(dotRadius(2, 10)).toBe(dotRadius(1, 10));
  });

  test("the cross-stitch chart covers the mark in whole cells, inside the stitched edge", () => {
    expect([128, 255, 256, 1024].map(stitchGrid)).toEqual([19, 19, 25, 25]);
    for (const size of [128, 256, 1024]) {
      const t = tileFor(size, "mac");
      const g = glyphFor(t, size);
      const n = stitchGrid(size);
      const cell = t.w / n;
      const cells = stitchCells(t, g, n);
      const key = (c: { col: number; row: number }) => `${c.col},${c.row}`;
      expect(new Set(cells.map(key)).size).toBe(cells.length);
      // Clear of the running stitch, 6% in from the edge.
      for (const c of cells) {
        expect(c.col).toBeGreaterThanOrEqual(2);
        expect(c.row).toBeGreaterThanOrEqual(2);
        expect(c.col).toBeLessThan(n - 2);
        expect(c.row).toBeLessThan(n - 2);
      }
      // Every cell's centre is on the mark or within an eighth of a cell.
      for (const c of cells) {
        expect(partAt(g, t.x + (c.col + 0.5) * cell, t.y + (c.row + 0.5) * cell, cell * 0.13)).toBe(c.part);
      }
      const chevron = cells.filter((c) => c.part === "chevron");
      const cursor = cells.filter((c) => c.part === "cursor");
      expect(chevron.length).toBeGreaterThan(cursor.length);
      expect(cursor.length).toBeGreaterThanOrEqual(4);
      // The chevron's tip is its rightmost column, at its middle row.
      const tipCol = Math.max(...chevron.map((c) => c.col));
      const rows = chevron.map((c) => c.row);
      const midRow = (Math.min(...rows) + Math.max(...rows)) / 2;
      expect(chevron.filter((c) => c.col === tipCol).every((c) => Math.abs(c.row - midRow) <= 1)).toBe(true);
    }
  });

  test("the seeded source draws the same numbers every time, in [0, 1)", () => {
    const a = seeded(7);
    const b = seeded(7);
    const xs = Array.from({ length: 50 }, () => a());
    expect(xs).toEqual(Array.from({ length: 50 }, () => b()));
    expect(xs.every((x) => x >= 0 && x < 1)).toBe(true);
    expect(seeded(8)()).not.toBe(xs[0]);
  });
});
