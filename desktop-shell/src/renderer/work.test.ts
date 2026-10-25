import { describe, expect, test } from "vitest";
import { capLines, formatDuration, splitWork } from "./work";

describe("tool output", () => {
  const lines = (n: number) => Array.from({ length: n }, (_, i) => `line ${i + 1}`).join("\n");

  test("up to the cap, the output shows whole", () => {
    expect(capLines(lines(40), 40)).toEqual({ text: lines(40), count: 40, cut: false });
    expect(capLines(`${lines(40)}\n`, 40).cut).toBe(false);
  });

  test("past the cap, the first lines show and the count names them all", () => {
    expect(capLines(`${lines(41)}\n`, 40)).toEqual({ text: lines(40), count: 41, cut: true });
  });
});

describe("work fold", () => {
  test("the text after the last tool stays; the rest folds", () => {
    const tool = { k: "tool", call: null, result: null } as const;
    const text = (t: string) => ({ k: "text", text: t, truncated: false }) as const;
    const r = splitWork([text("looking"), tool, text("found it"), text("done")]);
    expect(r.work).toHaveLength(2);
    expect(r.answer.map((c) => (c.k === "text" ? c.text : ""))).toEqual(["found it", "done"]);
    expect(splitWork([text("hi")]).work).toEqual([]);
  });

  test("durations read like T3's", () => {
    expect(formatDuration(45_000)).toBe("45s");
    expect(formatDuration(223_000)).toBe("3m 43s");
    expect(formatDuration(3_720_000)).toBe("1h 2m");
  });
});
