import type { Message } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
import { type DeckBridge, NOTIFY_LIMITS } from "../shared/bridge";
import { projectState, subscribe } from "./appState";
import type { InboxItem, RoomStatus } from "./attention";
import type { BoardTask } from "./crew";
import { crewOf, subscribeCrew, teamOf } from "./crewState";
import {
  addEntry,
  badgeCount,
  Burst,
  decide,
  doneTasks,
  markRead,
  markRoomRead,
  newMentions,
  type NotifyEntry,
  type NotifyEvent,
  type NotifyKind,
  type NotifySettings,
  parseEntries,
  roomEvent,
  summarize,
} from "./notifications";
import { type AlertMemo, limitAlerts } from "./limits";
import { limitsOf, subscribeLimits } from "./limitsState";
import type { OpenProject } from "./openProjects";
import { getPrefs, limitThresholds, type Prefs } from "./prefs";
import { isSide } from "./sideChat";
import { onScreen } from "./ui";

// The notification centre: the list behind the bell (ui/NotificationBell),
// kept in this window's localStorage so it outlives a restart, and the
// watch over the open projects that fills it, pops the native
// notifications and sets the badge. The decisions are in notifications.ts.

const KEY = "deck.desktop.notifications";
// How long a burst gathers before one native notification goes out.
const BURST_MS = 1200;

function load(): NotifyEntry[] {
  try {
    return parseEntries(JSON.parse(window.localStorage.getItem(KEY) ?? "[]"));
  } catch {
    return [];
  }
}

let entries: NotifyEntry[] | null = null;
const listeners = new Set<() => void>();

export function notifyEntries(): NotifyEntry[] {
  entries ??= load();
  return entries;
}

function put(next: NotifyEntry[]): void {
  if (next === entries) return;
  entries = next;
  try {
    window.localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    // A locked storage keeps the list for this window only.
  }
  for (const l of listeners) l();
}

