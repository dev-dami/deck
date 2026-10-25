import type { AgentLine } from "@web/protocol";
import { describe, expect, test } from "vitest";
import {
  addAgentMention,
  applySuggestion,
  fileAt,
  fileSuggestions,
  matchPaths,
  mentionAt,
  slashAt,
  slashSuggestions,
  suggestions,
} from "./completion";

const line = (id: string, kind: string, busy = false): AgentLine => ({ id, name: id, kind, busy });
const roster = [line("claude-1", "claude", true), line("claude-2", "claude"), line("codex-1", "codex")];
const kinds = ["claude", "codex", "opencode"];

describe("mentionAt", () => {
  test("finds the $word that ends at the caret", () => {
    expect(mentionAt("$co", 3)).toEqual({ start: 0, query: "co" });
    expect(mentionAt("fix it $", 8)).toEqual({ start: 7, query: "" });
    expect(mentionAt("$claude $codex+", 15)).toEqual({ start: 8, query: "codex+" });
  });

  test("ignores a $ inside a word and a caret past the word", () => {
    expect(mentionAt("cost$x", 6)).toBeNull();
    expect(mentionAt("$codex review", 13)).toBeNull();
    expect(mentionAt("$codex review", 3)).toEqual({ start: 0, query: "co" });
  });
});

describe("suggestions", () => {
  test("lists members, kinds, kind+ and kind* the way Deck reads them", () => {
    const all = suggestions("", roster, kinds).map((s) => s.insert);
    expect(all).toEqual([
      "$claude-1",
      "$claude-2",
      "$codex-1",
      "$claude",
      "$claude+",
      "$claude*",
      "$codex",
      "$codex+",
      "$opencode",
      "$opencode+",
      "$all",
    ]);
  });

  test("filters by prefix, case-insensitively", () => {
    expect(suggestions("CO", roster, kinds).map((s) => s.insert)).toEqual(["$codex-1", "$codex", "$codex+"]);
    expect(suggestions("codex+", roster, kinds).map((s) => s.insert)).toEqual(["$codex+"]);
  });

  test("an empty thread offers to start each kind", () => {
    const s = suggestions("", [], ["codex"]);
    expect(s.map((x) => [x.insert, x.detail])).toEqual([
      ["$codex", "start codex"],
      ["$codex+", "start another codex"],
    ]);
  });

  test("shows a member's state", () => {
    expect(suggestions("claude-1", roster, kinds)[0]?.detail).toBe("claude · busy");
  });
});

describe("applying", () => {
  test("replaces the mention and leaves the caret after a space", () => {
    const text = "$co please review";
    const m = mentionAt(text, 3);
    if (m === null) throw new Error("no mention");
    const s = suggestions(m.query, roster, kinds).find((x) => x.insert === "$codex+");
    if (s === undefined) throw new Error("no suggestion");
    expect(applySuggestion(text, m, s)).toEqual({ text: "$codex+ please review", caret: 8 });
  });

  test("the add-agent menu starts the message with $kind+", () => {
    expect(addAgentMention("  look at the tests", "codex")).toBe("$codex+ look at the tests");
    expect(addAgentMention("$claude review", "codex")).toBe("$codex+ $claude review");
  });
});

describe("slash commands", () => {
  test("trigger only at the start of the message", () => {
    expect(slashAt("/pl", 3)).toEqual({ start: 0, query: "pl" });
    expect(slashAt("/", 1)).toEqual({ start: 0, query: "" });
    expect(slashAt("fix /pl", 7)).toBeNull();
    expect(slashAt("/plan now", 9)).toBeNull();
  });

  test("list the commands by prefix", () => {
    expect(slashSuggestions("").map((s) => s.insert)).toEqual(["/plan", "/ask", "/yolo", "/new", "/race", "/share", "/stop", "/clear"]);
    expect(slashSuggestions("S").map((s) => s.insert)).toEqual(["/share", "/stop"]);
    expect(slashSuggestions("race")[0]?.label).toBe("/race <n> [kind] <brief>");
  });

  test("accepting one replaces the typed name", () => {
    const m = slashAt("/pl", 3);
    const s = slashSuggestions("pl")[0];
    if (m === null || s === undefined) throw new Error("no slash");
    expect(applySuggestion("/pl", m, s)).toEqual({ text: "/plan ", caret: 6 });
  });
});

describe("the @ file picker", () => {
  const paths = ["pkg/remote/auth/limit.go", "pkg/remote/auth/limit_test.go", "README.md", "docs/limits.md", "internal/deck/hub/team.go"];

  test("finds the @word that ends at the caret", () => {
    expect(fileAt("@li", 3)).toEqual({ start: 0, query: "li" });
    expect(fileAt("look at @pkg/re", 15)).toEqual({ start: 8, query: "pkg/re" });
    expect(fileAt("me@example", 10)).toBeNull();
    expect(fileAt("@limit.go now", 13)).toBeNull();
  });

  test("ranks file names, then paths, then scattered letters", () => {
    expect(matchPaths("limit", paths)).toEqual(["docs/limits.md", "pkg/remote/auth/limit.go", "pkg/remote/auth/limit_test.go"]);
    expect(matchPaths("auth", paths)).toEqual(["pkg/remote/auth/limit.go", "pkg/remote/auth/limit_test.go"]);
    expect(matchPaths("rdm", paths)).toEqual(["README.md", "internal/deck/hub/team.go"]);
    expect(matchPaths("hbtm", paths)).toEqual(["internal/deck/hub/team.go"]);
    expect(matchPaths("zzz", paths)).toEqual([]);
  });

  test("an empty query lists every path, in order, up to the limit", () => {
    expect(matchPaths("", paths)).toEqual(paths);
    expect(matchPaths("", paths, 2)).toEqual(paths.slice(0, 2));
  });

  test("inserts the path in backticks", () => {
    const text = "check @lim please";
    const m = fileAt(text, 10);
    const s = fileSuggestions("lim", paths)[0];
    if (m === null || s === undefined) throw new Error("no file");
    expect(applySuggestion(text, m, s)).toEqual({ text: "check `docs/limits.md` please", caret: 23 });
  });
});
