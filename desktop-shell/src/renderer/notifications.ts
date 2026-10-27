import type { Message } from "@web/protocol";
import type { RoomState, RoomStatus } from "./attention";
import type { BoardTask } from "./crew";

// What the window tells the person about, and how: the native
// notifications, the notification centre (the bell in the sidebar's foot)
// and the badge. Pure, so the decisions are tested; notifyCentre.ts feeds it
// and keeps the list.

export type NotifyKind = "approval" | "error" | "mention" | "task" | "finished";

// Most urgent first: a burst names the most urgent of its events.
export const NOTIFY_KINDS: readonly { id: NotifyKind; label: string; description: string }[] = [
  { id: "approval", label: "Approvals and questions", description: "An agent waits for your answer." },
  { id: "error", label: "Errors and blocked work", description: "An agent stopped with an error, or a task is blocked or failed." },
  { id: "mention", label: "Mentions", description: "An agent writes to you by name (@user)." },
  { id: "task", label: "Tasks done", description: "A task on a room's board is done." },
  { id: "finished", label: "Finished turns", description: "A room's agents finished and wait for what comes next." },
];

const RANK: Record<NotifyKind, number> = { approval: 0, error: 1, mention: 2, task: 3, finished: 4 };

// Something to tell the person about, in a room of a project. source is the
// project's source ("folder:<dir>", "paired:<id>"), which outlives the
// window's own key for it, so an entry still opens after a restart.
export interface NotifyEvent {
  kind: NotifyKind;
  source: string;
  projectName: string;
  room: string;
  roomTitle: string;
  text: string;
  at: number;
}

// An entry of the notification centre. count is how many events of one
// kind in one room it stands for while it is unread.
export interface NotifyEntry extends NotifyEvent {
  id: string;
  read: boolean;
  count: number;
}

export interface NotifySettings {
  // The master switch: off, nothing is shown or kept.
  enabled: boolean;
  kinds: Record<NotifyKind, boolean>;
  sound: boolean;
  // Native notifications only while the window is in the background; off,
  // also for a room the person is not looking at.
  unfocusedOnly: boolean;
}

// ---- what happened ------------------------------------------------------

const finished = (s: RoomState) => s === "done" || s === "review";

// What a room's change of status is news of, or null. A room's first status
// is a starting point, not news; nor is anything while the person has a
// message queued there (it goes on by itself).
export function roomEvent(prev: RoomStatus | undefined, next: RoomStatus, queued: boolean): NotifyKind | null {
  if (prev === undefined || queued) return null;
  if (next.state === "needs-you") {
    // A new request in a room that already waited is news too.
    if (prev.state === "needs-you" && prev.label === next.label) return null;
    return next.need ?? "approval";
  }
  // done turns into review once the changed files are in: one finish.
  return finished(next.state) && !finished(prev.state) ? "finished" : null;
}

// The tasks that are newly done. With no board before, none are.
export function doneTasks(prev: readonly BoardTask[] | undefined, next: readonly BoardTask[]): BoardTask[] {
  if (prev === undefined) return [];
  const before = new Map(prev.map((t) => [t.id, t.status]));
  return next.filter((t) => t.status === "done" && before.has(t.id) && before.get(t.id) !== "done");
}

const escape = (s: string) => s.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");

// Whether text addresses the person: @user (or $user, or their team name)
// where a mention counts, as the engine reads them
// (docs/design/agents-and-mentions.md): after a start, a space or a clause's
// punctuation, and not followed by a path or a domain.
export function mentionsUser(text: string, names: readonly string[]): boolean {
  const alts = names.filter((n) => n !== "").map(escape);
  if (alts.length === 0) return false;
  const re = new RegExp(`(?:^|[\\s(,;:])[@$](?:${alts.join("|")})(?![\\w-]|[/.]\\S)`, "i");
  return re.test(text);
}

const textOf = (m: Message) =>
  m.parts
    .filter((p) => p.type === "text")
    .map((p) => p.text ?? "")
    .join("\n");

// The finished agent messages of a room not yet looked at, and those that
// mention the person. seen is null the first time: what is there already is
// the starting point, not news.
export function newMentions(
  seen: ReadonlySet<string> | null,
  messages: readonly Message[],
  names: readonly string[],
): { seen: Set<string>; mentions: { message: Message; text: string }[] } {
  const next = new Set(seen ?? []);
  const mentions: { message: Message; text: string }[] = [];
  for (const m of messages) {
    if (m.role === "user" || !m.finished || next.has(m.id)) continue;
    next.add(m.id);
    if (seen === null) continue;
    const text = textOf(m);
    if (mentionsUser(text, names)) mentions.push({ message: m, text });
  }
  return { seen: next, mentions };
}

// ---- whether and how to tell --------------------------------------------

export interface NotifyContext {
  // The window has the focus.
  focused: boolean;
  // The event's room is the one on screen.
  viewing: boolean;
}

export interface Decision {
  // It goes into the centre.
  record: boolean;
  // It pops up as a native notification.
  native: boolean;
  // It is read already: the person is looking at it.
  read: boolean;
}

const NONE: Decision = { record: false, native: false, read: false };

export function decide(kind: NotifyKind, s: NotifySettings, ctx: NotifyContext): Decision {
  if (!s.enabled || !s.kinds[kind]) return NONE;
  const read = ctx.focused && ctx.viewing;
  return { record: true, native: !read && (!ctx.focused || !s.unfocusedOnly), read };
}

// ---- bursts ---------------------------------------------------------------

export interface NativeNote {
  title: string;
  body: string;
  // The room a click opens: the most urgent event's.
  source: string;
  room: string;
}

