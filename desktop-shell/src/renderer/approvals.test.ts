import type { AgentLine, Approval, Session } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { agentColour, agentKind, instanceOf } from "./agents";
import { agentStatus, approvalSummary, DECISIONS, groupApprovals } from "./approvals";

const approval = (id: string, session_id: string, agent: string, extra: Partial<Approval> = {}): Approval => ({
  id,
  session_id,
  agent,
  kind: "command",
  description: "Run a command",
  created_at: "2026-10-19T09:00:00Z",
  ...extra,
});
const session = (id: string, title: string): Session => ({
  id,
  title,
  updated_at: "2026-10-19T09:00:00Z",
  busy: false,
  pending: 0,
  mode: "ask",
});
const line = (id: string, busy: boolean): AgentLine => ({ id, name: id, kind: "claude", busy });

describe("agent status", () => {
  const approvals = [approval("a1", "s1", "claude-2")];
  test("an open approval makes the agent wait, even while busy", () => {
    expect(agentStatus(line("claude-2", true), "s1", approvals)).toBe("waiting");
  });
  test("an approval in another session does not count", () => {
    expect(agentStatus(line("claude-2", false), "s2", approvals)).toBe("idle");
  });
  test("otherwise busy or idle as the roster says", () => {
    expect(agentStatus(line("claude-1", true), "s1", approvals)).toBe("busy");
    expect(agentStatus(line("claude-1", false), "s1", approvals)).toBe("idle");
  });
});

describe("the approvals inbox", () => {
  const sessions = [session("s1", "One"), session("s2", "")];
  const list = [approval("a1", "s1", "claude-1"), approval("a2", "s2", "codex-1"), approval("a3", "s1", "codex-1")];

  test("groups by session in order of the oldest approval", () => {
    const g = groupApprovals(list, sessions, null);
    expect(g.map((x) => [x.sessionId, x.title, x.items.map((a) => a.id)])).toEqual([
      ["s1", "One", ["a1", "a3"]],
      ["s2", "Untitled", ["a2"]],
    ]);
  });

  test("puts the open session first", () => {
    expect(groupApprovals(list, sessions, "s2").map((x) => x.sessionId)).toEqual(["s2", "s1"]);
  });

  test("summarizes the command, the path or the description", () => {
    expect(approvalSummary(approval("a", "s", "x", { command: "go test ./..." }))).toBe("go test ./...");
    expect(approvalSummary(approval("a", "s", "x", { kind: "edit", path: "a/b.go" }))).toBe("a/b.go");
    expect(approvalSummary(approval("a", "s", "x", { kind: "other" }))).toBe("Run a command");
  });

  test("offers the three decisions the protocol knows", () => {
    expect(DECISIONS.map((d) => d.decision)).toEqual(["allow", "allow_session", "deny"]);
  });
});

describe("agent identity", () => {
  test("the kind comes from the roster, else from the id", () => {
    expect(agentKind("x9", [{ id: "x9", name: "x9", kind: "codex", busy: false }])).toBe("codex");
    expect(agentKind("codex-2", [])).toBe("codex");
    expect(agentKind("claude", [])).toBe("claude");
  });

  test("each instance of a kind gets its own stable shade", () => {
    expect(instanceOf("codex-1")).toBe(1);
    expect(instanceOf("codex-3")).toBe(3);
    expect(instanceOf("codex")).toBe(1);
    expect(agentColour("codex-1", "codex")).toBe("var(--kind-codex, var(--p-fg-base))");
    expect(agentColour("codex-2", "codex")).toContain("color-mix");
    expect(agentColour("codex-2", "codex")).toBe(agentColour("codex-2", "codex"));
    expect(agentColour("codex-2", "codex")).not.toBe(agentColour("codex-3", "codex"));
  });
});
