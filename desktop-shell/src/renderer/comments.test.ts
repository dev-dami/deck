import type { AgentLine, Message } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { appendComment, commentBlock, countComments, fileAuthor, samePath } from "./comments";

const AT = "2026-10-25T10:00:00Z";

function msg(id: string, agent: string | undefined, parts: Message["parts"]): Message {
  return { id, session_id: "s", role: agent === undefined ? "user" : "assistant", created_at: AT, updated_at: AT, finished: true, parts, ...(agent === undefined ? {} : { agent }) };
}

const ROSTER: AgentLine[] = [
  { id: "codex-1", name: "codex-1", kind: "codex", busy: false },
  { id: "claude-1", name: "claude-1", kind: "claude", busy: false },
];

describe("a line comment", () => {
  test("the block names the agent, the file and line, and quotes the line", () => {
    const b = commentBlock("claude-1", { path: "pkg/remote/auth/limit.go", line: 13, text: "\tglobal.fail()" }, " Why global? \n");
    expect(b).toBe("$claude-1 In `pkg/remote/auth/limit.go:13`:\n> global.fail()\nWhy global?");
    expect(commentBlock(null, { path: "a.go", line: 1, text: "x" }, "y")).toBe("In `a.go:1`:\n> x\ny");
  });

  test("comments append to the draft and are counted", () => {
    const one = appendComment("", commentBlock("claude-1", { path: "a.go", line: 3, text: "x" }, "first"));
    const two = appendComment(`${one}\n`, commentBlock("codex-1", { path: "b.go", line: 9, text: "y" }, "second"));
    expect(two).toBe("$claude-1 In `a.go:3`:\n> x\nfirst\n\n$codex-1 In `b.go:9`:\n> y\nsecond");
    expect(countComments(two)).toBe(2);
    expect(countComments("Fix it. In `a.go:3`: is wrong")).toBe(0);
    expect(countComments("")).toBe(0);
  });

  test("paths match across a/ b/ prefixes and absolute paths", () => {
    expect(samePath("b/pkg/a.go", "pkg/a.go")).toBe(true);
    expect(samePath("/home/me/deck/pkg/a.go", "pkg/a.go")).toBe(true);
    expect(samePath("pkg/ba.go", "a.go")).toBe(false);
    expect(samePath("", "a.go")).toBe(false);
  });
});

describe("who a line comment goes to", () => {
  const thread = [
    msg("u1", undefined, [{ type: "text", text: "$claude $codex fix the limiter" }]),
    msg("a1", "claude-1", [
      { type: "tool_call", id: "t1", name: "edit", summary: "pkg/remote/auth/limit.go" },
      { type: "tool_result", tool_call_id: "t1", name: "edit", output: "ok" },
    ]),
    msg("a2", "codex-1", [
      { type: "tool_call", id: "t2", name: "apply_patch", summary: "patch" },
      {
        type: "tool_result",
        tool_call_id: "t2",
        name: "apply_patch",
        diff: "--- a/pkg/remote/auth/limit_test.go\n+++ b/pkg/remote/auth/limit_test.go\n@@ -1 +1 @@\n-a\n+b\n",
      },
    ]),
    msg("a3", "codex-1", [{ type: "tool_call", id: "t3", name: "read", summary: "pkg/remote/auth/limit.go" }]),
  ];

  test("the last agent that changed the file, by an edit tool or a diff", () => {
    expect(fileAuthor(thread, ROSTER, "pkg/remote/auth/limit.go")).toBe("claude-1");
    expect(fileAuthor(thread, ROSTER, "pkg/remote/auth/limit_test.go")).toBe("codex-1");
  });

  test("else the room's first agent, else the first that wrote", () => {
    expect(fileAuthor(thread, ROSTER, "README.md")).toBe("codex-1");
    expect(fileAuthor(thread, [], "README.md")).toBe("claude-1");
    expect(fileAuthor([], [], "README.md")).toBeNull();
  });
});
