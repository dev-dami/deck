import type { QueuedItem } from "@web/protocol";
import type { SendKey } from "./prefs";

// Queue or steer. A message to a busy agent waits in the engine's queue
// until its turn ends (Queue); a steer takes it out of the queue and gives
// it to the agent in its running turn (Steer).

export type SendAction = "send" | "queue" | "steer" | "newline";

// What Enter does in the composer, by the send key preference, the keys
// held and whether an agent works. "newline" leaves the key to the text.
export function sendAction(sendKey: SendKey, keys: { mod: boolean; shift: boolean }, busy: boolean): SendAction {
  const { mod, shift } = keys;
  if (sendKey === "enter") {
    if (shift) return "newline";
    if (mod) return busy ? "steer" : "send";
    return busy ? "queue" : "send";
  }
  if (!mod) return "newline";
  if (shift) return busy ? "steer" : "send";
  return busy ? "queue" : "send";
}

const modName = (mac: boolean) => (mac ? "⌘" : "Ctrl+");

// The strip's line while an agent works.
export function sendHint(sendKey: SendKey, mac: boolean): string {
  const m = modName(mac);
  return sendKey === "enter" ? `Enter queues · ${m}Enter steers` : `${m}Enter queues · ${m}Shift+Enter steers`;
}

// The send button's tooltip.
export function sendTitle(sendKey: SendKey, mac: boolean, busy: boolean): string {
  const key = sendKey === "enter" ? "Enter" : `${modName(mac)}Enter`;
  return busy ? `Queue for the busy agent (${key}). ${sendHint(sendKey, mac)}.` : `Send (${key})`;
}

// The text that queues an item again for the same agent. The engine keeps
// an item's text without its mentions.
export function queuedText(q: QueuedItem): string {
  return `$${q.agent} ${q.text}`;
}

// Moving item i one place up (-1) or down (1). The engine keeps the queue
// in the order the messages came, so the items from the first moved place
// on are cancelled and queued again in the new order. null: no move.
export function moveQueued(items: readonly QueuedItem[], i: number, dir: -1 | 1): { cancel: QueuedItem[]; resend: QueuedItem[] } | null {
  const j = i + dir;
  const a = items[i];
  const b = items[j];
  if (a === undefined || b === undefined) return null;
  const from = Math.min(i, j);
  const next = [...items];
  next[i] = b;
  next[j] = a;
  return { cancel: items.slice(from), resend: next.slice(from) };
}

// The items a send added to the queue: those not there before it.
export function newQueued(before: ReadonlySet<string>, items: readonly QueuedItem[]): QueuedItem[] {
  return items.filter((q) => !before.has(q.id));
}
