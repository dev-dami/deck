import type { QueuedItem } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { moveQueued, newQueued, queuedText, sendAction, sendHint, sendTitle } from "./queue";

const keys = (mod: boolean, shift: boolean) => ({ mod, shift });

describe("sendAction", () => {
  test("with Enter to send: Enter sends or queues, Ctrl/Cmd+Enter steers a busy agent", () => {
    expect(sendAction("enter", keys(false, false), false)).toBe("send");
    expect(sendAction("enter", keys(false, false), true)).toBe("queue");
    expect(sendAction("enter", keys(true, false), true)).toBe("steer");
    expect(sendAction("enter", keys(true, false), false)).toBe("send");
    expect(sendAction("enter", keys(false, true), true)).toBe("newline");
    expect(sendAction("enter", keys(true, true), true)).toBe("newline");
  });

  test("with Ctrl/Cmd+Enter to send: the inverse, Shift added to steer", () => {
    expect(sendAction("mod-enter", keys(false, false), true)).toBe("newline");
    expect(sendAction("mod-enter", keys(false, true), true)).toBe("newline");
    expect(sendAction("mod-enter", keys(true, false), false)).toBe("send");
    expect(sendAction("mod-enter", keys(true, false), true)).toBe("queue");
    expect(sendAction("mod-enter", keys(true, true), true)).toBe("steer");
    expect(sendAction("mod-enter", keys(true, true), false)).toBe("send");
  });

  test("the hint and the tooltip say which", () => {
    expect(sendHint("enter", false)).toBe("Enter queues · Ctrl+Enter steers");
    expect(sendHint("enter", true)).toBe("Enter queues · ⌘Enter steers");
    expect(sendHint("mod-enter", false)).toBe("Ctrl+Enter queues · Ctrl+Shift+Enter steers");
    expect(sendTitle("enter", false, false)).toBe("Send (Enter)");
    expect(sendTitle("mod-enter", true, false)).toBe("Send (⌘Enter)");
    expect(sendTitle("enter", false, true)).toBe("Queue for the busy agent (Enter). Enter queues · Ctrl+Enter steers.");
  });
});

const item = (id: string, agent = "claude-1"): QueuedItem => ({ id, agent, text: `text ${id}`, at: "2026-01-01T00:00:00Z" });

describe("the queue", () => {
  const items = [item("a"), item("b", "codex-1"), item("c")];

  test("moving an item cancels and queues again from the first moved place", () => {
    expect(moveQueued(items, 1, -1)).toEqual({ cancel: items, resend: [items[1], items[0], items[2]] });
    expect(moveQueued(items, 1, 1)).toEqual({ cancel: [items[1], items[2]], resend: [items[2], items[1]] });
  });

  test("the ends do not move", () => {
    expect(moveQueued(items, 0, -1)).toBeNull();
    expect(moveQueued(items, 2, 1)).toBeNull();
    expect(moveQueued([], 0, 1)).toBeNull();
  });

  test("an item queues again for its agent", () => {
    expect(queuedText(item("a"))).toBe("$claude-1 text a");
  });

  test("a send's own items are the new ones", () => {
    expect(newQueued(new Set(["a"]), items).map((q) => q.id)).toEqual(["b", "c"]);
  });
});
