import { describe, expect, test } from "bun:test";
import {
  applySuggestion,
  contextAt,
  fileAt,
  fileSuggestions,
  mentionAt,
  nameAt,
  pathLike,
  pickerAt,
  suggestions,
} from "./mentions";
import type { AgentLine } from "./protocol";

const line = (id: string, kind: string, extra: Partial<AgentLine> = {}): AgentLine => ({
  id,
  name: id,
  kind,
  busy: false,
  ...extra,
});
const roster = [
  line("claude-1", "claude"),
  line("codex-1", "codex", { name: "rev", role: "reviewer" }),
];
const kinds = ["claude", "codex"];

describe("the composer's triggers", () => {
  test("@ opens agents and people, # files, $ agents", () => {
    expect(pickerAt("@re", 3, true)).toEqual({ type: "@", m: { start: 0, query: "re" } });
    expect(pickerAt("see #rea", 8, true)).toEqual({ type: "#", m: { start: 4, query: "rea" } });
    expect(pickerAt("$co", 3, true)).toEqual({ type: "$", m: { start: 0, query: "co" } });
  });

  test("@ before something path-like still offers files", () => {
    expect(pathLike("pkg/")).toBe(true);
    expect(pathLike("README.md")).toBe(true);
    expect(pathLike("rev")).toBe(false);
    expect(pickerAt("@pkg/re", 7, true)).toEqual({ type: "#", m: { start: 0, query: "pkg/re" } });
    expect(fileAt("@README.md", 10)).toEqual({ start: 0, query: "README.md" });
    expect(nameAt("@README.md", 10)).toBeNull();
  });

  test("no files: # and @path open nothing; emails and words with # stay text", () => {
    expect(pickerAt("#rea", 4, false)).toBeNull();
    expect(pickerAt("@pkg/", 5, false)).toBeNull();
    expect(pickerAt("me@example", 10, true)).toBeNull();
    expect(pickerAt("issue#4", 7, true)).toBeNull();
    expect(contextAt("# Heading", 9)).toBeNull();
    expect(mentionAt("cost$x", 6)).toBeNull();
  });
});

describe("the lists", () => {
  test("@ names members by name and shows the role; $ keeps ids", () => {
    const at = suggestions("", roster, kinds, { sigil: "@" });
    expect(at.map((s) => s.insert)).toEqual([
      "@claude-1",
      "@rev",
      "@claude",
      "@claude+",
      "@codex",
      "@codex+",
      "@all",
    ]);
    expect(at[1]).toEqual({
      insert: "@rev",
      label: "rev",
      detail: "reviewer · codex",
      kind: "codex",
    });
    expect(suggestions("", roster, kinds).map((s) => s.insert)).toContain("$codex-1");
  });

  test("profiles with no member and people come before the kinds", () => {
    const s = suggestions("", [], ["claude"], {
      sigil: "@",
      profiles: [{ name: "charles", kind: "claude", role: "reviewer" }],
      people: [{ name: "alice" }],
    });
    expect(s.map((x) => [x.insert, x.detail, x.kind])).toEqual([
      ["@charles", "reviewer", "claude"],
      ["@alice", "teammate", null],
      ["@claude", "start claude", "claude"],
      ["@claude+", "start another claude", "claude"],
    ]);
  });

  test("a file from # goes in as `path`, as an @path pick did", () => {
    const m = contextAt("see #rea", 8);
    const s = fileSuggestions("rea", ["README.md"])[0];
    if (m === null || s === undefined) throw new Error("no file");
    expect(applySuggestion("see #rea", m, s)).toEqual({ text: "see `README.md` ", caret: 16 });
  });
});
