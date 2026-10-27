import type { AgentLine } from "./protocol";

// The composer's pickers, after Deck's addressing rules
// (internal/deck/session/address.go and docs/design/agents-and-mentions.md):
// "@" names an agent or a person, "$" is the older sigil for agents, and "#"
// picks a file. "@codex" reaches a free codex or starts one, "@claude-2" one
// member, "@charles" a profile's agent, "@all" every member. Nothing typed
// starts a second agent or a race: "/race" does, and agents start helpers
// themselves. The desktop and the phone share these.

export interface Suggestion {
  insert: string;
  label: string;
  // The role, or what the pick does.
  detail: string;
  // The agent kind whose glyph and hue the row wears; null for a person
  // or "all".
  kind: string | null;
  // A profile's own colour, worn in place of the kind's hue.
  color?: string;
}

export interface Mention {
  // Index of the trigger ("$", "@", "#" or "/").
  start: number;
  query: string;
}

// A saved profile the "@" picker offers (ListProfiles).
export interface ProfileRef {
  name: string;
  kind: string;
  role?: string;
  color?: string;
}

// A teammate "@" reaches where Deck routes a message to a person.
export interface Person {
  name: string;
  role?: string;
}

// Which list a picker shows: agents ("$"), agents and people ("@"), files
// ("#", or "@" before something path-like).
export type PickerType = "$" | "@" | "#";

export interface Picked {
  type: PickerType;
  m: Mention;
}

// A query that reads as a path: it holds a "/" or ends in ".ext". "@" before
// one still offers files, so "@src/" and "@main.go" keep working.
export function pathLike(query: string): boolean {
  return query.includes("/") || /\.[A-Za-z0-9]+$/.test(query);
}

// The bytes a name may hold, as the engine reads them (isNameByte).
const NAME = /^[\w-]*$/;

// The "$word" that ends at the caret, when it starts the text or follows
// whitespace; null otherwise.
export function mentionAt(text: string, caret: number): Mention | null {
  const m = /(^|\s)\$([\w-]*)$/.exec(text.slice(0, caret));
  if (m === null) return null;
  const query = m[2] ?? "";
  return { start: caret - query.length - 1, query };
}

// The "@word" or "#word" that ends at the caret, when it starts the text or
// follows whitespace. "me@example" and "a#b" are not one.
function sigilAt(sigil: "@" | "#", text: string, caret: number): Mention | null {
  const re = sigil === "@" ? /(^|\s)@([^\s@`]*)$/ : /(^|\s)#([^\s#`]*)$/;
  const m = re.exec(text.slice(0, caret));
  if (m === null) return null;
  const query = m[2] ?? "";
  return { start: caret - query.length - 1, query };
}

// The "@path" that ends at the caret: an "@" before something path-like.
export function fileAt(text: string, caret: number): Mention | null {
  const m = sigilAt("@", text, caret);
  return m !== null && pathLike(m.query) ? m : null;
}

// The "#word" that ends at the caret: the context picker.
export const contextAt = (text: string, caret: number): Mention | null => sigilAt("#", text, caret);

// The "@name" that ends at the caret: the agents and people picker.
export function nameAt(text: string, caret: number): Mention | null {
  const m = sigilAt("@", text, caret);
  return m !== null && !pathLike(m.query) && NAME.test(m.query) ? m : null;
}

// The picker the text before the caret opens, if any. files says whether
// the composer can list files at all; names whether the Deck reads "@" as
// an agent's sigil. An older Deck reads only "$", so there "@" keeps
// picking files, as it did, and never inserts a name the Deck would take
// for text.
export function pickerAt(text: string, caret: number, files: boolean, names = true): Picked | null {
  if (files) {
    const ctx = contextAt(text, caret) ?? (names ? fileAt(text, caret) : sigilAt("@", text, caret));
    if (ctx !== null) return { type: "#", m: ctx };
  }
  const named = names ? nameAt(text, caret) : null;
  if (named !== null) return { type: "@", m: named };
  const agent = mentionAt(text, caret);
  return agent === null ? null : { type: "$", m: agent };
}

