import type { Message } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { firstLine, foldsBody, handoffOf, subTasks } from "./handoff";

describe("handoffOf", () => {
  test("reads the recipients and the message", () => {
    expect(handoffOf("→ $codex $claude-2\n\nreview the limiter")).toEqual({ to: ["codex", "claude-2"], body: "review the limiter", task: null, sub: null });
    expect(handoffOf("→ @charles\n\nhi")?.to).toEqual(["charles"]);
  });

  test("reads a task's report", () => {
    const h = handoffOf('→ $claude\n\ntask t3 "Prune idle buckets" done: added prune() and a test\nsecond line');
    expect(h?.task).toEqual({ id: "t3", title: "Prune idle buckets", state: "done", result: "added prune() and a test\nsecond line" });
    expect(handoffOf('→ $claude\n\ntask t5 "Fix \\"it\\"" failed')?.task).toMatchObject({ id: "t5", state: "failed", result: "" });
  });

  test("reads a sub-thread's report", () => {
    const h = handoffOf('→ $claude · sub-thread ses_42\n\n[sub-thread "Second opinion"] Yes, keyed by address.');
    expect(h?.sub).toEqual({ id: "ses_42", title: "Second opinion" });
    expect(h?.body).toBe("Yes, keyed by address.");
    expect(h?.to).toEqual(["claude"]);
  });

  test("anything else is prose", () => {
    expect(handoffOf("The limiter → fixed")).toBeNull();
    expect(handoffOf("→ nobody\n\ntext")).toBeNull();
    expect(handoffOf("")).toBeNull();
  });

  test("folds long bodies only", () => {
    expect(foldsBody("short")).toBe(false);
    expect(foldsBody("x".repeat(500))).toBe(true);
    expect(foldsBody("a\nb\nc\nd\ne\nf\ng")).toBe(true);
  });
});

describe("subTasks", () => {
  const AT = "2026-10-30T10:00:00Z";
  const m = (agent: string, text: string): Message => ({ id: text, session_id: "s", role: "assistant", agent, created_at: AT, updated_at: AT, finished: true, parts: [{ type: "text", text }] });

  test("what each subagent was first asked, as one line", () => {
    const tasks = subTasks(
      [
        m("claude-1", "→ $codex-2\n\n## Review\nCheck the limiter keys by address."),
        m("claude-1", "→ $codex-2\n\nAnd the docs."),
        m("claude-1", "→ $codex-1\n\nA peer's task."),
        m("codex-2", "→ $claude-1\n\nLooks right."),
      ],
      (name) => name === "codex-2",
    );
    expect([...tasks]).toEqual([["codex-2", "Review"]]);
  });

  test("firstLine skips blank lines and markdown marks", () => {
    expect(firstLine("\n\n- **Done**: two files")).toBe("Done: two files");
    expect(firstLine("**codex-1** on it. You asked: _check `the` flags_. snake_case stays")).toBe("codex-1 on it. You asked: check the flags. snake_case stays");
    expect(firstLine("")).toBe("");
  });
});
