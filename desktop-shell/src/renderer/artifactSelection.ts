import { useSyncExternalStore } from "react";

// Selection belongs to a client and a room, shared by chat cards and the
// side panel. It survives closing the panel without leaking across projects.
const selected = new WeakMap<object, Map<string, string>>();
const listeners = new Set<() => void>();
const subscribe = (fn: () => void) => { listeners.add(fn); return () => { listeners.delete(fn); }; };

export function selectArtifact(client: object, sid: string, id: string): void {
  let rooms = selected.get(client);
  if (rooms === undefined) { rooms = new Map(); selected.set(client, rooms); }
  if (rooms.get(sid) === id) return;
  rooms.set(sid, id);
  for (const fn of listeners) fn();
}

export function useSelectedArtifact(client: object, sid: string): string | null {
  return useSyncExternalStore(subscribe, () => selected.get(client)?.get(sid) ?? null, () => null);
}
