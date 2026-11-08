import { describe, expect, test } from "vitest";
import { arcPath, dialSegments, effortDial, effortLevels, effortName, effortStep, FALLBACK_LEVELS, GAP, SWEEP } from "./effort";

describe("the effort dial", () => {
  test("one segment per level, lit up to the one the model runs at", () => {
    const claude = ["low", "medium", "high", "xhigh", "max"];
    expect(effortDial(claude, "high")).toMatchObject({ count: 5, lit: 3, auto: false });
    expect(effortDial(claude, "max")).toMatchObject({ count: 5, lit: 5, auto: false });
    expect(effortDial(["low", "medium", "high"], "low")).toMatchObject({ count: 3, lit: 1, auto: false });
  });

  test("the pointer stands at the middle of the level's segment, low to max clockwise", () => {
    const claude = ["low", "medium", "high", "xhigh", "max"];
    const angles = claude.map((l) => effortDial(claude, l).angle);
    expect(angles[2]).toBeCloseTo(0);
    expect(angles[4]).toBeCloseTo(-(angles[0] as number));
    for (let i = 1; i < angles.length; i++) expect(angles[i]).toBeGreaterThan(angles[i - 1] as number);
    const [a, b] = dialSegments(5)[4] as [number, number];
    expect(angles[4]).toBeCloseTo((a + b) / 2);
  });

  test("an off level lights no segment and is not a segment of its own", () => {
    const pi = ["none", "low", "medium", "high"];
    expect(effortDial(pi, "none")).toMatchObject({ count: 3, lit: 0, auto: false });
    expect(effortDial(pi, "none").angle).toBeLessThan(-SWEEP / 2);
    expect(effortDial(pi, "medium")).toMatchObject({ count: 3, lit: 2, auto: false });
  });

  test("the agent's default rests the pointer in the ring's gap", () => {
    expect(effortDial(["low", "high"], "")).toEqual({ count: 2, lit: 0, auto: true, angle: -180 });
  });

  test("the segments fill the sweep, a gap apart", () => {
    for (const n of [1, 2, 5, 7]) {
      const segs = dialSegments(n);
      expect(segs).toHaveLength(n);
      expect(segs[0]?.[0]).toBeCloseTo(-SWEEP / 2);
      expect(segs.at(-1)?.[1]).toBeCloseTo(SWEEP / 2);
      for (let i = 1; i < n; i++) expect((segs[i] as [number, number])[0] - (segs[i - 1] as [number, number])[1]).toBeCloseTo(GAP);
    }
  });

  test("turning steps through the default and the levels, and stops at the ends", () => {
    const levels = ["low", "medium", "high"];
    expect(effortStep(levels, "", 1)).toBe("low");
    expect(effortStep(levels, "medium", 1)).toBe("high");
    expect(effortStep(levels, "high", 1)).toBe("high");
    expect(effortStep(levels, "low", -1)).toBe("");
    expect(effortStep(levels, "", -3)).toBe("");
    expect(effortStep(levels, "low", 9)).toBe("high");
  });

  test("arcs run clockwise from twelve o'clock", () => {
    expect(arcPath(10, 8, 0, 90)).toBe("M 10.000 2.000 A 8 8 0 0 1 18.000 10.000");
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
