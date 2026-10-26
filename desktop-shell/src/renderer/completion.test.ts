import type { AgentLine } from "@web/protocol";
import { afterEach, describe, expect, test, vi } from "vitest";
import {
  addAgentMention,
  applySuggestion,
  contextAt,
  FILES_DEBOUNCE_MS,
  fileAt,
  fileLister,
  fileSuggestions,
  mergeFiles,
  matchPaths,
  mentionAt,
  nameAt,
  pathLike,
  pickerAt,
  slashAt,
  slashSuggestions,
  suggestionHue,
  suggestions,
} from "./completion";

const line = (id: string, kind: string, busy = false, extra: Partial<AgentLine> = {}): AgentLine => ({ id, name: id, kind, busy, ...extra });
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
    expect(slashSuggestions("race")[0]?.label).toBe("/race <n> [kind] [judge] <brief>");
  });

  test("accepting one replaces the typed name", () => {
    const m = slashAt("/pl", 3);
    const s = slashSuggestions("pl")[0];
    if (m === null || s === undefined) throw new Error("no slash");
    expect(applySuggestion("/pl", m, s)).toEqual({ text: "/plan ", caret: 6 });
  });
});

describe("the @ picker: agents and people", () => {
  const profiles = [
    { name: "charles", kind: "claude", role: "reviewer" },
    { name: "ada", kind: "codex" },
    // A profile with a live member is offered as the member.
    { name: "claude-2", kind: "claude", role: "taken" },
  ];
  const people = [{ name: "alice", role: "teammate" }];

  test("finds the @name that ends at the caret", () => {
    expect(nameAt("@ch", 3)).toEqual({ start: 0, query: "ch" });
    expect(nameAt("ask @", 5)).toEqual({ start: 4, query: "" });
    expect(nameAt("ask @claude+", 12)).toEqual({ start: 4, query: "claude+" });
    expect(nameAt("me@example", 10)).toBeNull();
    expect(nameAt("@src/a", 6)).toBeNull();
    expect(nameAt("@main.go", 8)).toBeNull();
    expect(nameAt("@ch now", 7)).toBeNull();
  });

  test("lists members by name, kinds, profiles, then people, with @", () => {
    const named = [line("claude-1", "claude", true), line("claude-2", "claude"), line("codex-1", "codex", false, { name: "rev", role: "reviewer", profile: "rev" })];
    expect(suggestions("", named, kinds, { sigil: "@", profiles, people }).map((s) => s.insert)).toEqual([
      "@claude-1",
      "@claude-2",
      "@rev",
      "@claude",
      "@claude+",
      "@claude*",
      "@codex",
      "@codex+",
      "@opencode",
      "@opencode+",
      "@charles",
      "@ada",
      "@alice",
      "@all",
    ]);
  });

  test("each row carries the kind for its glyph, the name and the role", () => {
    const s = suggestions("", roster, kinds, { sigil: "@", profiles, people });
    expect(s.find((x) => x.label === "charles")).toEqual({ insert: "@charles", label: "charles", detail: "reviewer", kind: "claude" });
    expect(s.find((x) => x.label === "ada")).toEqual({ insert: "@ada", label: "ada", detail: "start ada, a codex", kind: "codex" });
    expect(s.find((x) => x.label === "alice")).toEqual({ insert: "@alice", label: "alice", detail: "teammate", kind: null });
    const member = suggestions("rev", [line("rev", "claude", true, { role: "reviewer" })], [], { sigil: "@" })[0];
    expect(member).toEqual({ insert: "@rev", label: "rev", detail: "reviewer · claude · busy", kind: "claude" });
  });

  test("a profile's own colour is its row's hue; else the kind's, and none for a person", () => {
    const hue = (k: string) => `hue-${k}`;
    const s = suggestions("", [], kinds, { sigil: "@", profiles: [{ name: "zed", kind: "codex", color: "tomato" }, ...profiles], people });
    const row = (label: string) => s.find((x) => x.label === label) ?? { insert: "", label, detail: "", kind: null };
    expect(suggestionHue(row("zed"), hue)).toBe("tomato");
    expect(suggestionHue(row("ada"), hue)).toBe("hue-codex");
    expect(suggestionHue(row("alice"), hue)).toBeUndefined();
  });

  test("filters by prefix, and $ offers no profiles or people", () => {
    expect(suggestions("CH", roster, kinds, { sigil: "@", profiles, people }).map((s) => s.insert)).toEqual(["@charles"]);
    expect(suggestions("a", roster, kinds, { sigil: "@", profiles, people }).map((s) => s.insert)).toEqual(["@ada", "@alice", "@all"]);
    expect(suggestions("", roster, kinds, { profiles, people }).map((s) => s.insert)).not.toContain("$charles");
  });

  test("a pick inserts @name and a space", () => {
    const text = "@cha look again";
    const m = nameAt(text, 4);
    const s = suggestions("cha", roster, kinds, { sigil: "@", profiles })[0];
    if (m === null || s === undefined) throw new Error("no name");
    expect(applySuggestion(text, m, s)).toEqual({ text: "@charles look again", caret: 9 });
  });
});

