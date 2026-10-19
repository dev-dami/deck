import type { Session } from "@web/protocol";

// The desktop's own state, beside the shared reducer's AppState (which
// holds what Deck sent). Pure, so the tests drive it directly.

export type Panel = "changes" | "approvals" | "queue";

export const PANELS: ReadonlyArray<{ panel: Panel; label: string }> = [
  { panel: "changes", label: "Changes" },
  { panel: "approvals", label: "Approvals" },
  { panel: "queue", label: "Queue" },
];

export interface UiState {
  openId: string | null;
  panel: Panel | null;
  palette: boolean;
  // Sessions with news the person has not seen.
  unread: Record<string, true>;
  // The last_text each session had when this window last looked.
  seen: Record<string, string>;
  // Each session's message list as last seen, by reference.
  lists: Record<string, readonly unknown[]>;
}

export const initialUi: UiState = { openId: null, panel: null, palette: false, unread: {}, seen: {}, lists: {} };

export type UiAction =
  | { type: "open"; id: string | null }
  // A panel toggles; null closes whichever is open.
  | { type: "panel"; panel: Panel | null }
  | { type: "palette"; open: boolean }
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
  for (const s of sessions) {
    const text = s.last_text ?? "";
    seen[s.id] = text;
    const before = state.seen[s.id];
    const fresh = before !== undefined && before !== text && s.id !== state.openId;
    if (state.unread[s.id] === true || fresh) unread[s.id] = true;
  }
  if (state.openId !== null) unread = without(unread, state.openId);
  return { ...state, unread, seen };
}

export function reduceUi(state: UiState, a: UiAction): UiState {
  switch (a.type) {
    case "open":
      return {
        ...state,
        openId: a.id,
        palette: false,
        unread: a.id === null ? state.unread : without(state.unread, a.id),
      };
    case "panel":
      return { ...state, panel: a.panel === state.panel ? null : a.panel };
    case "palette":
      return state.palette === a.open ? state : { ...state, palette: a.open };
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
  for (const [id, list] of Object.entries(lists)) {
    if (id !== state.openId && list !== state.lists[id] && unread[id] !== true) unread = { ...unread, [id]: true };
  }
  return { ...state, unread, lists };
}
