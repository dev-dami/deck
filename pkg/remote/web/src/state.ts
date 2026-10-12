import type { PairOffer } from "./auth";
import {
  type AgentLine,
  type Approval,
  type Device,
  type DeviceRef,
  isMode,
  type Message,
  type Mode,
  type QueuedItem,
  type ServerEvent,
  type ServerInfo,
  type Session,
  type Theme,
} from "./protocol";
import { compareNewest, toMillis } from "./time";

export type Link = "connecting" | "online" | "offline";

export interface Toast {
  id: number;
  text: string;
  kind: "info" | "error";
}

// unpaired: show pairing; offer: a code is in hand and waits for the person
// to confirm the fingerprint (R9); pairing: the POST is in flight; paired:
// connected or reconnecting; protocol: the server closed 4400.
export type AuthPhase = "unpaired" | "offer" | "pairing" | "paired" | "protocol";

export interface Page {
  more: boolean;
  cursor: string | null;
}

export interface AppState {
  auth: AuthPhase;
  authMessage: string | null;
  offer: PairOffer | null;
  link: Link;
  server: ServerInfo | null;
  device: DeviceRef | null;
  theme: Theme | null;
  sessions: Session[];
  sessionsLoaded: boolean;
  messages: Record<string, Message[]>;
  // Sessions whose latest page has been fetched at least once, with the
  // cursor to the older page.
  pages: Record<string, Page>;
  approvals: Approval[];
  queue: Record<string, QueuedItem[]>;
  roster: Record<string, AgentLine[]>;
  devices: Device[] | null;
  toasts: Toast[];
  nextToastId: number;
}

export const initialState: AppState = {
  auth: "unpaired",
  authMessage: null,
  offer: null,
  link: "connecting",
  server: null,
  device: null,
  theme: null,
  sessions: [],
  sessionsLoaded: false,
  messages: {},
  pages: {},
  approvals: [],
  queue: {},
  roster: {},
  devices: null,
  toasts: [],
  nextToastId: 1,
};

export type Action =
  | { type: "event"; event: ServerEvent }
  | { type: "auth"; auth: AuthPhase; message: string | null }
  | { type: "offer"; offer: PairOffer | null; message: string | null }
  | { type: "link"; link: Link }
  | { type: "welcome"; server: ServerInfo; device: DeviceRef }
  | { type: "sessions.loaded"; sessions: Session[] }
  | {
      type: "messages.loaded";
      sessionId: string;
      messages: Message[];
      more: boolean;
      cursor: string | null;
      // True for the latest page: the cursor is replaced, not kept.
      latest: boolean;
    }
  | { type: "approvals.loaded"; approvals: Approval[] }
  | { type: "queue.loaded"; sessionId: string; items: QueuedItem[] }
  | { type: "roster.loaded"; sessionId: string; roster: AgentLine[] }
  | { type: "mode.loaded"; sessionId: string; mode: Mode }
  | { type: "devices.loaded"; devices: Device[] }
  | { type: "approval.dropped"; id: string }
  | { type: "toast.add"; text: string; kind: Toast["kind"] }
  | { type: "toast.dismiss"; id: number }
  | { type: "reset"; auth: AuthPhase; message: string | null };

const MAX_TOASTS = 4;

export function sortSessions(list: Session[]): Session[] {
  return [...list].sort((a, b) => compareNewest(a.updated_at, a.id, b.updated_at, b.id));
}

export function upsertSession(list: Session[], s: Session): Session[] {
  const i = list.findIndex((x) => x.id === s.id);
  if (i < 0) return sortSessions([...list, s]);
  const next = [...list];
  next[i] = s;
  return sortSessions(next);
}

function byCreated(a: Message, b: Message): number {
  return toMillis(a.created_at) - toMillis(b.created_at) || a.id.localeCompare(b.id);
}

// Merge by id and keep the newer revision, so a snapshot that raced with live
// events never rolls a streaming message back.
export function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  const byId = new Map<string, Message>();
  for (const m of existing) byId.set(m.id, m);
  for (const m of incoming) {
    const cur = byId.get(m.id);
    if (cur === undefined || toMillis(m.updated_at) >= toMillis(cur.updated_at)) byId.set(m.id, m);
  }
  return [...byId.values()].sort(byCreated);
}

function patchSession(list: Session[], id: string, patch: Partial<Session>): Session[] {
  const i = list.findIndex((s) => s.id === id);
  const cur = list[i];
  if (cur === undefined) return list;
  const next = [...list];
  next[i] = { ...cur, ...patch };
  return next;
}

function pendingFor(approvals: Approval[], sessionId: string): number {
  return approvals.filter((a) => a.session_id === sessionId).length;
}

function withPending(sessions: Session[], approvals: Approval[]): Session[] {
  return sessions.map((s) => {
    const n = pendingFor(approvals, s.id);
    return n === s.pending ? s : { ...s, pending: n };
  });
}

function byCreatedAt(a: Approval, b: Approval): number {
  return toMillis(a.created_at) - toMillis(b.created_at) || a.id.localeCompare(b.id);
}

function addToast(state: AppState, text: string, kind: Toast["kind"]): AppState {
  const toast: Toast = { id: state.nextToastId, text, kind };
  const toasts = [...state.toasts, toast].slice(-MAX_TOASTS);
  return { ...state, toasts, nextToastId: state.nextToastId + 1 };
}

