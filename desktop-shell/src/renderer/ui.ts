import type { Session } from "@web/protocol";

// The desktop's own state, beside the shared reducer's AppState (which
// holds what Deck sent). Pure, so the tests drive it directly.

// The room's tabs. The queue is the first section of Tasks; approvals sit
// over the composer and in the inbox.
export type Panel = "changes" | "tasks" | "agents" | "side";

export const PANELS: ReadonlyArray<{ panel: Panel; label: string }> = [
  { panel: "changes", label: "Changes" },
  { panel: "tasks", label: "Tasks" },
  { panel: "agents", label: "Agents" },
  { panel: "side", label: "Side chat" },
];

export type SettingsSection = "general" | "appearance" | "agents" | "connection" | "phone" | "keybindings";

export const SETTINGS_SECTIONS: ReadonlyArray<{ section: SettingsSection; label: string }> = [
  { section: "general", label: "General" },
  { section: "appearance", label: "Appearance" },
  { section: "agents", label: "Agents" },
  { section: "connection", label: "Connection" },
  { section: "phone", label: "Phone" },
  { section: "keybindings", label: "Keyboard shortcuts" },
];

export interface UiState {
  openId: string | null;
  // The Settings page, when it is open, in place of the chat.
  settings: SettingsSection | null;
  // The Usage and Team pages, likewise.
  usage: boolean;
  team: boolean;
  // The inbox across projects, likewise.
  inbox: boolean;
  // The project is the one on screen. A room open in a project that is not
  // shown gathers news like any other.
  shown: boolean;
  panel: Panel | null;
  palette: boolean;
  // Sessions with news the person has not seen.
  unread: Record<string, true>;
  // The last_text each session had when this window last looked.
  seen: Record<string, string>;
  // Each session's message list as last seen, by reference.
  lists: Record<string, readonly unknown[]>;
}

export const initialUi: UiState = {
  openId: null,
  settings: null,
  usage: false,
  team: false,
  inbox: false,
  shown: true,
  panel: null,
  palette: false,
  unread: {},
  seen: {},
  lists: {},
};

export type UiAction =
  | { type: "open"; id: string | null }
  // A panel toggles; null closes whichever is open.
  | { type: "panel"; panel: Panel | null }
  | { type: "palette"; open: boolean }
  | { type: "settings"; section: SettingsSection | null }
  | { type: "usage"; open: boolean }
  | { type: "team"; open: boolean }
  | { type: "inbox"; open: boolean }
  | { type: "shown"; on: boolean }
  | { type: "sessions"; sessions: Session[] }
  // The reducer's messages by session (AppState.messages).
  | { type: "messages"; messages: Record<string, readonly unknown[]> };

function without(rec: Record<string, true>, id: string): Record<string, true> {
  if (!(id in rec)) return rec;
  const { [id]: _gone, ...rest } = rec;
  return rest;
}

// Scoped message events reach only the open session (§3.3), so a session in
// the background shows news through its session.updated: a new last_text
// marks it unread. The first list a window gets marks nothing.
function trackUnread(state: UiState, sessions: Session[]): UiState {
  let unread: Record<string, true> = {};
  const seen: Record<string, string> = {};
  const open = onScreen(state);
  for (const s of sessions) {
    const text = s.last_text ?? "";
    seen[s.id] = text;
    const before = state.seen[s.id];
    const fresh = before !== undefined && before !== text && s.id !== open;
    if (state.unread[s.id] === true || fresh) unread[s.id] = true;
  }
  if (open !== null) unread = without(unread, open);
  return { ...state, unread, seen };
}

// The room the person sees: the open one of the project on screen, when no
// page (settings, usage, team, inbox) stands in its place.
export function onScreen(state: UiState): string | null {
  const page = state.settings !== null || state.usage || state.team || state.inbox;
  return state.shown && !page ? state.openId : null;
}

export function reduceUi(state: UiState, a: UiAction): UiState {
  const next = step(state, a);
  // The room on screen has no news the person has not seen.
  const open = onScreen(next);
  return open !== null && next.unread[open] === true ? { ...next, unread: without(next.unread, open) } : next;
}

function step(state: UiState, a: UiAction): UiState {
  switch (a.type) {
    case "open":
      return { ...state, openId: a.id, settings: null, usage: false, team: false, inbox: false, palette: false };
    case "panel":
      return { ...state, panel: a.panel === state.panel ? null : a.panel };
    case "palette":
      return state.palette === a.open ? state : { ...state, palette: a.open };
    case "settings":
      return a.section === null
        ? { ...state, settings: null, palette: false }
        : { ...state, settings: a.section, usage: false, team: false, inbox: false, palette: false };
    case "usage":
      return a.open
        ? { ...state, usage: true, team: false, inbox: false, settings: null, palette: false }
        : { ...state, usage: false, palette: false };
    case "team":
      return a.open
        ? { ...state, team: true, usage: false, inbox: false, settings: null, palette: false }
        : { ...state, team: false, palette: false };
    case "inbox":
      return a.open
        ? { ...state, inbox: true, usage: false, team: false, settings: null, palette: false }
        : { ...state, inbox: false, palette: false };
    case "shown":
      return state.shown === a.on ? state : { ...state, shown: a.on };
    case "sessions": {
      const next = trackUnread(state, a.sessions);
      // A deleted open session closes.
      const open = state.openId;
      return open !== null && !a.sessions.some((s) => s.id === open) ? { ...next, openId: null } : next;
    }
    case "messages":
      return trackActivity(state, a.messages);
  }
}

// A transport that streams every session (rpc) changes the message list of
// a session in the background; that marks it unread at once. Over v2 only
// the open session's list changes, so this marks nothing there.
function trackActivity(state: UiState, lists: Record<string, readonly unknown[]>): UiState {
  let unread = state.unread;
  const open = onScreen(state);
  for (const [id, list] of Object.entries(lists)) {
    if (id !== open && list !== state.lists[id] && unread[id] !== true) unread = { ...unread, [id]: true };
  }
  return { ...state, unread, lists };
}
