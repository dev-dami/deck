import type { Store } from "@web/controller";
import { useEffect, useSyncExternalStore } from "react";
import type { Board, Crew, Team } from "./crew";
import type { ProjectApi } from "./project";
import { teamOn } from "./teamGate";

// The task board, agents and team of each local project, polled once for
// the whole window: the sidebar, the inbox and the Agents panel read the
// same data, whether the panel is open or not. Deck has no board or agents
// events yet, so this polls: every 3 s while an agent works, else every 10 s.

export interface RoomCrew {
  board: Board | null;
  crew: Crew | null;
  // When it was fetched (ms).
  at: number;
}

const BUSY_MS = 3000;
const IDLE_MS = 10_000;
// The team (for contract proposals) changes slowly.
const TEAM_MS = 30_000;

interface Watch {
  rooms: Map<string, RoomCrew>;
  team: Team | null;
  onChange: () => void;
}

const watches = new Map<ProjectApi, Watch>();
const listeners = new Set<() => void>();

function changed(w: Watch): void {
  w.onChange();
  for (const l of listeners) l();
}

function watchOf(api: ProjectApi): Watch {
  let w = watches.get(api);
  if (w === undefined) {
    // An api nobody watches (a test, a panel before its project): kept until
    // its project is watched.
    w = { rooms: new Map(), team: null, onChange: () => {} };
    watches.set(api, w);
  }
  return w;
}

// Fetches one room's board and agents now.
export async function refreshCrew(api: ProjectApi, sid: string): Promise<void> {
  const [b, c] = await Promise.all([api.board(sid), api.agents(sid)]);
  const w = watchOf(api);
  const cur = w.rooms.get(sid);
  w.rooms.set(sid, {
    board: typeof b === "string" ? (cur?.board ?? null) : b,
    crew: typeof c === "string" ? (cur?.crew ?? null) : c,
    at: Date.now(),
  });
  changed(w);
}

export async function refreshTeam(api: ProjectApi): Promise<void> {
  const t = await api.team();
  if (typeof t === "string") return;
  const w = watchOf(api);
  w.team = t.available ? t : null;
  changed(w);
}

export function crewOf(api: ProjectApi | undefined, sid: string): RoomCrew | undefined {
  return api === undefined ? undefined : watches.get(api)?.rooms.get(sid);
}

// null with the team off (teamGate.ts), whose contracts then leave the inbox.
export function teamOf(api: ProjectApi | undefined): Team | null {
  return api === undefined || !teamOn() ? null : (watches.get(api)?.team ?? null);
}

// Polls a project's rooms: those that work, the open one, and each room
// once more after it stops. Returns the function that stops it.
export function watchCrew(api: ProjectApi, store: Store, openId: () => string | null, onChange: () => void): () => void {
  const w = watchOf(api);
  w.onChange = onChange;
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  let busyBefore = new Set<string>();
  let teamAt = 0;
  const tick = async () => {
    const busy = new Set(store.state.sessions.filter((s) => s.busy).map((s) => s.id));
    const ids = new Set([...busy, ...busyBefore]);
    const open = openId();
    if (open !== null) ids.add(open);
    busyBefore = busy;
    const jobs: Promise<void>[] = [...ids].map((id) => refreshCrew(api, id));
    if (teamOn() && Date.now() - teamAt >= TEAM_MS) {
      teamAt = Date.now();
      jobs.push(refreshTeam(api));
    }
    await Promise.allSettled(jobs);
    if (!stopped) timer = setTimeout(() => void tick(), busy.size > 0 ? BUSY_MS : IDLE_MS);
  };
  void tick();
  return () => {
    stopped = true;
    if (timer !== null) clearTimeout(timer);
    watches.delete(api);
  };
}

export function subscribeCrew(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

// One room's board and agents for a view, fetched at once when it mounts.
export function useRoomCrew(api: ProjectApi | undefined, sid: string | null): RoomCrew | undefined {
  useEffect(() => {
    if (api !== undefined && sid !== null) void refreshCrew(api, sid);
  }, [api, sid]);
  return useSyncExternalStore(subscribeCrew, () => (sid === null ? undefined : crewOf(api, sid)));
}
