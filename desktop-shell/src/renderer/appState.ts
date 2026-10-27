import type { Store } from "@web/controller";
import type { Notice, Session } from "@web/protocol";
import type { Action, AppState } from "@web/state";
import { useMemo, useSyncExternalStore } from "react";
import type { EngineStop } from "../shared/bridge";
import { actedAt, type RoomNotice, type RoomStatus, roomStatus, sameStatus } from "./attention";
import { type DeckClient, serves } from "./client";
import { crewOf, watchCrew } from "./crewState";
import { loadDraft, saveDraft } from "./drafts";
import { watchLimits } from "./limitsState";
import { useSelect } from "./select";
import { isSide } from "./sideChat";
import { initialUi, onScreen, reduceUi, type UiAction, type UiState } from "./ui";

// The window's own state for each open project, keyed by the project's key
// (OpenProject.key). It lives here and not in Shell, so a switch to another
// project and back keeps the open thread, the panel, the unread marks and
// the drafts. Each project's unread marks follow its Deck from the moment
// it opens, whether its Shell shows or not. So does each room's status
// (attention.ts), which the sidebar, the inbox, the badge and the
// notifications read.

export interface ProjectState {
  ui: UiState;
  // Set while the project's engine is stopped (a local Deck only).
  engine: EngineStop | null;
  // Each room's status, by session id.
  rooms: Record<string, RoomStatus>;
  // The notices of the project's rooms, oldest first.
  notices: RoomNotice[];
}

const EMPTY: ProjectState = { ui: initialUi, engine: null, rooms: {}, notices: [] };
const MAX_NOTICES = 50;

// What a project's state is fed from.
export type ProjectClient = Pick<DeckClient, "project" | "changes" | "rooms" | "capabilities">;

// A Deck that serves the room calls also marks its notices and says what
// each room waits for (Session.attention); an older one does neither.
const marks = (c: ProjectClient | null): boolean => c?.rooms !== undefined && (c.capabilities?.().length ?? 0) > 0;

interface Attached {
  source: string;
  store: Store;
  client: ProjectClient | null;
  // The files each room changed, fetched each time it stops working.
  changed: Map<string, number>;
  off: () => void;
}

const states = new Map<string, ProjectState>();
// Each project's drafts by thread ("" is the new-thread screen), read from
// localStorage the first time a thread's draft is asked for.
const drafts = new Map<string, Map<string, string>>();
const attached = new Map<string, Attached>();
const listeners = new Set<() => void>();

function notify(): void {
  for (const l of listeners) l();
}

export function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

export function projectState(key: string): ProjectState {
  return states.get(key) ?? EMPTY;
}

function put(key: string, next: ProjectState): void {
  if (next === projectState(key)) return;
  states.set(key, next);
  notify();
}

// Each room's status from the project's state; the same objects while
// nothing a room reads changed.
function withRooms(key: string, p: ProjectState): ProjectState {
  const a = attached.get(key);
  if (a === undefined) return p;
  const s = a.store.state;
  const open = onScreen(p.ui);
  const rooms: Record<string, RoomStatus> = {};
  let same = true;
  let count = 0;
  for (const session of s.sessions) {
    if (isSide(session)) continue;
    const id = session.id;
    const crew = crewOf(a.client?.project, id);
    const engine = marks(a.client) ? (a.client?.rooms?.get(id).attention ?? "") : null;
    const next = roomStatus({
      session,
      approvals: s.approvals.filter((x) => x.session_id === id),
      tasks: crew?.board?.tasks ?? [],
      agents: crew?.crew?.agents ?? [],
      notices: p.notices.filter((n) => n.session === id),
      actedAt: actedAt(s.messages[id], s.queue[id]),
      unread: p.ui.unread[id] === true,
      open: id === open,
      changed: a.changed.get(id) ?? 0,
      attention: engine,
    });
    const before = p.rooms[id];
    const kept = before !== undefined && sameStatus(before, next) ? before : next;
    rooms[id] = kept;
    if (kept !== before) same = false;
    count++;
  }
  if (same && count === Object.keys(p.rooms).length) return p;
  return { ...p, rooms };
}

