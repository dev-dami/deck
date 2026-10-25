import type { AgentLine } from "@web/protocol";
import { SLASH_COMMANDS } from "./slash";

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
  // Index of the trigger: "$", "@" or "/".
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

// The "/command" being typed at the start of the message: the slash
// commands run only as a whole message (slash.ts).
export function slashAt(text: string, caret: number): Mention | null {
  const m = /^\/([\w-]*)$/.exec(text.slice(0, caret));
  return m === null ? null : { start: 0, query: m[1] ?? "" };
}

export function slashSuggestions(query: string): Suggestion[] {
  const q = query.toLowerCase();
  return SLASH_COMMANDS.filter((c) => c.name.startsWith(q)).map((c) => ({
    insert: `/${c.name}`,
    label: c.args === "" ? `/${c.name}` : `/${c.name} ${c.args}`,
    detail: c.detail,
    kind: null,
  }));
}

// The "@path" that ends at the caret, when it starts the text or follows
// whitespace.
export function fileAt(text: string, caret: number): Mention | null {
  const m = /(^|\s)@([^\s@`]*)$/.exec(text.slice(0, caret));
  if (m === null) return null;
  const query = m[2] ?? "";
  return { start: caret - query.length - 1, query };
}

// How well a path matches: its file name starts with the query, then holds
// it, then the path holds it, then the path holds its letters in order.
function pathScore(q: string, path: string): number | null {
  if (q === "") return 0;
  const p = path.toLowerCase();
  const base = p.slice(p.lastIndexOf("/") + 1);
  if (base.startsWith(q)) return 0;
  if (base.includes(q)) return 1;
  if (p.includes(q)) return 2;
  let i = 0;
  for (const ch of p) if (ch === q[i]) i++;
  return i === q.length ? 3 : null;
}

// The paths that match query, best first; ties keep the shorter path, then
// the given order.
export function matchPaths(query: string, paths: readonly string[], limit = 8): string[] {
  const q = query.toLowerCase();
  if (q === "") return paths.slice(0, limit);
  return paths
    .map((path, i) => ({ path, i, score: pathScore(q, path) }))
    .filter((x): x is { path: string; i: number; score: number } => x.score !== null)
    .sort((a, b) => a.score - b.score || a.path.length - b.path.length || a.i - b.i)
    .slice(0, limit)
    .map((x) => x.path);
}

export function fileSuggestions(query: string, paths: readonly string[]): Suggestion[] {
  return matchPaths(query, paths).map((p) => ({ insert: `\`${p}\``, label: p, detail: "changed", kind: null }));
}
