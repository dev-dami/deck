import { useSyncExternalStore } from "react";
import type { AgentSummary, BrowserTab, HighlightItem, HighlightsResponse, HighlightSource } from "../shared/bridge";
import type { DeckClient } from "./client";
import type { LiveEvent } from "./rpcMap";

// Highlights and the browser's tabs of each room, as the Deck last said
// them: a snapshot per room, then the deltas of the event stream. An item
// or a line is replaced only by one at least as new (updated_at), so a
// replayed event changes nothing and a snapshot read before a delta never
// overwrites it. Tabs carry no time: a snapshot is dropped when a delta
// arrived while it was being fetched (browserStamp).

export interface RoomLive {
  items: readonly HighlightItem[];
  summaries: readonly AgentSummary[];
  tabs: readonly BrowserTab[];
  // The desktop browser is attached and can take commands.
  browserAvailable: boolean;
}

export const EMPTY_LIVE: RoomLive = { items: [], summaries: [], tabs: [], browserAvailable: false };

// The Deck fills in a source after the fact (the message of the turn)
// without touching updated_at, so at the same time the one that names a
// source is the later.
const hasSource = (s: HighlightSource | null): s is HighlightSource => s !== null && (s.message_id !== "" || s.task_id !== "");

function pick<T extends { updated_at: number; source: HighlightSource | null }>(have: T, next: T): T {
  if (have.updated_at !== next.updated_at) return have.updated_at > next.updated_at ? have : next;
  return hasSource(next.source) || !hasSource(have.source) ? next : { ...next, source: have.source };
}

// The list with item in it, unless it holds the same item at a later time.
function mergeItem(list: readonly HighlightItem[], item: HighlightItem): readonly HighlightItem[] {
  const have = list.find((i) => i.id === item.id);
  if (have === undefined) return [...list, item];
  const keep = pick(have, item);
  return keep === have ? list : list.map((i) => (i.id === item.id ? keep : i));
}

function mergeSummary(list: readonly AgentSummary[], s: AgentSummary): readonly AgentSummary[] {
  const have = list.find((x) => x.agent === s.agent);
  if (have === undefined) return [...list, s];
  const keep = pick(have, s);
  return keep === have ? list : list.map((x) => (x.agent === s.agent ? keep : x));
}

export class LiveStore {
  private rooms: ReadonlyMap<string, RoomLive> = new Map();
  private readonly listeners = new Set<() => void>();
  private readonly browserEvents = new Map<string, number>();

  get(id: string): RoomLive {
    return this.rooms.get(id) ?? EMPTY_LIVE;
  }

  readonly snapshot = (): ReadonlyMap<string, RoomLive> => this.rooms;

  readonly subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };

  loadHighlights(id: string, r: HighlightsResponse): void {
    const cur = this.get(id);
    const items = r.items.reduce((acc, i) => mergeItem(acc, i), cur.items);
    const summaries = r.summaries.reduce((acc, s) => mergeSummary(acc, s), cur.summaries);
    this.patch(id, { items, summaries });
  }

  // Taken before a snapshot is fetched, given back with it.
  browserStamp(id: string): number {
    return this.browserEvents.get(id) ?? 0;
  }

  // A snapshot of the tabs; dropped when a delta came in since stamp.
  loadBrowser(id: string, tabs: readonly BrowserTab[], available: boolean, stamp?: number): void {
    if (stamp !== undefined && stamp !== this.browserStamp(id)) return;
    this.patch(id, { tabs, browserAvailable: available });
  }

  // The tab a command of the person's answered with, unless a delta came in
  // since the command left (stamp): the delta is the later word, and an
  // answer must not undo a close or a takeover.
  applyTab(tab: BrowserTab, stamp?: number): void {
    if (stamp !== undefined && stamp !== this.browserStamp(tab.session_id)) return;
    const cur = this.get(tab.session_id).tabs;
    const tabs = cur.some((t) => t.id === tab.id) ? cur.map((t) => (t.id === tab.id ? tab : t)) : [...cur, tab];
    this.bumpBrowser(tab.session_id);
    this.patch(tab.session_id, { tabs });
  }

  // The item a Highlights action of the person's answered with.
  applyItem(item: HighlightItem): void {
    this.patch(item.session_id, { items: mergeItem(this.get(item.session_id).items, item) });
  }

  private bumpBrowser(id: string): void {
    this.browserEvents.set(id, this.browserStamp(id) + 1);
  }

  apply(ev: LiveEvent): void {
    switch (ev.k) {
      case "highlight":
        this.applyItem(ev.item);
        return;
      case "summary":
        this.patch(ev.summary.session_id, { summaries: mergeSummary(this.get(ev.summary.session_id).summaries, ev.summary) });
        return;
      case "browser":
        this.bumpBrowser(ev.state.session_id);
        this.patch(ev.state.session_id, { tabs: ev.state.tabs, browserAvailable: ev.state.available });
        return;
    }
  }

  // A room that is gone takes its items with it.
  drop(id: string): void {
    if (!this.rooms.has(id)) return;
    const next = new Map(this.rooms);
    next.delete(id);
    this.set(next);
  }

  private patch(id: string, part: Partial<RoomLive>): void {
    const next = new Map(this.rooms);
    next.set(id, { ...this.get(id), ...part });
    this.set(next);
  }

  private set(next: ReadonlyMap<string, RoomLive>): void {
    this.rooms = next;
    for (const l of this.listeners) l();
  }
}

const noSubscribe = () => () => {};

// The room's Highlights and tabs; empty for a Deck that does not serve them.
export function useRoomLive(c: DeckClient, sessionId: string | null): RoomLive {
  const live = c.live;
  return useSyncExternalStore(live === undefined ? noSubscribe : live.subscribe, () => (live === undefined || sessionId === null ? EMPTY_LIVE : live.get(sessionId)));
}
