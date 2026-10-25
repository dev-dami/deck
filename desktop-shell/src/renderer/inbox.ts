import { sessionTitle } from "@web/state";
import { useEffect, useState } from "react";
import { type DeckBridge, NOTIFY_LIMITS } from "../shared/bridge";
import { projectState, subscribe } from "./appState";
import { type InboxItem, inboxItems, type RoomState, type RoomStatus, shouldNotify } from "./attention";
import { subscribeCrew, teamOf } from "./crewState";
import type { OpenProject } from "./openProjects";
import { getPrefs } from "./prefs";
import { isSide } from "./sideChat";

// The window's attention across its open projects: the inbox, the badge and
// the notifications, all read from the room statuses in appState.ts.

// A notification's tag names the room: the project's key and the session.
export const roomTag = (key: string, sid: string): string => `${key}\n${sid}`;

export function parseTag(tag: string): { project: string; room: string } | null {
  const i = tag.indexOf("\n");
  return i < 0 ? null : { project: tag.slice(0, i), room: tag.slice(i + 1) };
}

export function useInbox(open: OpenProject[]): InboxItem[] {
  const [items, setItems] = useState<InboxItem[]>([]);
  useEffect(() => {
    let parts: unknown[] = [];
    const read = () => {
      const next = open.flatMap((p) => {
        const ps = projectState(p.key);
        const s = p.c.store.state;
        return [ps.rooms, ps.notices, s.sessions, s.approvals, teamOf(p.c.project), p.c.rooms?.snapshot()];
      });
      if (next.length === parts.length && next.every((x, i) => x === parts[i])) return;
      parts = next;
      setItems(
        inboxItems(
          open.map((p) => {
            const ps = projectState(p.key);
            const s = p.c.store.state;
            return {
              key: p.key,
              source: p.source,
              name: p.name,
              // An archived room asks for nothing.
              sessions: s.sessions.filter((x) => !isSide(x) && p.c.rooms?.get(x.id).archived !== true),
              rooms: ps.rooms,
              approvals: s.approvals,
              notices: ps.notices,
              contracts: teamOf(p.c.project)?.contracts ?? [],
            };
          }),
        ),
      );
    };
    read();
    const offs = [
      subscribe(read),
      subscribeCrew(read),
      ...open.map((p) => p.c.store.subscribe(read)),
      ...open.flatMap((p) => (p.c.rooms === undefined ? [] : [p.c.rooms.subscribe(read)])),
    ];
    return () => {
      for (const off of offs) off();
    };
  }, [open]);
  return items;
}

// The dock or taskbar badge: how many items need the person.
export function useBadge(bridge: DeckBridge, items: InboxItem[]): void {
  const count = Math.min(9999, items.filter((i) => i.state === "needs-you").length);
  useEffect(() => {
    void bridge.badge(count).catch(() => {});
  }, [bridge, count]);
}

// A native notification when a room enters needs-you or finishes while the
// window is in the background. The first statuses a project shows are its
// starting point, not news.
export function useNotifications(bridge: DeckBridge, open: OpenProject[]): void {
  useEffect(() => {
    const states = new Map<string, RoomState>();
    const last = new Map<string, Record<string, RoomStatus>>();
    const check = () => {
      for (const p of open) {
        const rooms = projectState(p.key).rooms;
        if (last.get(p.key) === rooms) continue;
        last.set(p.key, rooms);
        const s = p.c.store.state;
        const prefs = getPrefs();
        for (const [sid, st] of Object.entries(rooms)) {
          const tag = roomTag(p.key, sid);
          const before = states.get(tag);
          states.set(tag, st.state);
          if (!shouldNotify(before, st.state, prefs.notifications, (s.queue[sid]?.length ?? 0) > 0)) continue;
          const title = `${p.name} · ${sessionTitle(s.sessions.find((x) => x.id === sid))}`.slice(0, NOTIFY_LIMITS.title);
          const body = st.label.slice(0, NOTIFY_LIMITS.body);
          void bridge
            .focused()
            .then((focused) => (focused ? undefined : bridge.notify({ title, body, tag, silent: !prefs.sound })))
            .catch(() => {});
        }
      }
    };
    check();
    return subscribe(check);
  }, [bridge, open]);
}