export function subscribeNotify(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

export function useNotifyEntries(): NotifyEntry[] {
  return useSyncExternalStore(subscribeNotify, notifyEntries);
}

export const readAll = (ids?: string[]): void => put(markRead(notifyEntries(), ids));
export const clearAll = (): void => put([]);

// What opens an entry's room: App sets it, as it knows the open projects.
let opener: ((source: string, room: string) => void) | null = null;

export function openEntry(e: NotifyEntry): void {
  put(markRoomRead(notifyEntries(), e.source, e.room));
  opener?.(e.source, e.room);
}

export function setNotifyOpener(f: ((source: string, room: string) => void) | null): void {
  opener = f;
}

// A native notification's tag names the room by its project's source, so a
// click still finds it after the project reopened under another key.
export const roomTag = (source: string, room: string): string => `${source}\n${room}`;

export function parseTag(tag: string): { source: string; room: string } | null {
  const i = tag.indexOf("\n");
  return i < 0 ? null : { source: tag.slice(0, i), room: tag.slice(i + 1) };
}

export function notifySettings(p: Prefs): NotifySettings {
  return { enabled: p.notifications, kinds: p.notifyKinds, sound: p.sound, unfocusedOnly: p.notifyUnfocusedOnly };
}

const focused = (): boolean => typeof document === "undefined" || document.hasFocus();

// What the watch remembers of a project between looks.
interface Seen {
  rooms: Record<string, RoomStatus> | null;
  tasks: Map<string, readonly BoardTask[]>;
  messages: Record<string, Message[]> | null;
  read: Map<string, Set<string>>;
  limits: unknown;
}

// What the limit alerts told, by project source, kept so a restart does not
// tell it again (limits.ts limitAlerts).
const MEMO_KEY = "deck.desktop.limitAlerts";

function loadMemo(source: string): AlertMemo | null {
  try {
    const all = JSON.parse(window.localStorage.getItem(MEMO_KEY) ?? "{}") as Record<string, AlertMemo>;
    return typeof all[source] === "object" && all[source] !== null ? all[source] : null;
  } catch {
    return null;
  }
}

function saveMemo(source: string, memo: AlertMemo): void {
  try {
    const all = JSON.parse(window.localStorage.getItem(MEMO_KEY) ?? "{}") as Record<string, AlertMemo>;
    all[source] = memo;
    window.localStorage.setItem(MEMO_KEY, JSON.stringify(all));
  } catch {
    // A locked storage tells again after a restart.
  }
}

// A limit entry with no room opens the Usage page.
export const LIMITS_ROOM = "";

// Watches the open projects for what to tell the person: a room that needs
// them or finished, a task done, a mention. shownKey is the project on
// screen. The first look at a project, and at a room's messages, is a
// starting point, not news.
export function useNotifyCentre(bridge: DeckBridge, open: OpenProject[], shownKey: string | null): void {
  const shown = useRef(shownKey);
  shown.current = shownKey;
  const live = useRef(open);
  live.current = open;
  // Each project's last look, kept while the set of open projects changes.
  const seen = useRef(new Map<string, Seen>());
  const bridgeRef = useRef(bridge);
  bridgeRef.current = bridge;
  const burst = useRef<Burst | null>(null);
  burst.current ??= new Burst(BURST_MS, (events) => {
    const note = summarize(events);
    if (note === null) return;
    void bridgeRef.current
      .notify({
        title: note.title.slice(0, NOTIFY_LIMITS.title),
        body: note.body.slice(0, NOTIFY_LIMITS.body),
        tag: roomTag(note.source, note.room).slice(0, NOTIFY_LIMITS.tag),
        silent: !getPrefs().sound,
      })
      .catch(() => {});
  });
  useEffect(() => () => burst.current?.dispose(), []);

  // The room on screen, in a focused window, has its entries read.
  const look = useCallback(() => {
    const p = live.current.find((x) => x.key === shown.current);
    const sid = p === undefined ? null : onScreen(projectState(p.key).ui);
    if (p !== undefined && sid !== null && focused()) put(markRoomRead(notifyEntries(), p.source, sid));
  }, []);
  useEffect(look, [look, shownKey]);

  useEffect(() => {
    const keys = new Set(open.map((p) => p.key));
    for (const k of seen.current.keys()) if (!keys.has(k)) seen.current.delete(k);
    // Main says whether the window has the focus: the page's own word can
    // lag behind the system's.
    const tell = (p: OpenProject, sid: string, kind: NotifyKind, text: string, at: number) => {
      const e: NotifyEvent = {
        kind,
        source: p.source,
        projectName: p.name,
        room: sid,
        roomTitle: sid === LIMITS_ROOM ? "Plan limits" : sessionTitle(p.c.store.state.sessions.find((x) => x.id === sid)),
        text,
        at: at > 0 ? at : Date.now(),
      };
      const viewing = p.key === shown.current && onScreen(projectState(p.key).ui) === sid;
      void bridgeRef.current
        .focused()
        .catch(focused)
        .then((f) => {
          const d = decide(kind, notifySettings(getPrefs()), { focused: f, viewing });
          if (d.record) put(addEntry(notifyEntries(), e, d.read));
          if (d.native) burst.current?.push(e);
        });
    };
    const check = () => {
      for (const p of open) {
        let w = seen.current.get(p.key);
        if (w === undefined) {
          w = { rooms: null, tasks: new Map(), messages: null, read: new Map(), limits: null };
          seen.current.set(p.key, w);
        }
        const s = p.c.store.state;
        const rooms = projectState(p.key).rooms;
        if (w.rooms !== rooms) {
          for (const [sid, st] of Object.entries(rooms)) {
            const kind = w.rooms === null ? null : roomEvent(w.rooms[sid], st, (s.queue[sid]?.length ?? 0) > 0);
            if (kind !== null) tell(p, sid, kind, st.label, st.at);
          }
          w.rooms = rooms;
        }
        for (const session of s.sessions) {
          if (isSide(session)) continue;
          const tasks = crewOf(p.c.project, session.id)?.board?.tasks;
          if (tasks === undefined || w.tasks.get(session.id) === tasks) continue;
          for (const t of doneTasks(w.tasks.get(session.id), tasks)) tell(p, session.id, "task", `Task done: ${t.title || t.id}`, t.updatedAt);
          w.tasks.set(session.id, tasks);
        }
        if (s.messages !== w.messages) {
          const names = ["user", teamOf(p.c.project)?.name ?? ""];
          for (const [sid, list] of Object.entries(s.messages)) {
            if (w.messages?.[sid] === list) continue;
            // A room's history, loaded when it opens, is not news either.
            const got = newMentions(w.read.get(sid) ?? null, list, names);
            w.read.set(sid, got.seen);
            for (const m of got.mentions) {
              const who = m.message.agent ?? "An agent";
              const text = `${who}: ${m.text.replace(/\s+/g, " ").trim()}`.slice(0, 200);
              tell(p, sid, "mention", text, Date.parse(m.message.created_at) || 0);
            }
          }
          w.messages = s.messages;
        }
        const lim = limitsOf(p.c.project).data;
        if (lim !== null && lim !== w.limits) {
          w.limits = lim;
          const prefs = getPrefs();
          const got = limitAlerts(loadMemo(p.source), lim.history, lim.limits, Date.now(), limitThresholds(prefs), 3, prefs.clock === "12h");
          saveMemo(p.source, got.memo);
          for (const a of got.alerts) {
            // An agent that waits for this reset is in a room: that room.
            const room = lim.waits.find((x) => x.kind === a.kind)?.sessionId ?? LIMITS_ROOM;
            tell(p, room, "limit", a.text, Date.now());
          }
        }
      }
    };
    check();
    const offs = [subscribe(check), subscribe(look), subscribeCrew(check), subscribeLimits(check), ...open.map((p) => p.c.store.subscribe(check))];
    window.addEventListener("focus", look);
    return () => {
      for (const off of offs) off();
      window.removeEventListener("focus", look);
    };
  }, [open, look]);
}

// The dock or taskbar badge: the rooms that need the person or told them
// something unread.
export function useBadge(bridge: DeckBridge, items: InboxItem[]): void {
  const list = useNotifyEntries();
  const count = Math.min(NOTIFY_LIMITS.badge, badgeCount(items.filter((i) => i.state === "needs-you"), list));
  useEffect(() => {
    void bridge.badge(count).catch(() => {});
  }, [bridge, count]);
}