const busyDetail = (l: AgentLine): string =>
  [l.role ?? "", l.kind, l.busy ? "busy" : ""].filter((x) => x !== "").join(" · ");

// What sigil + query can name: the thread's members; for "@" the profiles
// with no member yet and the teammates; each kind; then "all". Filtered by
// prefix, case-insensitively, each once.
export function suggestions(
  query: string,
  roster: AgentLine[],
  kinds: string[],
  {
    sigil = "$",
    profiles = [],
    people = [],
  }: {
    sigil?: "$" | "@";
    profiles?: readonly ProfileRef[] | undefined;
    people?: readonly Person[] | undefined;
  } = {},
): Suggestion[] {
  const out: Suggestion[] = [];
  const at = sigil === "@";
  const allKinds = [...new Set([...kinds, ...roster.map((l) => l.kind)])];
  for (const l of roster) {
    // "$" keeps naming members by id; "@" by the name they are shown by.
    const name = at ? l.name || l.id : l.id;
    out.push({
      insert: `${sigil}${name}`,
      label: name,
      detail: busyDetail(l),
      kind: l.kind,
      ...(l.color ? { color: l.color } : {}),
    });
  }
  // "@" talks to someone: the named come before the kinds.
  if (at) {
    const taken = new Set(roster.flatMap((l) => [l.name.toLowerCase(), l.id.toLowerCase()]));
    for (const p of profiles) {
      if (taken.has(p.name.toLowerCase())) continue;
      out.push({
        insert: `@${p.name}`,
        label: p.name,
        detail: p.role || `start ${p.name}, a ${p.kind}`,
        kind: p.kind,
        ...(p.color ? { color: p.color } : {}),
      });
    }
    for (const p of people)
      out.push({ insert: `@${p.name}`, label: p.name, detail: p.role || "teammate", kind: null });
  }
  for (const k of allKinds) {
    const members = roster.filter((l) => l.kind === k).length;
    out.push({
      insert: `${sigil}${k}`,
      label: k,
      detail: members === 0 ? `start ${k}` : `a free ${k}`,
      kind: k,
    });
  }
  if (roster.length > 1)
    out.push({ insert: `${sigil}all`, label: "all", detail: "every agent", kind: null });
  const q = query.toLowerCase();
  const seen = new Set<string>();
  return out.filter((s) => {
    const key = s.insert.toLowerCase();
    if (seen.has(key) || !key.slice(1).startsWith(q)) return false;
    seen.add(key);
    return true;
  });
}

// The typed forms Deck no longer reads, at the start of a message:
// "$codex×3" and "@claude*2" (a race), "$claude+" (another agent),
// "@claude*" (every one). They go as plain text; retiredForm says so, and
// where to go instead, or null for a message without one.
export function retiredForm(text: string): string | null {
  const m = /^\s*(?:[$@][\w-]+\s+)*([$@][\w-]+(?:[×*]\d*|\+))(?=[\s,:]|$)/.exec(text);
  if (m === null) return null;
  const form = m[1] ?? "";
  if (/[×*]\d/.test(form)) return `${form} is plain text now. Type /race to race agents.`;
  if (form.endsWith("+"))
    return `${form} is plain text now. Type /race to run more than one, or ask an agent to start a helper.`;
  return `${form} is plain text now. Name each agent to reach it.`;
}

// The hue a picker row wears: the profile's colour, else its kind's; none
// for a person or "all".
export function suggestionHue(
  s: Suggestion,
  kindColour: (kind: string) => string,
): string | undefined {
  if (s.color) return s.color;
  return s.kind === null ? undefined : kindColour(s.kind);
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

// A picked file goes in as `path`, the same text whether "#" or "@" picked
// it, so the engine and the agents read what they always did.
export function fileSuggestions(query: string, paths: readonly string[]): Suggestion[] {
  return matchPaths(query, paths).map((p) => ({
    insert: `\`${p}\``,
    label: p,
    detail: "changed",
    kind: null,
  }));
}
