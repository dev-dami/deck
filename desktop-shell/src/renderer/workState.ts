import { useEffect, useSyncExternalStore } from "react";
import type { ProjectApi } from "./project";
import type { Work } from "./worktrees";

// Each room's worktrees and capsules, fetched once for every view that
// shows them (the Work tab, the lane strip's branch badges, the Work
// Graph). Deck has no work events, so a room is polled while some view of
// it is mounted, and at once after an action changed it. A Deck that does
// not serve Work answers once and is left alone.

const POLL_MS = 10_000;

interface Room {
  work: Work | null;
  // How many views watch it, and the poll that runs while any does.
  views: number;
  timer: ReturnType<typeof setTimeout> | null;
}

const rooms = new Map<ProjectApi, Map<string, Room>>();
const listeners = new Set<() => void>();
let version = 0;

function roomOf(api: ProjectApi, sid: string): Room {
  let m = rooms.get(api);
  if (m === undefined) {
    m = new Map();
    rooms.set(api, m);
  }
  let r = m.get(sid);
  if (r === undefined) {
    r = { work: null, views: 0, timer: null };
    m.set(sid, r);
  }
  return r;
}

// Fetches a room's work now; the error, or null.
export async function refreshWork(api: ProjectApi, sid: string): Promise<string | null> {
  const w = await api.work(sid);
  if (typeof w === "string") return w;
  roomOf(api, sid).work = w;
  version++;
  for (const l of listeners) l();
  return null;
}

export function workOf(api: ProjectApi | undefined, sid: string | null): Work | null {
  return api === undefined || sid === null ? null : (rooms.get(api)?.get(sid)?.work ?? null);
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

// A room's work for a view: fetched when it mounts, then polled while any
// view of the room is mounted. on is false for a Deck that does not serve it.
export function useRoomWork(api: ProjectApi | undefined, sid: string | null, on = true): Work | null {
  useEffect(() => {
    if (api === undefined || sid === null || !on) return;
    const r = roomOf(api, sid);
    r.views++;
    const tick = async () => {
      r.timer = null;
      await refreshWork(api, sid);
      if (r.views > 0 && r.timer === null) r.timer = setTimeout(() => void tick(), POLL_MS);
    };
    if (r.views === 1) void tick();
    return () => {
      r.views--;
      if (r.views === 0 && r.timer !== null) {
        clearTimeout(r.timer);
        r.timer = null;
      }
    };
  }, [api, sid, on]);
  useSyncExternalStore(subscribe, () => version);
  return on ? workOf(api, sid) : null;
}