// "3 rooms finished", "1 room needs your answer".
const PHRASE: Record<NotifyKind, [string, string, string]> = {
  approval: ["room needs", "rooms need", "your answer"],
  error: ["room stopped", "rooms stopped", "or blocked"],
  mention: ["mention", "mentions", "of you"],
  task: ["task", "tasks", "done"],
  finished: ["room", "rooms", "finished"],
};

export function phrase(kind: NotifyKind, n: number): string {
  const [one, many, tail] = PHRASE[kind];
  return `${n} ${n === 1 ? one : many} ${tail}`;
}

const SHOWN = 4;

// One native notification for a burst of events (four agents finishing at
// once): the event itself when it is alone; else a count in the title, led
// by the most urgent kind, and a line per event, most urgent first.
export function summarize(events: readonly NotifyEvent[]): NativeNote | null {
  const sorted = [...events].sort((a, b) => RANK[a.kind] - RANK[b.kind] || b.at - a.at);
  const top = sorted[0];
  if (top === undefined) return null;
  if (sorted.length === 1) return { title: `${top.projectName} · ${top.roomTitle}`, body: top.text, source: top.source, room: top.room };
  const lead = sorted.filter((e) => e.kind === top.kind).length;
  const title = lead === sorted.length ? phrase(top.kind, lead) : `${sorted.length} updates · ${phrase(top.kind, lead)}`;
  const lines = sorted.slice(0, SHOWN).map((e) => `${e.roomTitle}: ${e.text}`);
  if (sorted.length > SHOWN) lines.push(`and ${sorted.length - SHOWN} more`);
  return { title, body: lines.join("\n"), source: top.source, room: top.room };
}

// Gathers the events of a short window into one flush: the first event
// starts the window, and those that come within it join it.
export class Burst {
  private pending: NotifyEvent[] = [];
  private timer: unknown = null;

  constructor(
    private readonly windowMs: number,
    private readonly flush: (events: NotifyEvent[]) => void,
    private readonly timers: { set: (f: () => void, ms: number) => unknown; clear: (t: unknown) => void } = {
      set: (f, ms) => setTimeout(f, ms),
      clear: (t) => clearTimeout(t as ReturnType<typeof setTimeout>),
    },
  ) {}

  push(e: NotifyEvent): void {
    this.pending.push(e);
    this.timer ??= this.timers.set(() => this.fire(), this.windowMs);
  }

  private fire(): void {
    const out = this.pending;
    this.pending = [];
    this.timer = null;
    if (out.length > 0) this.flush(out);
  }

  dispose(): void {
    if (this.timer !== null) this.timers.clear(this.timer);
    this.timer = null;
    this.pending = [];
  }
}

// ---- the centre ---------------------------------------------------------

export const MAX_ENTRIES = 100;

const sameRoom = (a: { source: string; room: string }, b: { source: string; room: string }) => a.source === b.source && a.room === b.room;

// The list with e at its head. An unread entry of the same kind in the same
// room takes it instead, so a room that finishes three times while the
// person is away is one entry (count 3), not three.
export function addEntry(list: readonly NotifyEntry[], e: NotifyEvent, read: boolean, max = MAX_ENTRIES): NotifyEntry[] {
  const i = read ? -1 : list.findIndex((x) => !x.read && x.kind === e.kind && sameRoom(x, e));
  const prior = i < 0 ? undefined : list[i];
  const rest = list.filter((_, j) => j !== i);
  // Unique within the list, though two events share a time.
  const base = `${e.source}\n${e.room}\n${e.kind}\n${e.at}`;
  let id = base;
  for (let n = 1; rest.some((x) => x.id === id); n++) id = `${base}\n${n}`;
  return [{ ...e, id, read, count: (prior?.count ?? 0) + 1 }, ...rest].slice(0, max);
}

export function markRead(list: readonly NotifyEntry[], ids?: readonly string[]): NotifyEntry[] {
  const only = ids === undefined ? null : new Set(ids);
  let changed = false;
  const out = list.map((x) => {
    if (x.read || (only !== null && !only.has(x.id))) return x;
    changed = true;
    return { ...x, read: true };
  });
  return changed ? out : (list as NotifyEntry[]);
}

// Opening a room reads what it told the person.
export function markRoomRead(list: readonly NotifyEntry[], source: string, room: string): NotifyEntry[] {
  return markRead(
    list,
    list.filter((x) => !x.read && sameRoom(x, { source, room })).map((x) => x.id),
  );
}

export const unreadCount = (list: readonly NotifyEntry[]): number => list.filter((x) => !x.read).length;

// The dock or taskbar badge: each room that needs the person or told them
// something they have not read, once.
export function badgeCount(needsYou: readonly { source: string; room: string | null }[], list: readonly NotifyEntry[]): number {
  const rooms = new Set<string>();
  for (const n of needsYou) rooms.add(`${n.source}\n${n.room ?? ""}`);
  for (const x of list) if (!x.read) rooms.add(`${x.source}\n${x.room}`);
  return rooms.size;
}

// Only well-formed entries survive a load from storage.
export function parseEntries(v: unknown): NotifyEntry[] {
  if (!Array.isArray(v)) return [];
  const str = (o: Record<string, unknown>, k: string) => typeof o[k] === "string";
  const num = (o: Record<string, unknown>, k: string) => typeof o[k] === "number" && Number.isFinite(o[k]);
  return v
    .filter((x): x is Record<string, unknown> => typeof x === "object" && x !== null)
    .filter(
      (o) =>
        (Object.keys(RANK) as string[]).includes(o["kind"] as string) &&
        ["id", "source", "projectName", "room", "roomTitle", "text"].every((k) => str(o, k)) &&
        num(o, "at") &&
        num(o, "count") &&
        typeof o["read"] === "boolean",
    )
    .map((o) => o as unknown as NotifyEntry)
    .slice(0, MAX_ENTRIES);
}
