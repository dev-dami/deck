import { setAgents } from "@web/composer";
import type { Message } from "@web/protocol";

// A message's text as markdown: its text parts, without thinking or tools.
export function messageText(m: Message): string {
  return m.parts
    .filter((p) => p.type === "text" && (p.text ?? "") !== "")
    .map((p) => p.text ?? "")
    .join("\n\n");
}

// The last agent reply of a thread, the one that gets Retry and Continue.
export function lastReplyId(messages: readonly Message[]): string | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m === undefined || m.role === "system") continue;
    return m.role === "assistant" ? m.id : null;
  }
  return null;
}

// Retry: the person's message before the reply, sent again to the agent
// that replied.
export function retryText(messages: readonly Message[], replyId: string): string | null {
  const i = messages.findIndex((m) => m.id === replyId);
  const agent = messages[i]?.agent;
  if (i < 0 || agent === undefined) return null;
  for (let j = i - 1; j >= 0; j--) {
    const m = messages[j];
    if (m?.role !== "user") continue;
    const text = messageText(m).trim();
    return text === "" ? null : setAgents(text, [agent]);
  }
  return null;
}
