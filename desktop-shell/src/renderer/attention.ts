import type { Approval, Message, QueuedItem, Session } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { toMillis } from "@web/time";
import type { AgentNode, BoardTask, TeamContract } from "./crew";
import type { Attention } from "./roomMeta";

// One status per room, derived from what Deck sent and what the person did.
// The sidebar, the inbox, the badge and the notifications all read it.

export type RoomState = "working" | "needs-you" | "overlap" | "review" | "done" | "idle";

export interface RoomStatus {
  state: RoomState;
  // Names the thing: "Approve command", "Task blocked: Map the limiter".
  label: string;
  // When the room entered this state (ms), for ordering.
  at: number;
}

// A notice Deck sent for a room, and whether the person has seen it: it
// arrived while the room was on screen, or the room was opened since.
export interface RoomNotice {
  session: string;
  text: string;
  at: number;
  seen: boolean;
  // How the notice is marked: "overlap" (the engine's overlap note), "local"
  // (the shell's own, such as a revert) or "". null when the Deck does not
  // mark its notices (an older engine, or a paired Deck).
  via: string | null;
}

export interface Overlap {
  agents: [string, string];
  file: string;
}

// The engine's overlap note: "claude-1 and codex-2 both changed a.go in the
// last few minutes; one may undo the other".
export function parseOverlap(text: string): Overlap | null {
  const m = /^(\S+) and (\S+) both changed (.+?)(?: in the last few minutes\b.*)?$/s.exec(text.trim());
  if (m === null) return null;
  return { agents: [m[1] ?? "", m[2] ?? ""], file: m[3] ?? "" };
}

const base = (path: string): string => path.split(/[\\/]/).pop() || path;

// The label of an overlap notice, or null for another notice. The engine
// marks its overlap notes (via); the text is matched only for a Deck that
// does not mark them.
export function overlapLabel(n: RoomNotice): string | null {
  if (n.via !== null && n.via !== "overlap") return null;
  const o = parseOverlap(n.text);
  if (o === null) return n.via === "overlap" ? n.text : null;
  return `${o.agents[0]} and ${o.agents[1]} both changed ${base(o.file)}`;
}

export function approvalLabel(a: Approval): string {
  if (a.kind === "command") return "Approve command";
  if (a.kind === "edit") return a.path !== undefined && a.path !== "" ? `Approve edit to ${base(a.path)}` : "Approve edit";
  return `Answer ${a.agent}`;
}

export interface RoomInput {
  session: Session;
  // This room's approvals, oldest first (as the reducer keeps them).
  approvals: Approval[];
  // This room's task board and agents; empty when unknown.
  tasks: BoardTask[];
  agents: AgentNode[];
  // This room's notices.
  notices: RoomNotice[];
  // When the person last wrote in the room, sent or queued (ms); 0 unknown.
  actedAt: number;
  // News since the room was last open (ui.ts).
  unread: boolean;
  // The room is on screen.
  open: boolean;
  // The files the room changed, as of its last finish.
  changed: number;
  // What the engine says the room waits for; null when it does not say.
  attention: Attention | null;
}

const ENGINE_LABEL: Record<"approval" | "question" | "error", string> = {
  approval: "Approve a request",
  question: "Answer a question",
  error: "An agent stopped with an error",
};

function needsYou(r: RoomInput, updated: number): RoomStatus | null {
  const first = r.approvals[0];
  if (first !== undefined) {
    const at = Math.max(...r.approvals.map((a) => toMillis(a.created_at)));
    return { state: "needs-you", label: approvalLabel(first), at };
  }
  for (const a of r.agents) {
    const at = a.since > 0 ? a.since : updated;
    if (a.status === "approval") return { state: "needs-you", label: `Answer ${a.name}`, at };
    // An error stands until the person writes again or the agent moves on.
    if (a.status === "error" && at > r.actedAt) return { state: "needs-you", label: `${a.name} stopped with an error`, at };
  }
  const stuck = (t: BoardTask) => t.status === "blocked" || t.status === "failed";
  const taskLabel = (t: BoardTask) => `${t.status === "blocked" ? "Task blocked" : "Task failed"}: ${t.title || t.id}`;
  if (r.attention === null) {
    for (const t of r.tasks) {
      // A task without a time never outranks what the person did.
      if (stuck(t) && t.updatedAt > r.actedAt) return { state: "needs-you", label: taskLabel(t), at: t.updatedAt };
    }
    return null;
  }
  // The engine's word. Its blocked holds until a retry or a cancel changes
  // the task, so a message from the person does not clear it.
  switch (r.attention) {
    case "blocked": {
      const t = r.tasks.find((x) => stuck(x) && !x.entry);
      return { state: "needs-you", label: t === undefined ? "Task blocked" : taskLabel(t), at: t?.updatedAt || updated };
    }
    case "approval":
    case "question":
      return { state: "needs-you", label: ENGINE_LABEL[r.attention], at: updated };
    case "error":
      // The agents tree names the agent, and the loop above weighed it.
      if (r.agents.some((a) => a.status === "error") || updated <= r.actedAt) return null;
      return { state: "needs-you", label: ENGINE_LABEL.error, at: updated };
  }
  return null;
}

