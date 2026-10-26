import { describe, expect, test } from "vitest";
import {
  atkinson,
  atkinsonPalette,
  autoLevels,
  chartTable,
  chartTones,
  dotRadius,
  grid,
  hex,
  inBlock,
  inSuit,
  levels,
  lightness,
  markGeometry,
  pips,
  place,
  rankCells,
  risoBlocks,
  screen,
  T,
  tableLayout,
} from "./core";
import { artFor, colourway, processOf, SPOT } from "./inks";

describe("colour", () => {
  test("hex reads three and six digits", () => {
    expect(hex("#1d2240")).toEqual([0x1d, 0x22, 0x40]);
    expect(hex("fff")).toEqual([255, 255, 255]);
  });
  test("lightness runs from ink to paper", () => {
    expect(lightness([0, 0, 0])).toBe(0);
    expect(lightness([255, 255, 255])).toBeCloseTo(1);
  });
});

describe("fitting a picture", () => {
  test("cover fills the frame and contain fits inside it", () => {
    const c = place(400, 200, 100, 100, "cover");
    expect(c.h).toBe(100);
    expect(c.w).toBe(200);
    expect(c.x).toBe(-50);
    const k = place(400, 200, 100, 100, "contain", 0.5, 1);
    expect(k.w).toBe(100);
    expect(k.y).toBe(50);
  });
  test("levels stretch a faded print to the full range", () => {
    const v = Float32Array.from({ length: 1000 }, (_, i) => 0.3 + (i / 999) * 0.4);
    const [black, white] = autoLevels(v, 0.01, 0.99);
    expect(black).toBeCloseTo(0.3, 1);
    expect(white).toBeCloseTo(0.7, 1);
    levels(v, black, white);
    expect(Math.min(...v)).toBeCloseTo(0, 1);
    expect(Math.max(...v)).toBeCloseTo(1, 1);
  });
});

describe("the dither", () => {
  test("Atkinson prints two tones that keep a grey's average", () => {
    const w = 64;
    const v = new Float32Array(w * w).fill(0.3);
    const bits = atkinson(v, w, w);
    expect(new Set(bits)).toEqual(new Set([0, 1]));
    const paper = bits.reduce((a, b) => a + b, 0) / bits.length;
    expect(paper).toBeGreaterThan(0.2);
    expect(paper).toBeLessThan(0.4);
  });
  test("Atkinson keeps black and white clean", () => {
    expect(atkinson(new Float32Array(100).fill(0), 10, 10).every((b) => b === 0)).toBe(true);
    expect(atkinson(new Float32Array(100).fill(1), 10, 10).every((b) => b === 1)).toBe(true);
  });
  test("in colour, an ink prints as itself", () => {
    const inks = [hex("#101119"), hex("#7aa2f7"), hex("#f7768e")];
    const rgb = new Float32Array(9 * 3);
    for (let i = 0; i < 9; i++) rgb.set(inks[i % 3] as readonly number[], i * 3);
    expect([...atkinsonPalette(rgb, 9, 1, inks)]).toEqual([0, 1, 2, 0, 1, 2, 0, 1, 2]);
  });
});

describe("the halftone screen", () => {
  test("covers the frame on a grid turned by its angle", () => {
    const pts = screen(200, 100, 10, 15);
    let near = Infinity;
    for (let k = 2; k < pts.length; k += 2) near = Math.min(near, Math.hypot((pts[k] as number) - (pts[0] as number), (pts[k + 1] as number) - (pts[1] as number)));
    expect(near).toBeCloseTo(10, 3);
    // Every corner of the frame has a dot within one pitch.
    for (const [x, y] of [
      [0, 0],
      [200, 0],
      [0, 100],
      [200, 100],
    ]) {
      let d = Infinity;
      for (let k = 0; k < pts.length; k += 2) d = Math.min(d, Math.hypot((pts[k] as number) - (x as number), (pts[k + 1] as number) - (y as number)));
      expect(d).toBeLessThan(10);
    }
  });
  test("a dot covers its share of the cell", () => {
    expect(dotRadius(0, 10)).toBe(0);
    expect(Math.PI * dotRadius(0.5, 10) ** 2).toBeCloseTo(50);
  });
});

describe("the riso's blocks", () => {
  // Blossfeldt's Allium as the window frames it: right of centre.
  const W = 1360;
  const H = 860;
  const allium = place(900, 1200, W, H, "contain", 0.88, 0.3, 0.92);

  test("sit behind the subject and keep the sidebar's side clear", () => {
    const blocks = risoBlocks(W, H, allium, 20);
    expect(blocks.map((b) => b.kind)).toEqual(["disc", "bar"]);
    const disc = blocks[0];
    if (disc?.kind !== "disc") throw new Error("no disc");
    expect(inBlock(disc, allium.x + allium.w / 2, allium.y + allium.h * 0.32)).toBe(true);
    // Nothing under the sidebar (8px gutter + 256px + its gutter).
    for (let y = 0; y < H; y += 4) for (let x = 0; x < 280; x += 4) expect(blocks.some((b) => inBlock(b, x, y))).toBe(false);
  });

  test("stay inside the press's margin, so no spot colour shows in the gutters", () => {
    for (const [w, h] of [
      [1360, 860],
      [1440, 900],
      [900, 640],
      [2560, 1440],
    ] as const) {
      const m = 20;
      const subject = place(900, 1200, w, h, "contain", 0.88, 0.3, 0.92);
      for (const b of risoBlocks(w, h, subject, m)) {
        if (b.kind === "bar") {
          expect(b.x).toBeGreaterThanOrEqual(m);
          expect(b.x + b.w).toBeLessThanOrEqual(w - m + 1e-6);
          expect(b.y + b.h).toBeLessThanOrEqual(h - m);
        }
      }
    }
  });
});

