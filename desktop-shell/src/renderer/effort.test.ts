import { describe, expect, test } from "vitest";
import { barHeights, effortGlyph, effortLevels, effortName, FALLBACK_LEVELS } from "./effort";

describe("the effort glyph", () => {
  test("one bar per level, filled up to the one the model runs at", () => {
    const claude = ["low", "medium", "high", "xhigh", "max"];
    expect(effortGlyph(claude, "high")).toEqual({ count: 5, filled: 3, auto: false });
    expect(effortGlyph(claude, "max")).toEqual({ count: 5, filled: 5, auto: false });
    expect(effortGlyph(["low", "medium", "high"], "low")).toEqual({ count: 3, filled: 1, auto: false });
  });

  test("an off level fills no bar and is not a bar of its own", () => {
    const pi = ["none", "low", "medium", "high"];
    expect(effortGlyph(pi, "none")).toEqual({ count: 3, filled: 0, auto: false });
    expect(effortGlyph(pi, "medium")).toEqual({ count: 3, filled: 2, auto: false });
  });

  test("the agent's default is the scale at rest", () => {
    expect(effortGlyph(["low", "high"], "")).toEqual({ count: 2, filled: 0, auto: true });
  });

  test("bars rise in one whole-pixel step to the full height", () => {
    expect(barHeights(5, 12)).toEqual([4, 6, 8, 10, 12]);
    expect(barHeights(3, 12)).toEqual([6, 9, 12]);
    expect(barHeights(1, 12)).toEqual([12]);
    for (const n of [2, 4, 6, 7]) {
      const h = barHeights(n, 12);
      expect(h.at(-1)).toBe(12);
      expect(new Set(h.slice(1).map((v, i) => v - (h[i] as number))).size).toBe(1);
    }
  });
});

describe("the levels the effort picker offers", () => {
  test("the model's own list, with a level set by hand after it", () => {
    expect(effortLevels(["low", "high"], "high")).toEqual(["low", "high"]);
    expect(effortLevels(["low", "high"], "")).toEqual(["low", "high"]);
    expect(effortLevels(["low", "high"], "turbo")).toEqual(["low", "high", "turbo"]);
  });

  test("a model that lists none takes none, unless a level is set", () => {
    expect(effortLevels([], "")).toEqual([]);
    expect(effortLevels([], "high")).toEqual(FALLBACK_LEVELS);
    expect(effortLevels([], "turbo")).toEqual([...FALLBACK_LEVELS, "turbo"]);
  });

  test("names as people read them", () => {
    expect(effortName("")).toBe("Default");
    expect(effortName("xhigh")).toBe("Extra high");
    expect(effortName("turbo")).toBe("Turbo");
  });
});
