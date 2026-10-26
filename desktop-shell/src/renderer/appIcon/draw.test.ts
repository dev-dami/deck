import { describe, expect, test } from "vitest";
import { APP_ICONS, appIconInfo, type FlatStyle } from "./catalog";
import { type Ctx, drawAppIcon } from "./draw";
import { glyphFor, type IconPlatform, stitchCells, stitchGrid, tileFor } from "./geometry";

// A 2D context that records what is drawn: each call with its arguments,
// each property set, and the stops of each gradient. Node has no canvas;
// the pixels are checked in Chromium (test/e2e/appIcon.spec.ts).
type Op = [string, ...unknown[]];

interface Recorder {
  ctx: Ctx;
  ops: Op[];
  layers: number;
}

function recorder(size: number): Recorder {
  const ops: Op[] = [];
  const r: Recorder = { ctx: null as unknown as Ctx, ops, layers: 0 };
  const make = (): Ctx => {
    const target: Record<string, unknown> = { canvas: { width: size, height: size } };
    return new Proxy(target, {
      get(t, k: string) {
        if (k in t) return t[k];
        if (k === "createLinearGradient" || k === "createRadialGradient") {
          return (...args: unknown[]) => {
            const stops: [number, string][] = [];
            ops.push([k, ...args]);
            return { stops, addColorStop: (at: number, c: string) => stops.push([at, c]) };
          };
        }
        return (...args: unknown[]) => void ops.push([k, ...args]);
      },
      set(t, k: string, v) {
        t[k] = v;
        ops.push([`=${k}`, v]);
        return true;
      },
    }) as unknown as Ctx;
  };
  r.ctx = make();
  return r;
}

function draw(id: string, size: number, platform: IconPlatform): Recorder {
  const r = recorder(size);
  const layer = () => {
    r.layers++;
    return recorder(size).ctx;
  };
  drawAppIcon(r.ctx, appIconInfo(id), size, platform, layer);
  return r;
}

const calls = (r: Recorder, name: string) => r.ops.filter((o) => o[0] === name);
const sets = (r: Recorder, prop: string) => r.ops.filter((o) => o[0] === `=${prop}`).map((o) => o[1]);
const stops = (v: unknown) => (v as { stops: [number, string][] }).stops.map((s) => s[1]);

describe("drawAppIcon", () => {
  test("draws every icon at every size on both platforms, balanced and clipped to the tile", () => {
    for (const i of APP_ICONS) {
      for (const platform of ["mac", "other"] as const) {
        for (const size of [16, 24, 32, 48, 64, 128, 256, 512]) {
          const r = draw(i.id, size, platform);
          const where = `${i.id} ${platform} ${size}`;
          expect(r.ops[0], where).toEqual(["clearRect", 0, 0, size, size]);
          expect(calls(r, "save").length, where).toBe(calls(r, "restore").length);
          expect(calls(r, "clip"), where).toHaveLength(1);
          // The ground and the mark at least.
          expect(calls(r, "fill").length + calls(r, "fillRect").length, where).toBeGreaterThanOrEqual(2);
          // No drawing past the image.
          for (const [, x, y, w, h] of calls(r, "fillRect") as [string, number, number, number, number][]) {
            expect(x, where).toBeGreaterThanOrEqual(-1);
            expect(y, where).toBeGreaterThanOrEqual(-1);
            expect(x + w, where).toBeLessThanOrEqual(size + 1);
            expect(y + h, where).toBeLessThanOrEqual(size + 1);
          }
        }
      }
    }
  });

  test("draws the same strokes every time", () => {
    for (const id of ["glass", "riso", "stitch", "classic"]) {
      expect(JSON.stringify(draw(id, 256, "mac").ops)).toBe(JSON.stringify(draw(id, 256, "mac").ops));
    }
  });

  test("a colourway's tile, chevron and cursor are its catalog colours", () => {
    for (const i of APP_ICONS) {
      if (i.style.kind !== "flat") continue;
      const st: FlatStyle = i.style;
      const r = draw(i.id, 256, "other");
      const fills = sets(r, "fillStyle");
      const gradients = fills.filter((f) => typeof f === "object").map(stops);
      expect(gradients[0], i.id).toEqual(st.tile);
      expect(gradients[1], i.id).toEqual(st.chevron);
      expect(fills, i.id).toContain(st.cursor);
    }
  });

  test("a Mac's icon casts its own shadow; elsewhere the image is the tile alone", () => {
    const mac = draw("classic", 256, "mac");
    const other = draw("classic", 256, "other");
    const firstClip = (r: Recorder) => r.ops.findIndex((o) => o[0] === "clip");
    expect(mac.ops.slice(0, firstClip(mac)).some((o) => o[0] === "=shadowBlur")).toBe(true);
    expect(other.ops.slice(0, firstClip(other)).some((o) => o[0] === "=shadowBlur")).toBe(false);
    // The continuous corner is drawn with curves, a plain one as a roundRect.
    expect(calls(mac, "bezierCurveTo").length).toBeGreaterThan(0);
    expect(calls(other, "roundRect").length).toBeGreaterThan(0);
  });

  test("small sizes drop shadows, blur and textures", () => {
    for (const i of APP_ICONS) {
      const r = draw(i.id, 16, "other");
      expect(sets(r, "shadowBlur").filter((b) => (b as number) > 0), i.id).toEqual([]);
      expect(sets(r, "filter"), i.id).toEqual([]);
      expect(r.layers, i.id).toBe(0);
    }
    expect(sets(draw("glass", 256, "other"), "filter")).toEqual([expect.stringMatching(/^blur\(\d+px\)$/)]);
  });

  test("Riso prints two impressions of halftone when large, and solid ink when small", () => {
    expect(draw("riso", 256, "other").layers).toBe(2);
    expect(draw("riso-paper", 512, "mac").layers).toBe(2);
    const small = draw("riso", 48, "other");
    expect(small.layers).toBe(0);
    expect(sets(small, "fillStyle")).toContain("#ff48b0");
  });

  test("Cross-stitch works each cell in two strands, and edges the tile with a running stitch", () => {
    const r = draw("stitch", 256, "other");
    const t = tileFor(256, "other");
    const cells = stitchCells(t, glyphFor(t, 256), stitchGrid(256));
    // Two passes over the cells, each strand stroked and then given its
    // sheen; the weave; the running stitch and its sheen.
    expect(calls(r, "stroke")).toHaveLength(cells.length * 2 * 2 + 3);
    expect(calls(r, "setLineDash")).toHaveLength(1);
    expect(sets(r, "fillStyle")).toContain("#e9dfca");
    expect(sets(draw("stitch-denim", 256, "other"), "fillStyle")).toContain("#33507a");
    // Below 48px there is no room for the edge.
    expect(calls(draw("stitch", 32, "other"), "setLineDash")).toHaveLength(0);
  });
});