describe("cross-stitch", () => {
  test("the lightest tones stay bare linen, the darkest bare denim", () => {
    const v = Float32Array.from([0, 0.2, 0.45, 0.6, 1]);
    expect([...chartTones(v, 3, 0.48, false)]).toEqual([0, 1, 2, -1, -1]);
    expect([...chartTones(v, 3, 0.38, true)]).toEqual([-1, -1, 0, 1, 2]);
  });
  test("the grid centres on the frame", () => {
    expect(grid(105, 50, 10)).toEqual({ cols: 11, rows: 5, ox: -2.5, oy: 0 });
  });
  test("suits and pips", () => {
    expect(inSuit("heart", 0, 0)).toBe(true);
    expect(inSuit("diamond", 0.95, 0.95)).toBe(false);
    for (let r = 1; r <= 10; r++) expect(pips(r)).toHaveLength(r);
    // "10" sets its two glyphs side by side, a cell apart.
    const ten = rankCells("10");
    expect(Math.max(...ten.map(([x]) => x))).toBeGreaterThanOrEqual(6);
    expect(Math.max(...ten.map(([, y]) => y))).toBe(6);
  });
  test("the cards and dice keep clear of the sidebar and the gutters", () => {
    // 10px cells: the window at 1360×860 and at its smallest; the sidebar and its
    // gutter take the first 272px.
    for (const [cols, rows] of [
      [136, 86],
      [96, 56],
    ] as const) {
      const chart = chartTable(cols, rows, tableLayout(cols, rows, 28));
      for (let y = 0; y < rows; y++) {
        for (let x = 0; x < 28; x++) expect(chart[y * cols + x], `${cols} ${x},${y}`).toBe(T.Blank);
        expect(chart[y * cols + cols - 1]).toBe(T.Blank);
      }
      for (let x = 0; x < cols; x++) expect(chart[(rows - 1) * cols + x]).toBe(T.Blank);
    }
  });

  test("a hand of cards reads as cards on the window's cloth and on a print's", () => {
    for (const [cols, rows] of [
      [136, 86],
      [192, 52],
      [74, 110],
    ] as const) {
      const hand = tableLayout(cols, rows);
      const card = hand.find((p) => p.kind === "card");
      expect(card?.h).toBeGreaterThanOrEqual(Math.min(20, Math.round(cols * 0.3)));
      for (const p of hand) {
        expect(p.cx).toBeGreaterThan(0);
        expect(p.cx).toBeLessThan(cols);
      }
      const chart = chartTable(cols, rows, hand);
      expect(chart.filter((t) => t === T.Card).length).toBeGreaterThan(50);
      expect(chart.filter((t) => t === T.Ink).length).toBeGreaterThan(5);
    }
  });
});

describe("the Deck mark", () => {
  test("the chevron and cursor fit the frame's height", () => {
    const m = markGeometry(0, 0, 268);
    expect(m.chevron[0]).toEqual([0, 0]);
    expect(m.chevron[2]).toEqual([0, 268]);
    expect(m.cursor.y + m.cursor.h).toBeLessThanOrEqual(268 + 1e-6);
  });
});

describe("art directions", () => {
  const tokens = { backdrop: hex("#101010"), canvas: hex("#202020"), primary: hex("#0a84ff"), text: hex("#e3e3e5") };

  test("Liquid Glass prints the Alps as a navy and peach duotone in 2px cells", () => {
    const a = artFor("glass", "dark");
    expect(a).toMatchObject({ process: "scene", picture: "alps", cell: 2, colour: false });
    if (a.process !== "scene") throw new Error();
    expect(a.inks).toEqual([hex("#1d2240"), hex("#f3c9a8")]);
    expect(a.mark).toBeGreaterThan(0);
    // As a print it hangs the same duotone in light mode, with no sky.
    expect(artFor("glass", "light", undefined, "print")).toMatchObject({ inks: [hex("#1d2240"), hex("#f3c9a8")], sky: 0 });
  });

  test("Riso screens Blossfeldt's Allium in fluorescent pink, black stock in dark and paper in light", () => {
    const dark = artFor("riso", "dark");
    const light = artFor("riso", "light");
    expect(dark).toMatchObject({ process: "halftone", picture: "allium", spot: SPOT, onBlack: true });
    expect(light).toMatchObject({ process: "halftone", onBlack: false });
    expect(SPOT).toEqual(hex("#ff48b0"));
    if (dark.process !== "halftone" || light.process !== "halftone") throw new Error();
    expect(lightness(dark.ground)).toBeLessThan(0.1);
    expect(lightness(light.ground)).toBeGreaterThan(0.9);
    expect(dark.margin).toBeGreaterThan(8);
    expect(artFor("riso", "dark", undefined, "print")).toMatchObject({ margin: 0 });
  });

  test("Stitch works cards and dice on linen in light and denim in dark", () => {
    expect(artFor("stitch", "light")).toMatchObject({ process: "stitch", table: true, twill: false });
    expect(artFor("stitch", "dark")).toMatchObject({ process: "stitch", table: true, twill: true });
  });

  test("the catalog themes are colourways of Liquid Glass", () => {
    expect(processOf("apple")).toBe("scene");
    expect(processOf("riso")).toBe("halftone");
    expect(processOf("stitch")).toBe("stitch");
    const c = artFor("apple", "dark", tokens);
    expect(c).toMatchObject({ process: "scene", picture: "alps" });
    expect(c).toEqual(colourway("dark", tokens));
    expect(artFor("tokyo-night", "dark", tokens)).toMatchObject({ picture: "xdf", colour: true });
  });
});
