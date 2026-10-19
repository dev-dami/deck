import type { AgentLine } from "@web/protocol";

// The `$` popup of the composer, after Deck's own addressing rules
// (internal/deck/session/address.go): "$codex" reaches a free codex or
// starts one, "$codex+" always starts another, "$codex*" reaches every
// codex, "$claude-2" one member by id, "$all" every member.

export interface Suggestion {
  insert: string;
  label: string;
  detail: string;
  kind: string | null;
}

export interface Mention {
  // Index of the "$".
  start: number;
  query: string;
}

// The "$word" that ends at the caret, when it starts the text or follows
// whitespace; null otherwise.
export function mentionAt(text: string, caret: number): Mention | null {
  const before = text.slice(0, caret);
  const m = /(^|\s)\$([\w-]*[+*]?)$/.exec(before);
  if (m === null) return null;
  const query = m[2] ?? "";
  return { start: caret - query.length - 1, query };
}

export function suggestions(query: string, roster: AgentLine[], kinds: string[]): Suggestion[] {
  const out: Suggestion[] = [];
  const allKinds = [...new Set([...kinds, ...roster.map((l) => l.kind)])];
  for (const l of roster) {
    out.push({
      insert: `$${l.id}`,
      label: l.id,
      detail: `${l.kind}${l.busy ? " · busy" : ""}`,
      kind: l.kind,
    });
  }
  for (const k of allKinds) {
    const members = roster.filter((l) => l.kind === k).length;
    out.push({
      insert: `$${k}`,
      label: k,
      detail: members === 0 ? `start ${k}` : `a free ${k}`,
      kind: k,
    });
    out.push({ insert: `$${k}+`, label: `${k}+`, detail: `start another ${k}`, kind: k });
    if (members > 1) {
      out.push({ insert: `$${k}*`, label: `${k}*`, detail: `every ${k}`, kind: k });
    }
  }
  if (roster.length > 1) out.push({ insert: "$all", label: "all", detail: "every agent", kind: null });
  const q = query.toLowerCase();
  const seen = new Set<string>();
  return out.filter((s) => {
    const key = s.insert.toLowerCase();
    if (seen.has(key) || !key.slice(1).startsWith(q)) return false;
    seen.add(key);
    return true;
  });
}

// Replaces the mention under the caret with s and a space.
export function applySuggestion(
  text: string,
  m: Mention,
  s: Suggestion,
): { text: string; caret: number } {
  const end = m.start + 1 + m.query.length;
  const rest = text.slice(end).replace(/^[ \t]*/, "");
  const head = `${text.slice(0, m.start)}${s.insert} `;
  return { text: head + rest, caret: head.length };
}

// The add-agent menu: "$kind+ " first, so the message starts another agent
// of that kind. A mention right after another one still counts.
export function addAgentMention(draft: string, kind: string): string {
  return `$${kind}+ ${draft.replace(/^\s+/, "")}`;
}
