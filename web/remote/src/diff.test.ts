import { describe, expect, test } from "bun:test";
import { diffStats, parseDiff } from "./diff";

describe("parseDiff", () => {
  test("classifies headers, hunks, additions, removals and context", () => {
    const text = [
      "diff --git a/a.go b/a.go",
      "--- a/a.go",
      "+++ b/a.go",
      "@@ -1,3 +1,3 @@",
      " keep",
      "-old",
      "+new",
      " tail",
    ].join("\n");
    expect(parseDiff(text).map((l) => l.kind)).toEqual([
      "meta",
      "meta",
      "meta",
      "hunk",
      "ctx",
      "del",
      "add",
      "ctx",
    ]);
  });

  test("the approval fixture diff", () => {
    const lines = parseDiff("@@ -1 +1 @@\n-a\n+b\n");
    expect(lines.map((l) => l.kind)).toEqual(["hunk", "del", "add"]);
    expect(diffStats(lines)).toEqual({ added: 1, removed: 1 });
  });

  test("a bare @@ (changed_file fixture) does not break", () => {
    expect(parseDiff("@@")).toEqual([{ kind: "hunk", text: "@@" }]);
  });

  test("a removed line that reads like a file header stays a removal", () => {
    // Removing a line "-- sql comment" renders as "--- sql comment" inside a hunk.
    const lines = parseDiff("@@ -1,2 +1,1 @@\n--- sql comment\n keep");
    expect(lines.map((l) => l.kind)).toEqual(["hunk", "del", "ctx"]);
  });

  test("a second file's headers after the hunk is exhausted are meta", () => {
    const lines = parseDiff(
      "@@ -1 +1 @@\n-a\n+b\ndiff --git a/b b/b\n--- a/b\n+++ b/b\n@@ -1 +1 @@\n-x\n+y",
    );
    expect(lines.map((l) => l.kind)).toEqual([
      "hunk",
      "del",
      "add",
      "meta",
      "meta",
      "meta",
      "hunk",
      "del",
      "add",
    ]);
  });

  test("no-newline marker is meta and CRLF is handled", () => {
    const lines = parseDiff("@@ -1 +1 @@\r\n-a\r\n\\ No newline at end of file\r\n+b\r\n");
    expect(lines.map((l) => l.kind)).toEqual(["hunk", "del", "meta", "add"]);
  });

  test("empty input yields no lines", () => {
    expect(parseDiff("")).toEqual([]);
  });
});
