import type { AgentLine } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { addAgentMention, applySuggestion, mentionAt, suggestions } from "./completion";

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
