import { useEffect, useState } from "react";
import { projectState, subscribe } from "./appState";
import { type InboxItem, inboxItems } from "./attention";
import { subscribeCrew, teamOf } from "./crewState";
import type { OpenProject } from "./openProjects";
import { isSide } from "./sideChat";

// The window's attention across its open projects: the inbox, read from the
// room statuses in appState.ts. The notifications and the badge are in
// notifyCentre.ts.

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
