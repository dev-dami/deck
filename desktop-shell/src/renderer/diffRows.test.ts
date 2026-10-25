import { expect, test } from "vitest";
import { diffRows, foldContext, isFold, pairRows, pathOfDiff, showHunk, splitRows } from "./diffRows";

const line = (kind: "add" | "del" | "ctx", oldNum: number | null, newNum: number | null, text: string) => ({
  k: "line",
  kind,
  num: kind === "del" ? oldNum : newNum,
  oldNum,
  newNum,
  text,
});

test("numbers lines in both files and reads each hunk's header", () => {
  const rows = diffRows(
    [
      "diff --git a/x.go b/x.go",
      "--- a/x.go",
      "+++ b/x.go",
      "@@ -10,3 +10,3 @@ func x()",
      " a",
      "-b",
      "+c",
      " d",
      "@@ -40,1 +40,2 @@",
      " e",
      "+f",
    ].join("\n"),
  );
  expect(rows).toEqual([
    { k: "hunk", header: "@@ -10,3 +10,3 @@", context: "func x()", skipped: 9, first: true },
    line("ctx", 10, 10, "a"),
    line("del", 11, null, "b"),
    line("add", null, 11, "c"),
    line("ctx", 12, 12, "d"),
    { k: "hunk", header: "@@ -40,1 +40,2 @@", context: "", skipped: 27, first: false },
    line("ctx", 40, 40, "e"),
    line("add", null, 41, "f"),
  ]);
});

test("a new file starts with its first line, not a header", () => {
  const rows = diffRows("@@ -0,0 +1,2 @@\n+a\n+b");
  expect(rows[0]).toEqual({ k: "hunk", header: "@@ -0,0 +1,2 @@", context: "", skipped: null, first: true });
  expect(showHunk(rows[0] as never)).toBe(false);
  expect(rows.slice(1)).toEqual([line("add", null, 1, "a"), line("add", null, 2, "b")]);
});

test("a first hunk shows its header when it names a function", () => {
  const [h] = diffRows("@@ -1,2 +1,2 @@ class A {\n-a\n+b\n");
  expect(showHunk(h as never)).toBe(true);
});

test("the path comes from the +++ header", () => {
  expect(pathOfDiff("--- a/x.go\n+++ b/pkg/x.go\n@@ -1 +1 @@\n-a\n+b")).toBe("pkg/x.go");
  expect(pathOfDiff("--- a/x.go\n+++ /dev/null\n")).toBeNull();
  expect(pathOfDiff("@@ -1 +1 @@\n-a\n+b")).toBeNull();
});

test("removed and added lines pair up, both ways", () => {
  const rows = diffRows("@@ -1,3 +1,3 @@\n a\n-const x = 1;\n+const x = 2;\n b\n");
  expect([...pairRows(rows)]).toEqual([
    [2, 3],
    [3, 2],
  ]);
});

test("split rows align pairs and leave blanks across from the rest", () => {
  const rows = diffRows(
    ["@@ -1,4 +1,4 @@", " keep", "-return a + b;", "-// gone", "+return a + c;", " end"].join("\n"),
  );
  // rows: 0 hunk, 1 ctx, 2 del, 3 del, 4 add, 5 ctx
  expect(splitRows(rows, pairRows(rows))).toEqual([
    { k: "row", i: 0 },
    { k: "pair", left: 1, right: 1 },
    { k: "pair", left: 2, right: 4 },
    { k: "pair", left: 3, right: null },
    { k: "pair", left: 5, right: 5 },
  ]);
});

test("unpaired lines sit side by side before the next pair", () => {
  const rows = diffRows(["@@ -1,2 +1,3 @@", "-alpha();", "-let total = sum(items);", "+zzz", "+let total = sum(items, 0);"].join("\n"));
  // rows: 0 hunk, 1 del, 2 del, 3 add, 4 add
  expect(splitRows(rows, pairRows(rows)).slice(1)).toEqual([
    { k: "pair", left: 1, right: 3 },
    { k: "pair", left: 2, right: 4 },
  ]);
  const uneven = diffRows(["@@ -1,1 +1,3 @@", "-let total = sum(items);", "+zzz", "+yyy", "+let total = sum(items, 0);"].join("\n"));
  expect(splitRows(uneven, pairRows(uneven)).slice(1)).toEqual([
    { k: "pair", left: null, right: 2 },
    { k: "pair", left: null, right: 3 },
    { k: "pair", left: 1, right: 4 },
  ]);
});

test("long runs of context fold in the middle", () => {
  const items = Array.from({ length: 20 }, (_, i) => (i === 0 || i === 19 ? "x" : "c"));
  const out = foldContext(items, (t) => t === "c", () => false);
  expect(out.length).toBe(1 + 3 + 1 + 3 + 1);
  const fold = out[4];
  expect(isFold(fold) && fold.items.length).toBe(12);
  // At a hunk's edge only the changed side keeps lines; short runs stay.
  const edge = foldContext(["h", "c", "c", "c", "c", "c", "c", "c", "c", "c", "c", "x"], (t) => t === "c", (t) => t === "h");
  expect(edge.slice(0, 2)).toEqual(["h", { k: "fold", items: ["c", "c", "c", "c", "c", "c", "c"] }]);
  expect(foldContext(["x", "c", "c", "x"], (t) => t === "c", () => false)).toEqual(["x", "c", "c", "x"]);
});