// The room the person sees has its notices seen.
function seeNotices(p: ProjectState): ProjectState {
  const open = onScreen(p.ui);
  if (open === null || !p.notices.some((n) => n.session === open && !n.seen)) return p;
  return { ...p, notices: p.notices.map((n) => (n.session === open && !n.seen ? { ...n, seen: true } : n)) };
}

function update(key: string, f: (p: ProjectState) => ProjectState): void {
  put(key, withRooms(key, f(projectState(key))));
}

const same = (p: ProjectState): ProjectState => p;

export function dispatchUi(key: string, a: UiAction): void {
  update(key, (cur) => {
    const ui = reduceUi(cur.ui, a);
    return ui === cur.ui ? cur : seeNotices({ ...cur, ui });
  });
}

// Marks the project on screen: its open room is seen, and the open rooms of
// the others gather news like any other room.
export function showProject(key: string | null): void {
  for (const k of attached.keys()) dispatchUi(k, { type: "shown", on: k === key });
}

// A notice for the room the person sees shows as a toast; one for another
// room waits in that room's status and in the inbox. Returns whether it
// toasts.
export function addNotice(key: string, n: Notice, at = Date.now(), via: string | null = null): boolean {
  if (n.session_id === "") return true;
  const seen = n.session_id === onScreen(projectState(key).ui);
  update(key, (cur) => ({
    ...cur,
    notices: [...cur.notices, { session: n.session_id, text: n.text, at, seen, via }].slice(-MAX_NOTICES),
  }));
  return seen;
}

// The shell's own one-line note in a room, such as a revert. The room's
// timeline shows it; it never toasts.
export function addLocalNotice(key: string, session: string, text: string): void {
  addNotice(key, { session_id: session, text }, Date.now(), "local");
}

const sameNotes = (a: RoomNotice[], b: RoomNotice[]): boolean => a.length === b.length && a.every((n, i) => n === b[i]);

// The shell's own notes in a room, oldest first.
export function useLocalNotices(key: string, session: string): RoomNotice[] {
  const select = useMemo(() => (p: ProjectState) => p.notices.filter((n) => n.session === session && n.via === "local"), [session]);
  return useSelect(subscribe, () => projectState(key), select, sameNotes);
}

export function setEngine(key: string, engine: EngineStop | null): void {
  const cur = projectState(key);
  if (cur.engine !== engine) put(key, { ...cur, engine });
}

function draftsOf(key: string): Map<string, string> {
  let d = drafts.get(key);
  if (d === undefined) {
    d = new Map();
    drafts.set(key, d);
  }
  return d;
}

export function draftOf(key: string, thread: string): string {
  const d = draftsOf(key);
  const text = d.get(thread);
  if (text !== undefined) return text;
  const source = attached.get(key)?.source;
  if (source === undefined) return "";
  const saved = loadDraft(source, thread);
  d.set(thread, saved);
  return saved;
}

export function setDraft(key: string, thread: string, text: string): void {
  if (draftOf(key, thread) === text) return;
  draftsOf(key).set(thread, text);
  const source = attached.get(key)?.source;
  if (source !== undefined) saveDraft(source, thread, text);
  notify();
}

