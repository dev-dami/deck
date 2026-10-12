import type {
  AgentLine,
  Approval,
  DeviceRef,
  Message,
  Mode,
  QueuedItem,
  ServerEvent,
  ServerInfo,
  Session,
} from "./protocol";
import { initialPushUi, type PushUi } from "./push-client";

export type Link = "connecting" | "online" | "offline";

export interface Notice {
  id: number;
  text: string;
  kind: "info" | "error";
}

export type AuthPhase = "unpaired" | "pairing" | "paired";

export interface AppState {
  auth: AuthPhase;
  authMessage: string | null;
  link: Link;
  server: ServerInfo | null;
  device: DeviceRef | null;
  sessions: Session[];
  sessionsLoaded: boolean;
  messages: Record<string, Message[]>;
  // Sessions whose full history has been fetched at least once.
  messagesLoaded: Record<string, boolean>;
  approvals: Approval[];
  queue: Record<string, QueuedItem[]>;
  roster: Record<string, AgentLine[]>;
  mode: Mode | null;
  notices: Notice[];
  nextNoticeId: number;
  push: PushUi;
}

export const initialState: AppState = {
  auth: "unpaired",
  authMessage: null,
  link: "connecting",
  server: null,
  device: null,
  sessions: [],
  sessionsLoaded: false,
  messages: {},
  messagesLoaded: {},
  approvals: [],
  queue: {},
  roster: {},
  mode: null,
  notices: [],
  nextNoticeId: 1,
  push: initialPushUi,
};

export type Action =
  | { type: "event"; event: ServerEvent }
  | { type: "auth"; auth: AuthPhase; message: string | null }
  | { type: "link"; link: Link }
  | { type: "welcome"; server: ServerInfo | null; device: DeviceRef | null }
  | { type: "sessions.loaded"; sessions: Session[] }
  | { type: "messages.loaded"; sessionId: string; messages: Message[] }
  | { type: "approvals.loaded"; approvals: Approval[] }
  | { type: "queue.loaded"; sessionId: string; items: QueuedItem[] }
  | { type: "roster.loaded"; sessionId: string; roster: AgentLine[] }
  | { type: "mode.loaded"; mode: Mode }
  | { type: "approval.dropped"; id: string }
  | { type: "notice.add"; text: string; kind: Notice["kind"] }
  | { type: "notice.dismiss"; id: number }
  | { type: "push.update"; patch: Partial<PushUi> }
  | { type: "reset"; auth: AuthPhase; message: string | null };

const MAX_NOTICES = 4;

export function sortSessions(list: Session[]): Session[] {
  return [...list].sort((a, b) => b.updated_at - a.updated_at || a.id.localeCompare(b.id));
}

export function upsertSession(list: Session[], s: Session): Session[] {
  const i = list.findIndex((x) => x.id === s.id);
  if (i < 0) return sortSessions([...list, s]);
  const next = [...list];
  next[i] = s;
  return sortSessions(next);
}

function byCreated(a: Message, b: Message): number {
  return a.created_at - b.created_at || a.id.localeCompare(b.id);
}

// Merge by id and keep the newer revision, so a snapshot that raced with live
// events never rolls a streaming message back.
export function mergeMessages(existing: Message[], incoming: Message[]): Message[] {
  const byId = new Map<string, Message>();
  for (const m of existing) byId.set(m.id, m);
  for (const m of incoming) {
    const cur = byId.get(m.id);
    if (cur === undefined || m.updated_at >= cur.updated_at) byId.set(m.id, m);
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

function addNotice(state: AppState, text: string, kind: Notice["kind"]): AppState {
  const notice: Notice = { id: state.nextNoticeId, text, kind };
  const notices = [...state.notices, notice].slice(-MAX_NOTICES);
  return { ...state, notices, nextNoticeId: state.nextNoticeId + 1 };
}

function applyEvent(state: AppState, ev: ServerEvent): AppState {
  switch (ev.e) {
    case "session.created":
    case "session.updated":
      return { ...state, sessions: upsertSession(state.sessions, ev.p) };
    case "session.deleted": {
      const { [ev.p.id]: _m, ...messages } = state.messages;
      const { [ev.p.id]: _q, ...queue } = state.queue;
      const { [ev.p.id]: _r, ...roster } = state.roster;
      return {
        ...state,
        sessions: state.sessions.filter((s) => s.id !== ev.p.id),
        approvals: state.approvals.filter((a) => a.session_id !== ev.p.id),
        messages,
        queue,
        roster,
      };
    }
    case "message.created":
    case "message.updated": {
      const sid = ev.p.session_id;
      const merged = mergeMessages(state.messages[sid] ?? [], [ev.p]);
      return { ...state, messages: { ...state.messages, [sid]: merged } };
    }
    case "message.deleted": {
      const sids = ev.p.session_id !== undefined ? [ev.p.session_id] : Object.keys(state.messages);
      const messages = { ...state.messages };
      for (const sid of sids) {
        const list = messages[sid];
        if (list !== undefined) messages[sid] = list.filter((m) => m.id !== ev.p.id);
      }
      return { ...state, messages };
    }
    case "approval.requested": {
      if (state.approvals.some((a) => a.id === ev.p.id)) return state;
      const approvals = [...state.approvals, ev.p].sort((a, b) => a.created_at - b.created_at);
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
      return { ...state, mode: ev.p.mode };
    case "notice":
      return addNotice(state, ev.p.text, "info");
    case "device.revoked":
      return state;
  }
}

export function reduce(state: AppState, action: Action): AppState {
  switch (action.type) {
    case "event":
      return applyEvent(state, action.event);
    case "auth":
      return { ...state, auth: action.auth, authMessage: action.message };
    case "link":
      return state.link === action.link ? state : { ...state, link: action.link };
    case "welcome":
      return { ...state, server: action.server, device: action.device };
    case "sessions.loaded":
      return {
        ...state,
        sessions: sortSessions(withPending(action.sessions, state.approvals)),
        sessionsLoaded: true,
      };
    case "messages.loaded":
      return {
        ...state,
        messages: {
          ...state.messages,
          [action.sessionId]: mergeMessages(
            state.messages[action.sessionId] ?? [],
            action.messages,
          ),
        },
        messagesLoaded: { ...state.messagesLoaded, [action.sessionId]: true },
      };
    case "approvals.loaded": {
      const approvals = [...action.approvals].sort((a, b) => a.created_at - b.created_at);
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
      return { ...state, mode: action.mode };
    case "notice.add":
      return addNotice(state, action.text, action.kind);
    case "notice.dismiss":
      return { ...state, notices: state.notices.filter((n) => n.id !== action.id) };
    case "push.update":
      return { ...state, push: { ...state.push, ...action.patch } };
    case "reset":
      return { ...initialState, auth: action.auth, authMessage: action.message };
  }
}

export function sessionTitle(s: Session | undefined): string {
  if (s === undefined) return "Session";
  return s.title.trim() === "" ? "Untitled" : s.title;
}

export function isBusy(state: AppState, sessionId: string): boolean {
  return state.sessions.find((s) => s.id === sessionId)?.busy ?? false;
}
