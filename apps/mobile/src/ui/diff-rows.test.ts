import { expect, test } from "bun:test";
import { diffRows } from "./diff-rows";

test("numbers lines and counts the lines between hunks", () => {
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
    { k: "fold", skipped: 9 },
    { k: "line", kind: "ctx", num: 10, text: "a" },
    { k: "line", kind: "del", num: 11, text: "b" },
    { k: "line", kind: "add", num: 11, text: "c" },
    { k: "line", kind: "ctx", num: 12, text: "d" },
    { k: "fold", skipped: 27 },
    { k: "line", kind: "ctx", num: 40, text: "e" },
    { k: "line", kind: "add", num: 41, text: "f" },
  ]);
});

test("a new file starts with its first line, not a fold", () => {
  expect(diffRows("@@ -0,0 +1,2 @@\n+a\n+b")).toEqual([
    { k: "line", kind: "add", num: 1, text: "a" },
    { k: "line", kind: "add", num: 2, text: "b" },
  ]);
});

test("a malformed hunk header folds without a count", () => {
  expect(diffRows("@@\n+a")).toEqual([
    { k: "fold", skipped: null },
    { k: "line", kind: "add", num: null, text: "a" },
  ]);
});
