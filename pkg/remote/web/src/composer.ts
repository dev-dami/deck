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

const LEADING_MENTIONS = /^(?:\$[\w-]+[+*]?[ \t]*)+/;

// Every agent the message opens by addressing ("$claude $codex fix it" names
// both). A "+" or "*" suffix asks for a new member, not an existing one.
export function activeAgents(text: string): string[] {
  const lead = LEADING_MENTIONS.exec(text)?.[0] ?? "";
  return [...lead.matchAll(/\$([\w-]+)[+*]?/g)].map((m) => m[1] as string);
}

export function setAgents(text: string, agents: readonly string[]): string {
  const body = text.replace(LEADING_MENTIONS, "");
  return agents.length === 0 ? body : `${agents.map((a) => `$${a}`).join(" ")} ${body}`;
}

// Adds the agent to the message's addressees, or removes it when it is one.
export function toggleAgent(text: string, agent: string): string {
  const current = activeAgents(text);
  const next = current.includes(agent) ? current.filter((a) => a !== agent) : [...current, agent];
  return setAgents(text, next);
}

// A message that is only a mention has nothing to send.
export function canSend(text: string, imageCount: number): boolean {
  const body = text.replace(LEADING_MENTIONS, "").trim();
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
