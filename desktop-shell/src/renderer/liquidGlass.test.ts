import { describe, expect, test } from "vitest";
import { lensFor, lensMap } from "./liquidGlass";

// The red and green of the map at (x, y): where the backdrop is read from,
// 128 being in place.
function at(map: Uint8ClampedArray, mw: number, x: number, y: number): [number, number] {
  const i = (y * mw + x) * 4;
  return [map[i] as number, map[i + 1] as number];
}

describe("the liquid glass lens", () => {
  test("its rim is no wider than a third of the panel, and its bend no deeper than the rim", () => {
    const small = lensFor(80, 28, 14);
    expect(small.band).toBeLessThanOrEqual(28 / 2);
    expect(small.depth).toBeLessThanOrEqual(small.band * 1.1);
    expect(lensFor(256, 840, 18).band).toBe(32);
    // A corner never rounds past half the panel.
    expect(lensFor(40, 20, 999).r).toBe(10);
  });

  test("the middle stays clear, and the rim bends inward", () => {
    const l = lensFor(200, 100, 20);
    const map = lensMap(l, 200, 100);
    expect(at(map, 200, 100, 50)).toEqual([128, 128]);
    // At the left edge the backdrop is read from further right, at the
    // top from further down.
    expect(at(map, 200, 0, 50)[0]).toBeGreaterThan(200);
    expect(at(map, 200, 100, 0)[1]).toBeGreaterThan(200);
    expect(at(map, 200, 199, 50)[0]).toBeLessThan(60);
  });
});