describe("which picker the text opens", () => {
  test("@ names, # files, $ agents, and @ before a path still files", () => {
    expect(pickerAt("@co", 3, true)).toEqual({ type: "@", m: { start: 0, query: "co" } });
    expect(pickerAt("#li", 3, true)).toEqual({ type: "#", m: { start: 0, query: "li" } });
    expect(pickerAt("see #", 5, true)).toEqual({ type: "#", m: { start: 4, query: "" } });
    expect(pickerAt("$co", 3, true)).toEqual({ type: "$", m: { start: 0, query: "co" } });
    expect(pickerAt("look at @pkg/re", 15, true)).toEqual({ type: "#", m: { start: 8, query: "pkg/re" } });
    expect(pickerAt("@README.md", 10, true)).toEqual({ type: "#", m: { start: 0, query: "README.md" } });
  });

  test("without files, # and a path after @ open nothing", () => {
    expect(pickerAt("#li", 3, false)).toBeNull();
    expect(pickerAt("@pkg/", 5, false)).toBeNull();
    expect(pickerAt("@co", 3, false)).toEqual({ type: "@", m: { start: 0, query: "co" } });
  });

  test("a Deck that reads only $: @ picks files as it did, # still files, $ agents", () => {
    expect(pickerAt("@co", 3, true, false)).toEqual({ type: "#", m: { start: 0, query: "co" } });
    expect(pickerAt("#co", 3, true, false)).toEqual({ type: "#", m: { start: 0, query: "co" } });
    expect(pickerAt("$co", 3, true, false)).toEqual({ type: "$", m: { start: 0, query: "co" } });
    expect(pickerAt("@co", 3, false, false)).toBeNull();
  });

  test("a # inside a word, a heading or ## opens nothing", () => {
    expect(pickerAt("issue#12", 8, true)).toBeNull();
    expect(pickerAt("# Title", 7, true)).toBeNull();
    expect(pickerAt("##", 2, true)).toBeNull();
    expect(contextAt("a #b", 4)).toEqual({ start: 2, query: "b" });
  });

  test("path-like means a slash or a file extension", () => {
    expect(pathLike("src/")).toBe(true);
    expect(pathLike("main.go")).toBe(true);
    expect(pathLike("charles")).toBe(false);
    expect(pathLike("claude-2")).toBe(false);
    expect(pathLike("v.")).toBe(false);
  });
});

describe("the # file picker", () => {
  const paths = ["pkg/remote/auth/limit.go", "pkg/remote/auth/limit_test.go", "README.md", "docs/limits.md", "internal/deck/hub/team.go"];

  test("@ before a path is a file reference, as before", () => {
    expect(fileAt("@li", 3)).toBeNull();
    expect(fileAt("look at @pkg/re", 15)).toEqual({ start: 8, query: "pkg/re" });
    expect(fileAt("me@example", 10)).toBeNull();
    expect(fileAt("@limit.go now", 13)).toBeNull();
    expect(fileAt("@limit.go", 9)).toEqual({ start: 0, query: "limit.go" });
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

  test("inserts the path in backticks, the same from # as from @", () => {
    const s = fileSuggestions("lim", paths)[0];
    if (s === undefined) throw new Error("no file");
    const hash = contextAt("check #lim please", 10);
    if (hash === null) throw new Error("no #");
    expect(applySuggestion("check #lim please", hash, s)).toEqual({ text: "check `docs/limits.md` please", caret: 23 });
    const at = fileAt("check @docs/lim please", 15);
    if (at === null) throw new Error("no @path");
    expect(applySuggestion("check @docs/lim please", at, s).text).toBe("check `docs/limits.md` please");
  });
});

describe("the # file picker over ListFiles", () => {
  afterEach(() => {
    vi.useRealTimers();
  });

  test("the room's changed files come first, then the rest of the tree, each once", () => {
    const changed = ["pkg/remote/auth/limit.go", "README.md"];
    const listed = ["main.go", "pkg/remote/auth/limit.go", "pkg/remote/auth/limit_test.go"];
    expect(mergeFiles("limit", changed, listed).map((s) => [s.label, s.detail])).toEqual([
      ["pkg/remote/auth/limit.go", "changed"],
      ["main.go", ""],
      ["pkg/remote/auth/limit_test.go", ""],
    ]);
    expect(mergeFiles("", [], listed).map((s) => s.insert)).toEqual(["`main.go`", "`pkg/remote/auth/limit.go`", "`pkg/remote/auth/limit_test.go`"]);
    expect(mergeFiles("x", [], Array.from({ length: 30 }, (_, i) => `f${i}`))).toHaveLength(20);
  });

  test("asks once the typing stops, and only the latest answer counts", async () => {
    vi.useFakeTimers();
    const asked: string[] = [];
    const answers = new Map<string, (p: string[]) => void>();
    const found: [string, readonly string[]][] = [];
    const l = fileLister(
      (q) => {
        asked.push(q);
        return new Promise((r) => answers.set(q, r));
      },
      (q, p) => found.push([q, p]),
    );
    l.ask("l");
    vi.advanceTimersByTime(FILES_DEBOUNCE_MS - 1);
    l.ask("li");
    vi.advanceTimersByTime(FILES_DEBOUNCE_MS - 1);
    expect(asked).toEqual([]);
    vi.advanceTimersByTime(1);
    expect(asked).toEqual(["li"]);
    l.ask("lim");
    vi.advanceTimersByTime(FILES_DEBOUNCE_MS);
    expect(asked).toEqual(["li", "lim"]);
    // The older query answers last: too late, it is dropped.
    answers.get("lim")?.(["limit.go"]);
    answers.get("li")?.(["list.go"]);
    await vi.waitFor(() => expect(found).toEqual([["lim", ["limit.go"]]]));
    l.ask("x");
    l.stop();
    vi.advanceTimersByTime(FILES_DEBOUNCE_MS);
    expect(asked).toEqual(["li", "lim"]);
  });
});
