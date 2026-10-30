import type { Message } from "@web/protocol";
import { expect, test } from "vitest";
import { lastReplyId, messageText, retryText } from "./messages";

const AT = "2026-10-25T10:00:00Z";

function msg(id: string, role: Message["role"], parts: Message["parts"], agent?: string): Message {
  return { id, session_id: "s", role, created_at: AT, updated_at: AT, finished: true, parts, ...(agent === undefined ? {} : { agent }) };
}

const thread: Message[] = [
  msg("u1", "user", [{ type: "text", text: "$claude $codex Why does the limiter lock everyone out?" }]),
  msg("a1", "assistant", [
    { type: "reasoning", text: "Read the limiter." },
    { type: "tool_call", id: "t", name: "bash", summary: "go test" },
    { type: "text", text: "It counts **every** address." },
    { type: "text", text: "- key it by `addr`" },
  ], "claude-1"),
  msg("a2", "assistant", [{ type: "text", text: "I agree." }], "codex-1"),
  msg("n", "system", [{ type: "text", text: "codex-1 finished" }]),
];

test("messageText keeps the text parts as markdown", () => {
  expect(messageText(thread[1]!)).toBe("It counts **every** address.\n\n- key it by `addr`");
  expect(messageText(msg("x", "user", [{ type: "image", image: { name: "a.png", mime: "image/png" } }]))).toBe("");
});

test("the last reply skips system notes and is none after the person's turn", () => {
  expect(lastReplyId(thread)).toBe("a2");
  expect(lastReplyId([...thread, msg("u2", "user", [{ type: "text", text: "more" }])])).toBeNull();
  expect(lastReplyId([])).toBeNull();
});

test("retry sends the person's message again to the agent that replied", () => {
  expect(retryText(thread, "a2")).toBe("$codex-1 Why does the limiter lock everyone out?");
  expect(retryText(thread, "a1")).toBe("$claude-1 Why does the limiter lock everyone out?");
  expect(retryText(thread, "nope")).toBeNull();
  expect(retryText(thread.slice(1), "a1")).toBeNull();
  // A sub-thread's answer to its parent agent's brief: nothing of the person's.
  const brief = msg("b1", "user", [{ type: "text", text: "[deck] From claude-1, the agent that opened this sub-thread:\n\ncheck it" }], "claude-1");
  expect(retryText([brief, msg("a3", "assistant", [{ type: "text", text: "ok" }], "codex-1")], "a3")).toBeNull();
});
