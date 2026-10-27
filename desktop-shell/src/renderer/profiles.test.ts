import { describe, expect, test } from "vitest";
import {
  cleanProfile,
  copyProfile,
  effortsFor,
  emptyProfile,
  PROFILE_SWATCHES,
  parsePaths,
  type Profile,
  sortProfiles,
  validateProfile,
} from "./profiles";

const kinds = ["claude", "codex", "opencode", "antigravity"];
const profile = (over: Partial<Profile>): Profile => ({ ...emptyProfile("claude", "project"), name: "charles", ...over });
const charles = profile({});
const globalAda = profile({ name: "ada", kind: "codex", scope: "global" });
const isColour = (v: string) => /^#[0-9a-f]{3,8}$/i.test(v) || v === "tomato";
const check = (p: Profile, editing: { name: string; scope: Profile["scope"] } | null = null) =>
  validateProfile(p, { kinds, teammates: ["alice"], profiles: [charles, globalAda], editing, isColour });

describe("profile validation (agents-and-mentions.md, section 1)", () => {
  test("a good profile has no errors", () => {
    expect(check(profile({ name: "grace", role: "reviewer", color: "#c96" }))).toEqual({});
  });

  test("the name is required and holds no spaces or sigils", () => {
    expect(check(profile({ name: "  " })).name).toBe("A profile needs a name.");
    for (const bad of ["ada lovelace", "a@b", "$x", "a/b", "a*", "a+", "#x", "tab\tx"]) {
      expect(check(profile({ name: bad })).name).toBe("No spaces, and none of @ $ / * + #.");
    }
  });

  test("Deck's words, the kinds and teammates are not names", () => {
    expect(check(profile({ name: "ALL" })).name).toMatch(/reserved by Deck/);
    expect(check(profile({ name: "user" })).name).toMatch(/reserved by Deck/);
    expect(check(profile({ name: "deck" })).name).toMatch(/reserved by Deck/);
    expect(check(profile({ name: "Codex" })).name).toMatch(/is an agent kind/);
    expect(check(profile({ name: "Alice" })).name).toBe("Alice is a teammate. Pick another name.");
  });

  test("a name is unique within its scope, case-insensitively", () => {
    expect(check(profile({ name: "Charles" })).name).toBe("There is already a project profile named Charles.");
    // A project profile may override a global one of the same name.
    expect(check(profile({ name: "ada" })).name).toBeUndefined();
    expect(check(profile({ name: "ada", scope: "global" })).name).toMatch(/already a global profile/);
    // A profile being edited keeps its own name.
    expect(check(charles, { name: "charles", scope: "project" })).toEqual({});
  });

  test("the kind, one-line role and voice, and the colour", () => {
    expect(check(profile({ kind: "" })).kind).toBe("Pick the agent it runs.");
    expect(check(profile({ kind: "gpt" })).kind).toBe("Pick the agent it runs.");
    expect(check(profile({ role: "reviews\nand fixes" })).role).toBe("Keep the role to one line.");
    expect(check(profile({ voice: "dry\nterse" })).voice).toBe("Keep the voice to one line.");
    expect(check(profile({ color: "not a colour" })).color).toBe("Not a CSS colour.");
    expect(check(profile({ color: " tomato " })).color).toBeUndefined();
    expect(check(profile({ color: "" })).color).toBeUndefined();
  });

  test("a name is what @ reads: letters, digits, - and _, at most 32", () => {
    expect(check(profile({ name: "ada_2-b" })).name).toBeUndefined();
    expect(check(profile({ name: "dr.who" })).name).toBe("Only letters, digits, - and _, so that @dr.who reaches it.");
    expect(check(profile({ name: "josé" })).name).toMatch(/^Only letters/);
    expect(check(profile({ name: "a".repeat(32) })).name).toBeUndefined();
    expect(check(profile({ name: "a".repeat(33) })).name).toBe("At most 32 characters.");
  });

  test("paths are globs Deck can read; a colour holds nothing that ends a declaration", () => {
    expect(check(profile({ paths: ["src/**", "docs/[a-c]*.md", "a\\[b"] })).paths).toBeUndefined();
    expect(check(profile({ paths: ["src/**", "docs/[a-c.md"] })).paths).toBe("docs/[a-c.md is not a glob Deck can read.");
    expect(check(profile({ paths: ["a\\"] })).paths).toMatch(/not a glob/);
    expect(check(profile({ color: "#c96;x" })).color).toBe("Not a CSS colour.");
  });
});

describe("profiles as saved and listed", () => {
  test("paths split on lines and commas; fields are trimmed", () => {
    expect(parsePaths("src/**\n docs/*.md, ,pkg/a.go\n")).toEqual(["src/**", "docs/*.md", "pkg/a.go"]);
    expect(cleanProfile(profile({ name: " charles ", role: " reviewer ", paths: [" a ", ""] }))).toMatchObject({ name: "charles", role: "reviewer", paths: ["a"] });
  });

  test("project profiles first, then global, each by name", () => {
    const list = [globalAda, profile({ name: "zed" }), charles, profile({ name: "bob", scope: "global" })];
    expect(sortProfiles(list).map((p) => `${p.scope}:${p.name}`)).toEqual(["project:charles", "project:zed", "global:ada", "global:bob"]);
  });
});

describe("the editor's helpers", () => {
  test("a copy takes the first free name in its scope, within the limit", () => {
    const others = [charles, profile({ name: "charles-2" }), globalAda];
    expect(copyProfile(charles, others)).toMatchObject({ name: "charles-3", kind: "claude", scope: "project" });
    // Another scope's names do not count.
    expect(copyProfile(globalAda, [globalAda, profile({ name: "ada-2" })]).name).toBe("ada-2");
    const long = profile({ name: "x".repeat(32) });
    const copy = copyProfile(long, [long]);
    expect(copy.name).toBe(`${"x".repeat(30)}-2`);
    expect(check(copy)).toEqual({});
  });

  test("efforts: the model's own, else every effort of the kind", () => {
    const models = [
      { id: "opus", label: "Opus", efforts: ["low", "high", "xhigh"] },
      { id: "fast", label: "Fast", efforts: ["low", "medium"] },
      { id: "plain", label: "Plain" },
    ];
    expect(effortsFor(models, "fast")).toEqual(["low", "medium"]);
    expect(effortsFor(models, "plain")).toEqual([]);
    expect(effortsFor(models, "")).toEqual(["low", "high", "xhigh", "medium"]);
    expect(effortsFor(null, "opus")).toEqual([]);
  });

  test("every swatch is a colour Deck takes", () => {
    for (const color of PROFILE_SWATCHES) {
      expect(validateProfile(profile({ color }), { kinds, profiles: [], isColour: () => true }).color).toBeUndefined();
    }
  });
});
