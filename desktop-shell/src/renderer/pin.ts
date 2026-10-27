import type { Message } from "@web/protocol";
import { type DeckClient, serves } from "./client";
import { pinTitle } from "./highlights";
import { messageText } from "./messages";

// Whether the Deck serves pinning. Info.capabilities arrives after the
// room first shows, so the room's actions depend on this, not on the client.
export const canPin = (c: Pick<DeckClient, "capabilities" | "highlightAction">): boolean =>
  serves(c, "HighlightAction") && c.highlightAction !== undefined;

// Pins an agent message to Highlights as a Key finding titled by its first
// line; keyed by the message, so a second pin updates the same item.
export function pinMessage(c: Pick<DeckClient, "highlightAction">, m: Message): void {
  void c.highlightAction?.(m.session_id, {
    action: "pin",
    key: `pin:${m.id}`,
    kind: "finding",
    title: pinTitle(messageText(m)),
    source: { message_id: m.id, task_id: "", turn_id: "" },
  });
}