function dropSession(state: AppState, id: string): AppState {
  const { [id]: _m, ...messages } = state.messages;
  const { [id]: _p, ...pages } = state.pages;
  const { [id]: _q, ...queue } = state.queue;
  const { [id]: _r, ...roster } = state.roster;
  return {
    ...state,
    sessions: state.sessions.filter((s) => s.id !== id),
    approvals: state.approvals.filter((a) => a.session_id !== id),
    messages,
    pages,
    queue,
    roster,
  };
}

function applyEvent(state: AppState, ev: ServerEvent): AppState {
  switch (ev.e) {
    case "session.created":
    case "session.updated":
      return {
        ...state,
        sessions: withPending(upsertSession(state.sessions, ev.p), state.approvals),
      };
    case "session.deleted":
      return dropSession(state, ev.p.id);
    case "message.created":
    case "message.updated": {
      const sid = ev.p.session_id;
      const merged = mergeMessages(state.messages[sid] ?? [], [ev.p]);
      return { ...state, messages: { ...state.messages, [sid]: merged } };
    }
    case "message.deleted": {
      const list = state.messages[ev.p.session_id];
      if (list === undefined) return state;
      return {
        ...state,
        messages: { ...state.messages, [ev.p.session_id]: list.filter((m) => m.id !== ev.p.id) },
      };
    }
    case "approval.requested": {
      if (state.approvals.some((a) => a.id === ev.p.id)) return state;
      const approvals = [...state.approvals, ev.p].sort(byCreatedAt);
      return { ...state, approvals, sessions: withPending(state.sessions, approvals) };
    }
    case "approval.resolved": {
      const approvals = state.approvals.filter((a) => a.id !== ev.p.id);
      return { ...state, approvals, sessions: withPending(state.sessions, approvals) };
    }
    case "busy":
      return {
        ...state,
        sessions: patchSession(state.sessions, ev.p.session_id, { busy: ev.p.busy }),
      };
    case "roster.changed":
      // The controller refetches the roster; the stale entry stays until then.
      return state;
    case "queue.changed":
      return { ...state, queue: { ...state.queue, [ev.p.session_id]: ev.p.items } };
    case "mode.changed":
      return {
        ...state,
        sessions: patchSession(state.sessions, ev.p.session_id, { mode: ev.p.mode }),
      };
    case "notice":
      return addToast(state, ev.p.text, "info");
    case "theme.changed":
      return { ...state, theme: ev.p };
    case "device.paired": {
      const devices =
        state.devices === null ? null : [...state.devices.filter((d) => d.id !== ev.p.id), ev.p];
      return addToast({ ...state, devices }, `Paired a new phone: ${ev.p.name}`, "info");
    }
    case "device.revoked": {
      if (state.devices === null) return state;
      return { ...state, devices: state.devices.filter((d) => d.id !== ev.p.id) };
    }
  }
}

export function reduce(state: AppState, action: Action): AppState {
  switch (action.type) {
    case "event":
      return applyEvent(state, action.event);
    case "auth":
      return { ...state, auth: action.auth, authMessage: action.message };
    case "offer":
      return {
        ...state,
        auth: action.offer === null ? "unpaired" : "offer",
        offer: action.offer,
        authMessage: action.message,
      };
    case "link":
      return state.link === action.link ? state : { ...state, link: action.link };
    case "welcome":
      return {
        ...state,
        server: action.server,
        device: action.device,
        theme: action.server.theme,
      };
    case "sessions.loaded":
      return {
        ...state,
        sessions: sortSessions(withPending(action.sessions, state.approvals)),
        sessionsLoaded: true,
      };
    case "messages.loaded": {
      const cur = state.pages[action.sessionId];
      const page: Page = action.latest
        ? { more: action.more, cursor: action.cursor }
        : { more: action.more, cursor: action.cursor ?? cur?.cursor ?? null };
      return {
        ...state,
        messages: {
          ...state.messages,
          [action.sessionId]: mergeMessages(
            state.messages[action.sessionId] ?? [],
            action.messages,
          ),
        },
        pages: { ...state.pages, [action.sessionId]: page },
      };
    }
    case "approvals.loaded": {
      const approvals = [...action.approvals].sort(byCreatedAt);
      return { ...state, approvals, sessions: withPending(state.sessions, approvals) };
    }
    case "approval.dropped": {
      const approvals = state.approvals.filter((a) => a.id !== action.id);
      return { ...state, approvals, sessions: withPending(state.sessions, approvals) };
    }
    case "queue.loaded":
      return { ...state, queue: { ...state.queue, [action.sessionId]: action.items } };
    case "roster.loaded":
      return { ...state, roster: { ...state.roster, [action.sessionId]: action.roster } };
    case "mode.loaded":
      return {
        ...state,
        sessions: patchSession(state.sessions, action.sessionId, { mode: action.mode }),
      };
    case "devices.loaded":
      return { ...state, devices: action.devices };
    case "toast.add":
      return addToast(state, action.text, action.kind);
    case "toast.dismiss":
      return { ...state, toasts: state.toasts.filter((n) => n.id !== action.id) };
    case "reset":
      return { ...initialState, auth: action.auth, authMessage: action.message };
  }
}

export function sessionTitle(s: Session | undefined): string {
  if (s === undefined) return "Session";
  return s.title.trim() === "" ? "Untitled" : s.title;
}

export function sessionMode(s: Session | undefined): Mode | null {
  return s !== undefined && isMode(s.mode) ? s.mode : null;
}

export function isBusy(state: AppState, sessionId: string): boolean {
  return state.sessions.find((s) => s.id === sessionId)?.busy ?? false;
}
