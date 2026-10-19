import type { AgentLine, Approval, Message, Session } from "@web/protocol";
import { groupParts } from "@web/transcript";

// The side chat: a question about what the agents of a thread are doing,
// asked of another agent in a thread of its own, so the agents at work are
// never interrupted. Each question carries a snapshot of the main thread,
// below a marker the window hides again when it shows the question.

export const SIDE_PREFIX = "Side chat: ";
export const MARKER = "--- What the main thread is doing (context for you to answer from, not a task) ---";
const MAX_DIGEST = 6000;
const RECENT = 8;

export function isSide(s: Session): boolean {
  return s.title.startsWith(SIDE_PREFIX);
}

export function sideTitle(main: Session): string {
  return `${SIDE_PREFIX}${main.title.trim() === "" ? "Untitled" : main.title}`.slice(0, 200);
}

// The side thread of a main one: the newest whose title names it.
export function findSide(sessions: Session[], main: Session): Session | undefined {
  const t = sideTitle(main);
  return sessions.filter((s) => s.title === t).sort((a, b) => b.updated_at.localeCompare(a.updated_at))[0];
}

function clip(s: string, n: number): string {
  const t = s.replace(/\s+/g, " ").trim();
  return t.length > n ? `${t.slice(0, n - 1)}…` : t;
}

function describe(m: Message): string {
  const parts = groupParts(m.parts);
  const text = parts
    .filter((c) => c.k === "text")
    .map((c) => (c.k === "text" ? c.text : ""))
    .join(" ");
  const tools = parts
    .filter((c) => c.k === "tool")
    .map((c) => (c.k === "tool" ? `${c.call?.name ?? c.result?.name ?? "tool"}${c.call?.summary ? ` ${clip(c.call.summary, 60)}` : ""}${c.result?.is_error === true ? " (failed)" : ""}` : ""));
  const who = m.role === "user" ? "User" : m.role === "system" ? "Deck" : (m.agent ?? "agent");
  const did = tools.length === 0 ? "" : ` [tools: ${tools.slice(-6).join("; ")}]`;
  const state = m.role === "assistant" && !m.finished ? " (still writing)" : "";
  return `${who}${state}: ${clip(text, 500)}${did}`;
}

// What the main thread is doing, in a few lines an agent can answer from.
export function sideDigest(main: Session, roster: AgentLine[], approvals: Approval[], messages: Message[]): string {
  const lines = [`Thread: ${main.title || "Untitled"} (${main.mode} mode${main.busy ? ", agents working" : ""})`];
  lines.push(
    roster.length === 0
      ? "Agents: none yet"
      : `Agents: ${roster.map((a) => `${a.id} (${a.kind}, ${a.model ?? "default model"}, ${a.busy ? "busy" : "idle"})`).join(", ")}`,
  );
  const waiting = approvals.filter((a) => a.session_id === main.id);
  if (waiting.length > 0) lines.push(`Waiting for the user's approval: ${waiting.map((a) => `${a.agent}: ${clip(a.command ?? a.path ?? a.description, 120)}`).join("; ")}`);
  lines.push("Recent messages, oldest first:");
  for (const m of messages.slice(-RECENT)) lines.push(`- ${describe(m)}`);
  const out = lines.join("\n");
  return out.length > MAX_DIGEST ? `…${out.slice(out.length - MAX_DIGEST)}` : out;
}

// The message the side agent gets: who it goes to, the question, then the
// snapshot. mention is "$claude" for the first question, "" after.
export function sideMessage(mention: string, question: string, digest: string): string {
  return `${mention === "" ? "" : `${mention} `}${question.trim()}\n\n${MARKER}\n${digest}`;
}

// The question as the person wrote it, without the mention and snapshot.
export function sideQuestion(text: string): string {
  const i = text.indexOf(MARKER);
  const q = i < 0 ? text : text.slice(0, i);
  return q.replace(/^\$[\w-]+[+*]?\s+/, "").trim();
}