function workingLabel(agents: AgentNode[]): string {
  const names = agents.filter((a) => a.depth === 0 && (a.status === "working" || a.status === "tool")).map((a) => a.name);
  if (names.length === 1) return `${names[0]} working`;
  if (names.length === 2) return `${names[0]} and ${names[1]} working`;
  if (names.length > 2) return `${names.length} agents working`;
  return "Working";
}

// needs-you holds until the person acts: the approval is answered, they
// write in the room, or the agent moves on. Opening the room does not clear
// it. review and done clear when the room is opened; overlap when its
// notice is seen.
export function roomStatus(r: RoomInput): RoomStatus {
  const updated = toMillis(r.session.updated_at);
  const need = needsYou(r, updated);
  if (need !== null) return need;
  let overlap: { n: RoomNotice; label: string } | null = null;
  for (const n of r.notices) {
    const label = n.seen ? null : overlapLabel(n);
    if (label !== null && (overlap === null || n.at >= overlap.n.at)) overlap = { n, label };
  }
  if (overlap !== null) return { state: "overlap", label: overlap.label, at: overlap.n.at };
  if (r.session.busy) return { state: "working", label: workingLabel(r.agents), at: updated };
  if (r.unread && !r.open) {
    return r.changed > 0 ? { state: "review", label: "Changes to review", at: updated } : { state: "done", label: "Finished", at: updated };
  }
  return { state: "idle", label: "", at: updated };
}

export const sameStatus = (a: RoomStatus | undefined, b: RoomStatus | undefined): boolean =>
  a === b || (a !== undefined && b !== undefined && a.state === b.state && a.label === b.label && a.at === b.at);

// The person's last word in a room: their newest message, or a queued one.
const actedMemo = new WeakMap<Message[], number>();

export function actedAt(messages: Message[] | undefined, queue: QueuedItem[] | undefined): number {
  let at = 0;
  if (messages !== undefined) {
    const hit = actedMemo.get(messages);
    if (hit !== undefined) at = hit;
    else {
      for (let i = messages.length - 1; i >= 0; i--) {
        const m = messages[i];
        if (m !== undefined && m.role === "user") {
          at = toMillis(m.created_at);
          break;
        }
      }
      actedMemo.set(messages, at);
    }
  }
  for (const q of queue ?? []) at = Math.max(at, toMillis(q.at));
  return at;
}

// The sidebar's order within a group: rooms that need the person first,
// then rooms that just finished or overlap, newest first; the rest keep
// Deck's order (newest activity first).
export function attentionRank(s: RoomStatus | undefined): number {
  if (s === undefined) return 2;
  if (s.state === "needs-you") return 0;
  return s.state === "overlap" || s.state === "review" || s.state === "done" ? 1 : 2;
}

export function byAttention(a: { s: Session; status: RoomStatus | undefined }, b: { s: Session; status: RoomStatus | undefined }): number {
  const ra = attentionRank(a.status);
  const rb = attentionRank(b.status);
  if (ra !== rb) return ra - rb;
  if (ra < 2) return (b.status?.at ?? 0) - (a.status?.at ?? 0);
  return toMillis(b.s.updated_at) - toMillis(a.s.updated_at);
}

// ---- the inbox ----------------------------------------------------------

export interface InboxProject {
  key: string;
  // "folder:<dir>" or "paired:<id>".
  source: string;
  name: string;
  sessions: Session[];
  rooms: Record<string, RoomStatus>;
  approvals: Approval[];
  notices: RoomNotice[];
  // Contracts from Team, when the project loaded them.
  contracts: TeamContract[];
}

