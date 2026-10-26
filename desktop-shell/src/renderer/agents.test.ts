import { describe, expect, test } from "vitest";
import { agentRole } from "./agents";

describe("agentRole", () => {
  const roster = [
    { id: "charles", name: "charles", kind: "claude", busy: false, role: "reviewer", profile: "charles" },
    { id: "claude-1", name: "claude-1", kind: "claude", busy: false },
  ];

  test("finds a member's role by id or by name", () => {
    expect(agentRole("charles", roster)).toBe("reviewer");
    expect(agentRole("c1", [{ id: "c1", name: "ada", role: "tests" }])).toBe("tests");
    expect(agentRole("ada", [{ id: "c1", name: "ada", role: "tests" }])).toBe("tests");
  });

  test("falls back to the next list, and is empty for a member with none", () => {
    expect(agentRole("ada", roster, [{ name: "ada", role: "implements the plan" }])).toBe("implements the plan");
    expect(agentRole("claude-1", roster, [{ name: "claude-1", role: "" }])).toBe("");
    expect(agentRole("nobody", roster, undefined)).toBe("");
  });
});
