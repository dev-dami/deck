import { describe, expect, test } from "vitest";
import { diffRows } from "./diffRows";
import { expandTabs, highlightCode, highlightRows, langName, langOf, langOfFence, markSpans, type Seg, tokenLines } from "./highlight";

const classes = (segs: Seg[] | null | undefined) => (segs ?? []).filter((s) => s.cls !== "").map((s) => [s.text, s.cls]);
const text = (segs: Seg[] | null | undefined) => (segs ?? []).map((s) => s.text).join("");

describe("langOf", () => {
  test("names a file's language by its extension or its name", () => {
    expect(langOf("pkg/remote/auth/limit.go")).toBe("go");
    expect(langOf("src/App.tsx")).toBe("tsx");
    expect(langOf("a/b.mts")).toBe("typescript");
    expect(langOf("x.jsx")).toBe("jsx");
    expect(langOf("setup.py")).toBe("python");
    expect(langOf("main.rs")).toBe("rust");
    expect(langOf(".github/workflows/ci.yml")).toBe("yaml");
    expect(langOf("Cargo.toml")).toBe("toml");
    expect(langOf("README.md")).toBe("markdown");
    expect(langOf("deploy/Dockerfile")).toBe("docker");
    expect(langOf("Dockerfile.dev")).toBe("docker");
    expect(langOf("lib/x.hpp")).toBe("cpp");
    expect(langOf("Gemfile")).toBe("ruby");
    expect(langOf("App.kt")).toBe("kotlin");
    expect(langOf("C:\\code\\Main.java")).toBe("java");
    expect(langOf("index.HTML")).toBe("html");
  });

  test("is null for what Deck does not colour", () => {
    expect(langOf("LICENSE")).toBeNull();
    expect(langOf("photo.png")).toBeNull();
    expect(langOf(".gitignore")).toBeNull();
    expect(langOf("")).toBeNull();
  });

  test("fences and names", () => {
    expect(langOfFence("go")).toBe("go");
    expect(langOfFence("golang")).toBe("go");
    expect(langOfFence("TS title=x.ts")).toBe("typescript");
    expect(langOfFence("sh")).toBe("bash");
    expect(langOfFence("c++")).toBe("cpp");
    expect(langOfFence("")).toBeNull();
    expect(langOfFence("brainfuck")).toBeNull();
    expect(langName("go")).toBe("Go");
    expect(langName("cpp")).toBe("C++");
    expect(langName("docker")).toBe("Dockerfile");
  });

  test("every language Deck names has a grammar", () => {
    for (const p of ["a.ts", "a.tsx", "a.js", "a.jsx", "a.go", "a.py", "a.rs", "a.json", "a.yaml", "a.toml", "a.css", "a.html", "a.xml", "a.sh", "a.md", "a.sql", "a.diff", "Dockerfile", "a.java", "a.c", "a.cpp", "a.rb", "a.swift", "a.kt"]) {
      expect(tokenLines("x", langOf(p)), p).not.toBeNull();
    }
  });
});

describe("tokenLines", () => {
  test("colours Go", () => {
    const [l] = tokenLines('func f() string { return "x" }', "go") ?? [];
    expect(classes(l)).toContainEqual(["func", "syn-keyword"]);
    expect(classes(l)).toContainEqual(['"x"', "syn-string"]);
    expect(classes(l)).toContainEqual(["f", "syn-function"]);
  });

  test("a line inside a block comment stays a comment", () => {
    const lines = tokenLines("/* one\ntwo\nthree */\nlet x = 1;", "typescript") ?? [];
    expect(lines).toHaveLength(4);
    expect(classes(lines[1])).toEqual([["two", "syn-comment"]]);
    expect(classes(lines[3])).toContainEqual(["let", "syn-keyword"]);
  });

  test("code inside a template string is code again", () => {
    const [l] = tokenLines("const s = `a ${n + 1} b`;", "typescript") ?? [];
    expect(classes(l)).toContainEqual(["1", "syn-number"]);
    expect(classes(l)).toContainEqual(["a ", "syn-string"]);
  });

  test("keeps the text and empty lines", () => {
    const src = "a = 1\n\nb = 2";
    const lines = tokenLines(src, "python") ?? [];
    expect(lines.map(text)).toEqual(["a = 1", "", "b = 2"]);
  });

  test("is null without a grammar", () => {
    expect(tokenLines("x", null)).toBeNull();
    expect(tokenLines("x", "cobol")).toBeNull();
  });

  test("a code block over the limits goes plain", () => {
    expect(highlightCode("x".repeat(1001), "go")).toBeNull();
    expect(highlightCode("x = 1", "python")).not.toBeNull();
  });
});

describe("highlightRows", () => {
  test("each side of a hunk is tokenized whole", () => {
    // The removed line opens a comment only in the old file; the added
    // line after it is code in the new one.
    const rows = diffRows(["@@ -1,3 +1,3 @@", " x := 1", "-/* old", "+y := 2", " end */"].join("\n"));
    const hl = highlightRows(rows, "go");
    expect(hl[0]).toBeNull();
    expect(classes(hl[2])).toEqual([["/* old", "syn-comment"]]);
    expect(classes(hl[3])).toContainEqual([":=", "syn-operator"]);
    // Context takes the new file's view: "end */" is not in a comment there.
    expect(classes(hl[4]).some(([, c]) => c === "syn-comment")).toBe(false);
    expect(text(hl[1])).toBe("x := 1");
  });

  test("a long line stays plain, and so does a file with no language", () => {
    const long = `x := "${"a".repeat(1100)}"`;
    const rows = diffRows(`@@ -1,2 +1,2 @@\n-${long}\n+y := 1\n`);
    const hl = highlightRows(rows, "go");
    expect(hl[1]).toBeNull();
    expect(classes(hl[2])).toContainEqual([":=", "syn-operator"]);
    expect(highlightRows(rows, null).every((s) => s === null)).toBe(true);
  });

  test("a file past 5000 rows stays plain", () => {
    const body = Array.from({ length: 5001 }, (_, i) => `+x${i} := 1`).join("\n");
    expect(highlightRows(diffRows(`@@ -0,0 +1,5001 @@\n${body}`), "go").every((s) => s === null)).toBe(true);
  });
});

describe("markSpans and tabs", () => {
  test("cuts pieces where a changed span starts and ends", () => {
    const segs: Seg[] = [
      { text: "const ", cls: "syn-keyword", mark: false },
      { text: "delay = 100;", cls: "", mark: false },
    ];
    const out = markSpans(segs, [[3, 8]]);
    expect(out.map((s) => [s.text, s.cls, s.mark])).toEqual([
      ["con", "syn-keyword", false],
      ["st ", "syn-keyword", true],
      ["de", "", true],
      ["lay = 100;", "", false],
    ]);
  });

  test("tabs become spaces to the next stop of four", () => {
    expect(expandTabs("\tx", 0)).toEqual(["    x", 5]);
    expect(expandTabs("ab\tc", 0)).toEqual(["ab  c", 5]);
    expect(expandTabs("\t", 2)).toEqual(["  ", 4]);
    expect(expandTabs("none", 3)).toEqual(["none", 7]);
  });
});