export type InboxState = "needs-you" | "overlap" | "review" | "done" | "notice";

// answer: Approve, Decline, Always allow; contract: Accept, Decline; open:
// opens the room.
export type InboxAction = "answer" | "contract" | "open";

export interface InboxItem {
  id: string;
  project: string;
  source: string;
  projectName: string;
  // The room, or null for a contract.
  room: string | null;
  title: string;
  state: InboxState;
  label: string;
  at: number;
  action: InboxAction;
  approval?: Approval;
  contract?: TeamContract;
}

const RANK: Record<InboxState, number> = { "needs-you": 0, overlap: 1, review: 2, done: 3, notice: 4 };

// Everything across the open projects that waits for the person: what needs
// an answer first, then overlaps, changes to review, finished rooms and
// notices; newest first within each.
export function inboxItems(projects: InboxProject[]): InboxItem[] {
  const out: InboxItem[] = [];
  for (const p of projects) {
    const ids = new Set(p.sessions.map((s) => s.id));
    const title = (id: string) => sessionTitle(p.sessions.find((s) => s.id === id));
    const head = { project: p.key, source: p.source, projectName: p.name };
    const asked = new Set<string>();
    for (const a of p.approvals) {
      if (!ids.has(a.session_id)) continue;
      asked.add(a.session_id);
      out.push({
        ...head,
        id: `${p.key}/approval/${a.id}`,
        room: a.session_id,
        title: title(a.session_id),
        state: "needs-you",
        label: approvalLabel(a),
        at: toMillis(a.created_at),
        action: "answer",
        approval: a,
      });
    }
    for (const s of p.sessions) {
      const st = p.rooms[s.id];
      if (st === undefined || st.state === "working" || st.state === "idle") continue;
      if (st.state === "needs-you" && asked.has(s.id)) continue;
      out.push({ ...head, id: `${p.key}/room/${s.id}`, room: s.id, title: sessionTitle(s), state: st.state, label: st.label, at: st.at, action: "open" });
    }
    for (const n of p.notices) {
      if (n.seen || !ids.has(n.session) || overlapLabel(n) !== null) continue;
      out.push({ ...head, id: `${p.key}/notice/${n.session}/${n.at}/${n.text}`, room: n.session, title: title(n.session), state: "notice", label: n.text, at: n.at, action: "open" });
    }
    for (const c of p.contracts) {
      if (!c.inbound || c.state !== "proposed") continue;
      out.push({
        ...head,
        id: `${p.key}/contract/${c.peer}/${c.id}`,
        room: null,
        title: c.title || c.id,
        state: "needs-you",
        label: `Contract from ${c.peer}`,
        at: 0,
        action: "contract",
        contract: c,
      });
    }
  }
  return out.sort((a, b) => RANK[a.state] - RANK[b.state] || b.at - a.at || a.id.localeCompare(b.id));
}

// The rooms that need the person, in inbox order, each once.
export function needsYouRooms(items: InboxItem[]): { project: string; room: string }[] {
  const seen = new Set<string>();
  const out: { project: string; room: string }[] = [];
  for (const i of items) {
    if (i.state !== "needs-you" || i.room === null) continue;
    const k = `${i.project}/${i.room}`;
    if (seen.has(k)) continue;
    seen.add(k);
    out.push({ project: i.project, room: i.room });
  }
  return out;
}

// The room after the one on screen, wrapping round; the first when none is.
export function nextNeedsYou(
  rooms: { project: string; room: string }[],
  current: { project: string; room: string | null },
): { project: string; room: string } | null {
  const i = rooms.findIndex((r) => r.project === current.project && r.room === current.room);
  return rooms[(i + 1) % rooms.length] ?? null;
}

export type NotifyPref = "all" | "needs-you" | "none";

// A notification goes out when a room enters needs-you, or finishes (done
// or review), and not while the person has a message queued there.
export function shouldNotify(prev: RoomState | undefined, next: RoomState, pref: NotifyPref, queued: boolean): boolean {
  if (pref === "none" || prev === undefined || prev === next || queued) return false;
  if (next === "needs-you") return true;
  const finished = (s: RoomState) => s === "done" || s === "review";
  // done turns into review once the changed files are in: one finish.
  return pref === "all" && finished(next) && !finished(prev);
}