// Starts to keep a project's state: its Deck's sessions, messages and
// approvals feed the unread marks and the rooms' statuses. source names
// where its drafts persist. With a client, a local project's board and
// agents are polled too (crewState.ts), and a room that stops working has
// its changed files counted.
export function attachProject(key: string, source: string, store: Store, client: ProjectClient | null = null): void {
  if (attached.has(key)) return;
  let sessions: Session[] | null = null;
  let messages: AppState["messages"] | null = null;
  let approvals: AppState["approvals"] | null = null;
  let queue: AppState["queue"] | null = null;
  let busy = new Set<string>();
  const changed = new Map<string, number>();
  const count = (id: string) =>
    void client?.changes(id).then((files) => {
      if (files === null || !attached.has(key)) return;
      changed.set(id, files.length);
      update(key, same);
    });
  const feed = () => {
    const s = store.state;
    if (s.sessionsLoaded && s.sessions !== sessions) {
      sessions = s.sessions;
      const now = new Set(sessions.filter((x) => x.busy).map((x) => x.id));
      for (const id of busy) if (!now.has(id)) count(id);
      busy = now;
      dispatchUi(key, { type: "sessions", sessions });
    }
    if (s.messages !== messages) {
      messages = s.messages;
      dispatchUi(key, { type: "messages", messages });
    }
    if (s.approvals !== approvals || s.queue !== queue) {
      approvals = s.approvals;
      queue = s.queue;
      update(key, same);
    }
  };
  // The shared reducer turns every notice into a toast; the store's
  // dispatch is wrapped so that a notice for a room off screen does not.
  // The rpc client keeps the notice's via in its rooms just before.
  const viaOf = (n: Notice): string | null => {
    if (!marks(client)) return null;
    const last = client?.rooms?.get(n.session_id).notes.at(-1);
    return last !== undefined && last.text === n.text ? last.via : "";
  };
  const raw = store.dispatch;
  store.dispatch = (a: Action) => {
    if (a.type === "event" && a.event.e === "notice" && !addNotice(key, a.event.p, Date.now(), viaOf(a.event.p))) return;
    raw(a);
  };
  const api = client?.project;
  const stopCrew = api === undefined ? () => {} : watchCrew(api, store, () => projectState(key).ui.openId, () => update(key, same));
  const stopLimits = api === undefined || client === null ? () => {} : watchLimits(api, store, () => serves(client, "Limits"));
  const off = store.subscribe(feed);
  // The engine's attention changes without a shared event.
  const offRooms = client?.rooms?.subscribe(() => update(key, same)) ?? (() => {});
  attached.set(key, {
    source,
    store,
    client,
    changed,
    off: () => {
      off();
      offRooms();
      stopCrew();
      stopLimits();
      store.dispatch = raw;
    },
  });
  feed();
}

// Forgets a closed project. Its saved drafts stay for the next time it opens.
export function dropProject(key: string): void {
  attached.get(key)?.off();
  attached.delete(key);
  drafts.delete(key);
  if (states.delete(key)) notify();
}

// The parts of UiState a view reads. The seen texts and message lists change
// with every token and are left out.
export type UiView = Omit<UiState, "seen" | "lists">;

function uiView(p: ProjectState): UiView {
  const { seen: _s, lists: _l, ...view } = p.ui;
  return view;
}

const sameView = (a: UiView, b: UiView): boolean =>
  (Object.keys(a) as (keyof UiView)[]).every((k) => Object.is(a[k], b[k]));

export function useProjectUi(key: string): UiView {
  return useSelect(subscribe, () => projectState(key), uiView, sameView);
}

// Every open project's room statuses, by the project's source. The same
// object while no project's statuses changed.
let roomsMemo: { rooms: Record<string, RoomStatus>[]; v: Record<string, Record<string, RoomStatus>> } | null = null;

export function roomsBySource(): Record<string, Record<string, RoomStatus>> {
  const list = [...attached.entries()];
  const rooms = list.map(([key]) => projectState(key).rooms);
  if (roomsMemo !== null && roomsMemo.rooms.length === rooms.length && roomsMemo.rooms.every((r, i) => r === rooms[i])) return roomsMemo.v;
  const v = Object.fromEntries(list.map(([, a], i) => [a.source, rooms[i] ?? {}]));
  roomsMemo = { rooms, v };
  return v;
}

export function useRooms(): Record<string, Record<string, RoomStatus>> {
  return useSyncExternalStore(subscribe, roomsBySource);
}

// The room's status, for what the room is waiting on.
export function useRoomStatus(key: string, sid: string): RoomStatus | undefined {
  return useSyncExternalStore(subscribe, () => projectState(key).rooms[sid]);
}

export function useEngine(key: string): EngineStop | null {
  return useSyncExternalStore(subscribe, () => projectState(key).engine);
}

export function useDraft(key: string, thread: string): string {
  return useSyncExternalStore(subscribe, () => draftOf(key, thread));
}
