import type { Store } from "@web/controller";
import { useEffect, useReducer, useSyncExternalStore } from "react";
import type { Limits } from "./limits";
import type { ProjectApi } from "./project";

// Each local project's plan limits, polled once for the whole window: the
// quota chips, the room's wait banner, the queue's scheduled messages, the
// Usage page and the limit notifications read the same data. The engine
// has no limits events, so this polls every minute, and soon after an
// agent stops working (its turn may have run into the limit) or a limits
// call changed something.

const POLL_MS = 60_000;
// After a room's agents stop, or a call: long enough for the engine to
// have taken the turn's end in.
const SOON_MS = 1200;
// The record the alerts' burn rate reads.
export const HISTORY_HOURS = 6;

export interface LimitsView {
  data: Limits | null;
  // When it was fetched (ms).
  at: number;
}

interface Watch {
  view: LimitsView;
  poke: () => void;
}

const watches = new Map<ProjectApi, Watch>();
const listeners = new Set<() => void>();
const EMPTY: LimitsView = { data: null, at: 0 };

function changed(): void {
  for (const l of listeners) l();
}

function watchOf(api: ProjectApi): Watch {
  let w = watches.get(api);
  if (w === undefined) {
    w = { view: EMPTY, poke: () => void refreshLimits(api) };
    watches.set(api, w);
  }
  return w;
}

export async function refreshLimits(api: ProjectApi): Promise<void> {
  const r = await api.limits(HISTORY_HOURS);
  if (typeof r === "string") return;
  watchOf(api).view = { data: r, at: Date.now() };
  changed();
}

// Asks for a refresh shortly (after a call that changed the waits).
export function pokeLimits(api: ProjectApi | undefined): void {
  if (api !== undefined) setTimeout(() => watchOf(api).poke(), SOON_MS);
}

export function limitsOf(api: ProjectApi | undefined): LimitsView {
  return api === undefined ? EMPTY : (watches.get(api)?.view ?? EMPTY);
}

// Polls a project's limits while it is open, when its Deck serves them.
// Returns the function that stops it.
export function watchLimits(api: ProjectApi, store: Store, serves: () => boolean): () => void {
  let stopped = false;
  let timer: ReturnType<typeof setTimeout> | null = null;
  const tick = async () => {
    timer = null;
    // Until the Deck said what it serves (Info), look again soon.
    const ok = serves();
    if (ok) await refreshLimits(api);
    if (!stopped) timer = setTimeout(() => void tick(), ok ? POLL_MS : SOON_MS);
  };
  const soon = () => {
    if (stopped) return;
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(() => void tick(), SOON_MS);
  };
  watchOf(api).poke = soon;
  // A room that stops working may have hit a limit; one that starts may
  // have been resumed.
  let busy = "";
  const off = store.subscribe(() => {
    const now = store.state.sessions
      .filter((s) => s.busy)
      .map((s) => s.id)
      .join(",");
    if (now !== busy) {
      busy = now;
      soon();
    }
  });
  void tick();
  return () => {
    stopped = true;
    off();
    if (timer !== null) clearTimeout(timer);
    watches.delete(api);
    changed();
  };
}

export function subscribeLimits(l: () => void): () => void {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
}

export function useLimits(api: ProjectApi | undefined): LimitsView {
  return useSyncExternalStore(subscribeLimits, () => limitsOf(api));
}

// A clock for views that count down: now, again every interval.
export function useNow(ms = 30_000): number {
  const [, tick] = useReducer((n: number) => n + 1, 0);
  useEffect(() => {
    const id = window.setInterval(tick, ms);
    return () => window.clearInterval(id);
  }, [ms]);
  return Date.now();
}
