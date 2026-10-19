import type { AgentLine, Message, Session } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { findSide, isSide, sideDigest, sideMessage, sideQuestion, sideTitle } from "./sideChat";

const main: Session = { id: "m", title: "Fix the limiter", updated_at: "2026-10-19T10:00:00Z", busy: true, pending: 0, mode: "ask" };
const msg = (id: string, role: Message["role"], text: string, extra: Partial<Message> = {}): Message => ({
  id,
  session_id: "m",
  role,
  created_at: "2026-10-19T10:00:00Z",
  updated_at: "2026-10-19T10:00:00Z",
  finished: true,
  parts: [{ type: "text", text }],
  ...extra,
});

describe("side chat", () => {
  test("a side thread is named after its main thread and found by that name", () => {
    const side = { ...main, id: "s", title: sideTitle(main) };
    expect(isSide(side)).toBe(true);
    expect(isSide(main)).toBe(false);
    expect(findSide([main, side], main)?.id).toBe("s");
  });

  test("the digest names the agents, what waits and the recent work", () => {
    const roster: AgentLine[] = [{ id: "claude-1", name: "claude-1", kind: "claude", model: "opus", busy: true }];
    const d = sideDigest(
      main,
      roster,
      [{ id: "a", session_id: "m", agent: "claude-1", kind: "command", description: "Run", command: "go test ./...", created_at: "" }],
      [
        msg("1", "user", "why is it locked?"),
        msg("2", "assistant", "Looking at the bucket", {
          agent: "claude-1",
          finished: false,
          parts: [
            { type: "text", text: "Looking at the bucket" },
            { type: "tool_call", id: "t", name: "bash", summary: "go test ./pkg/..." },
          ],
        }),
      ],
    );
    expect(d).toContain("claude-1 (claude, opus, busy)");
    expect(d).toContain("go test ./...");
    expect(d).toContain("claude-1 (still writing): Looking at the bucket [tools: bash go test ./pkg/...]");
    expect(d).toContain("User: why is it locked?");
  });

  test("the window shows only the question", () => {
    const text = sideMessage("$claude", "what is it doing?", "Thread: x");
    expect(text.startsWith("$claude what is it doing?")).toBe(true);
    expect(sideQuestion(text)).toBe("what is it doing?");
    expect(sideQuestion(sideMessage("", "and now?", "y"))).toBe("and now?");
  });
});
