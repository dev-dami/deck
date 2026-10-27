import type { Message } from "@web/protocol";
import { describe, expect, test, vi } from "vitest";
import type { DeckClient } from "./client";
import { canPin, pinMessage } from "./pin";

const msg = { id: "m1", session_id: "s1", role: "assistant", parts: [{ type: "text", text: "## Limiter leaks\nper-IP state" }] } as unknown as Message;

type Fake = Pick<DeckClient, "capabilities" | "highlightAction">;

describe("canPin", () => {
  test("follows the capabilities as they arrive after the room shows", () => {
    let caps: readonly string[] = [];
    const c: Fake = { capabilities: () => caps, highlightAction: async () => true };
    expect(canPin(c)).toBe(false);
    caps = ["HighlightAction"];
    expect(canPin(c)).toBe(true);
  });

  test("needs a client that can act", () => {
    expect(canPin({ capabilities: () => ["HighlightAction"] })).toBe(false);
  });
});

describe("pinMessage", () => {
  test("pins as a Key finding keyed by the message, sourced to it", () => {
    const highlightAction = vi.fn(async () => true);
    pinMessage({ highlightAction }, msg);
    expect(highlightAction).toHaveBeenCalledWith("s1", {
      action: "pin",
      key: "pin:m1",
      kind: "finding",
      title: "Limiter leaks",
      source: { message_id: "m1", task_id: "", turn_id: "" },
    });
  });
});
