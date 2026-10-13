const LEADING_MENTION = /^\$[\w-]+[ \t]*/;

// Tapping an agent chip addresses the message to that agent by prefixing
// "$name ". Tapping another chip retargets instead of stacking mentions.
export function applyAgentChip(text: string, agent: string): string {
  return `$${agent} ${text.replace(LEADING_MENTION, "")}`;
}

export function activeAgent(text: string): string | null {
  const m = /^\$([\w-]+)/.exec(text);
  return m?.[1] ?? null;
}

// A message that is only a mention has nothing to send.
export function canSend(text: string, imageCount: number): boolean {
  const body = text.replace(LEADING_MENTION, "").trim();
  return body !== "" || imageCount > 0;
}

// Saving an edit needs a real change: the server rejects empty text, and an
// identical resend would only churn the transcript.
export function canSaveEdit(
  original: string,
  draft: string,
  online: boolean,
  saving: boolean,
): boolean {
  if (!online || saving) return false;
  const next = draft.trim();
  return next !== "" && next !== original.trim();
}
