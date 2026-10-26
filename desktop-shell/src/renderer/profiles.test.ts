import type { AgentLine } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { agentRole } from "./agents";
import { cleanProfile, emptyProfile, parsePaths, type Profile, reachable, sortProfiles, validateProfile } from "./profiles";

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

  test("@ reaches names of letters, digits, - and _", () => {
    expect(reachable("charles")).toBe(true);
    expect(reachable("ada_2-b")).toBe(true);
    expect(reachable("dr.who")).toBe(false);
    expect(reachable("josé")).toBe(false);
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

  test("a member's role, by id or by name", () => {
    const roster: AgentLine[] = [
      { id: "claude-2", name: "charles", kind: "claude", busy: false, profile: "charles", role: "reviewer" },
      { id: "codex-1", name: "codex-1", kind: "codex", busy: false },
    ];
    expect(agentRole("claude-2", roster)).toBe("reviewer");
    expect(agentRole("charles", roster)).toBe("reviewer");
    expect(agentRole("codex-1", roster)).toBe("");
    expect(agentRole("gone-1", roster)).toBe("");
  });
});
