import { describe, expect, it } from "vitest";
import { branchId, buildGraph, capsuleId, MAIN } from "./graph";
import { buildLanes } from "./lanes";
import { branchesByAgent, capsuleOfTree, landCounts, shortRef, toWork, toWorkAction, treeState, type Worktree } from "./worktrees";

const wire = {
  worktrees: [
    {
      name: "s1-claude",
      branch: "deck/s1-claude",
      path: "/w/s1-claude",
      kind: "agent",
      owner: "claude",
      owner_name: "claude",
      task: "t2",
      working: true,
      fork: "a".repeat(40),
      base_ref: "main",
      ahead: 2,
      behind: 1,
      dirty: 3,
      files: ["a.go", "b.go"],
    },
    { name: "s1-refactor", branch: "deck/s1-refactor", kind: "tree", base_ref: "main", files: [], landed: true, landed_at: 5 },
  ],
  capsules: [
    {
      name: "s1-claude",
      source: "worktree",
      worktree: "deck/s1-claude",
      authors: ["claude"],
      state: "landed",
      tests: "pass",
      versions: [{ id: "blake3:1", files: 2, add: 1, mod: 1, commits: [{ sha: "abc", subject: "fix" }], commits_total: 1 }],
      landings: [{ version: 1, into: "main", at: 9 }],
    },
    { name: "wip", source: "diff", versions: [{ id: "blake3:2", files: 1 }] },
  ],
};

const tree = (over: Partial<Worktree>): Worktree => ({ ...toWork({ worktrees: [{}] }).worktrees[0]!, ...over });

describe("worktrees", () => {
  it("maps Work from the wire", () => {
    const w = toWork(wire);
    expect(w.worktrees[0]).toMatchObject({ name: "s1-claude", ownerName: "claude", ahead: 2, behind: 1, dirty: 3, working: true, baseRef: "main" });
    expect(w.capsules[0]!.versions[0]!.commits[0]).toEqual({ sha: "abc", subject: "fix", author: "" });
    expect(w.capsules[1]!.state).toBe("open");
    expect(toWork(null)).toEqual({ worktrees: [], capsules: [] });
  });

  it("maps an action's answer, an unknown state as a conflict", () => {
    const a = toWorkAction({ conflicts: ["a.go"], files: [{ path: "a.go", state: "conflict" }, { path: "b.go", state: "odd" }, { path: "c.go", state: "merge" }], checked: true });
    expect(a.conflicts).toEqual(["a.go"]);
    expect(landCounts(a.files)).toEqual({ fits: 0, applied: 0, merge: 1, conflict: 2 });
    expect(a.worktree).toBeNull();
  });

  it("says where a worktree stands", () => {
    expect(treeState(tree({ error: "gone" }))).toBe("gone");
    expect(treeState(tree({ working: true, behind: 2 }))).toBe("working");
    expect(treeState(tree({ landed: true }))).toBe("landed");
    expect(treeState(tree({ behind: 1, files: ["a"] }))).toBe("behind");
    expect(treeState(tree({ dirty: 1 }))).toBe("changed");
    expect(treeState(tree({}))).toBe("clean");
    expect(shortRef("b".repeat(40))).toBe("bbbbbbbb");
    expect(shortRef("main")).toBe("main");
  });

  it("finds an agent's branch and a worktree's capsule", () => {
    const w = toWork(wire);
    expect(branchesByAgent(w).get("claude")).toBe("deck/s1-claude");
    expect(capsuleOfTree(w, w.worktrees[0]!)?.name).toBe("s1-claude");
    expect(capsuleOfTree(w, w.worktrees[1]!)).toBeUndefined();
  });

  it("puts worktrees and capsules on the Work Graph", () => {
    const g = buildGraph({
      roster: [{ id: "claude", name: "claude", kind: "claude", busy: true }],
      crew: null,
      board: null,
      files: [{ path: "a.go", added: 1, removed: 0 }],
      lines: toWork(wire),
    });
    const kinds = new Map(g.nodes.map((n) => [n.id, n.kind]));
    expect(kinds.get(branchId("s1-claude"))).toBe("branch");
    expect(kinds.get(capsuleId("wip"))).toBe("capsule");
    expect(kinds.get(MAIN)).toBe("main");
    const edges = g.edges.map((e) => `${e.kind}:${e.from}>${e.to}`);
    expect(edges).toContain(`works:agent:claude>${branchId("s1-claude")}`);
    expect(edges).toContain(`bundles:${branchId("s1-claude")}>${capsuleId("s1-claude")}`);
    expect(edges).toContain(`lands:${capsuleId("s1-claude")}>${MAIN}`);
    // Worktrees, then capsules, then main, then the files, left to right.
    const layer = (id: string) => g.nodes.find((n) => n.id === id)!.layer;
    expect(layer(branchId("s1-claude"))).toBeLessThan(layer(capsuleId("s1-claude")));
    expect(layer(capsuleId("s1-claude"))).toBeLessThan(layer(MAIN));
    expect(layer(MAIN)).toBeLessThan(layer("file:a.go"));
    expect(g.layers.map((l) => l.label)).toContain("Worktrees");
  });

  it("gives an agent on its own worktree a branch on its lane", () => {
    const lanes = buildLanes({
      roster: [{ id: "claude", name: "claude", kind: "claude", busy: false }],
      agents: [{ name: "claude", kind: "claude", branch: "deck/s1-claude" } as never],
      shares: {},
      waiting: [],
    });
    expect(lanes.map((l) => l.branch)).toEqual(["", "deck/s1-claude"]);
  });
});
