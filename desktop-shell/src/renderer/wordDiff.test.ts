import { describe, expect, test } from "vitest";
import { pairLines, similarity, wordDiff } from "./wordDiff";

const cut = (s: string, spans: [number, number][]) => spans.map(([a, b]) => s.slice(a, b));

describe("wordDiff", () => {
  test("marks the changed words of each side", () => {
    const a = "const delay = 100;";
    const b = "const delay = 250;";
    const w = wordDiff(a, b);
    expect(w).not.toBeNull();
    expect(cut(a, w?.del ?? [])).toEqual(["100"]);
    expect(cut(b, w?.add ?? [])).toEqual(["250"]);
  });

  test("an insertion marks only the new side", () => {
    const a = "wait(delay);";
    const b = "wait(delay * (i + 1));";
    const w = wordDiff(a, b);
    expect(w?.del).toEqual([]);
    // Either closing paren may be the new one; together they read as the insertion.
    expect(cut(b, w?.add ?? []).join("")).toBe(" * (i + 1)");
  });

  test("white space between two changed words joins them", () => {
    const a = "return a + b;";
    const b = "return x - y;";
    expect(cut(b, wordDiff(a, b)?.add ?? [])).toEqual(["x - y"]);
  });

  test("lines that share little are not marked word by word", () => {
    expect(wordDiff("import os", "func main() { fmt.Println(42) }")).toBeNull();
  });

  test("equal lines have nothing to mark, and long ones are skipped", () => {
    expect(wordDiff("same", "same")).toEqual({ del: [], add: [] });
    expect(wordDiff("x".repeat(1001), "y")).toBeNull();
  });

  test("spans count UTF-16 units", () => {
    const a = "naïve café";
    const b = "naïve cafe";
    expect(cut(b, wordDiff(a, b)?.add ?? [])).toEqual(["cafe"]);
  });
});

describe("pairLines", () => {
  test("equal counts pair by position", () => {
    expect(pairLines(["a", "b"], ["c", "d"])).toEqual([
      [0, 0],
      [1, 1],
    ]);
  });

  test("uneven counts pair the most alike lines, in order", () => {
    const dels = ["l.mu.Lock()", "global.fail()"];
    const adds = ["l.fail(addr)", "// Per address, never global (R1).", "l.prune()", "l.mu.Lock() // held"];
    expect(pairLines(dels, adds)).toEqual([[0, 3]]);
    expect(pairLines(["let total = sum(items);"], ["zzz", "let total = sum(items, 0);"])).toEqual([[0, 1]]);
  });

  test("nothing pairs with an empty side", () => {
    expect(pairLines([], ["a"])).toEqual([]);
  });

  test("similarity runs from 0 to 1", () => {
    expect(similarity("abc", "abc")).toBe(1);
    expect(similarity("abcd", "wxyz")).toBe(0);
    const s = similarity("const delay = 100;", "const delay = 250;");
    expect(s).toBeGreaterThan(0.6);
    expect(s).toBeLessThan(1);
  });
});
