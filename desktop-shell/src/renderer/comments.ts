import type { AgentLine, Message } from "@web/protocol";

// A comment on one line of the diff, as the composer gets it:
//
//   $claude-1 In `pkg/remote/auth/limit.go:13`:
//   > global.fail()
//   comment text
export interface LineRef {
  path: string;
  line: number;
  text: string;
}

export function commentBlock(agent: string | null, ref: LineRef, comment: string): string {
  const to = agent === null ? "" : `$${agent} `;
  return `${to}In \`${ref.path}:${ref.line}\`:\n> ${ref.text.trim()}\n${comment.trim()}`;
}

// Adds a block to the draft, after a blank line when the draft has text.
export function appendComment(draft: string, block: string): string {
  const head = draft.replace(/\s+$/, "");
  return head === "" ? block : `${head}\n\n${block}`;
}

const BLOCK = /^(?:\$[\w-]+ )?In `[^`\n]+:\d+`:$/gm;

// How many line comments the draft holds.
export function countComments(draft: string): number {
  return draft.match(BLOCK)?.length ?? 0;
}

const EDIT_TOOL = /edit|write|patch|apply|create|multiedit/i;

function norm(p: string): string {
  return p.trim().replace(/^(?:a|b)\//, "").replace(/^\.\//, "");
}

export function samePath(a: string, b: string): boolean {
  const x = norm(a);
  const y = norm(b);
  if (x === "" || y === "") return false;
  return x === y || x.endsWith(`/${y}`) || y.endsWith(`/${x}`);
}

// The files a unified diff names in its headers.
function diffPaths(diff: string): string[] {
  const out: string[] = [];
  for (const line of diff.split("\n")) {
    const m = /^(?:\+\+\+|---) (?:[ab]\/)?(\S+)/.exec(line);
    if (m !== null && m[1] !== "/dev/null") out.push(m[1] ?? "");
  }
  return out;
}

// Whether an agent's reply changed the file: an edit tool on that path, or
// a tool result whose diff names it.
function touches(m: Message, path: string): boolean {
  return m.parts.some((p) => {
    if (p.type === "tool_call" && EDIT_TOOL.test(p.name ?? "") && samePath(p.summary ?? "", path)) return true;
    return p.type === "tool_result" && p.diff !== undefined && diffPaths(p.diff).some((d) => samePath(d, path));
  });
}

// Who a line comment goes to: the last agent that changed the file, else the
// room's first agent, else the first that wrote.
export function fileAuthor(messages: readonly Message[], roster: readonly AgentLine[], path: string): string | null {
  for (let i = messages.length - 1; i >= 0; i--) {
    const m = messages[i];
    if (m !== undefined && m.role === "assistant" && m.agent !== undefined && touches(m, path)) return m.agent;
  }
  return roster[0]?.id ?? messages.find((m) => m.role === "assistant" && m.agent !== undefined)?.agent ?? null;
}
