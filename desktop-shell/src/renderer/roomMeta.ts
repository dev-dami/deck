// What a local Deck says of a room beyond the shared Session: what it waits
// for, its branch and whether it is archived (deck.v1 Session), the leases
// its agents hold (lease_changed) and the notices the engine marked
// (Notice.via). The phone protocol has none of these, so they are kept
// here, by session id, beside the shared state.

export const ATTENTION = ["approval", "question", "error", "blocked"] as const;
export type Attention = (typeof ATTENTION)[number] | "";

export interface Lease {
  agent: string;
  paths: string[];
  exclusive: boolean;
}

export interface RoomNote {
  text: string;
  // "overlap" for an overlap note.
  via: string;
}

export interface RoomMeta {
  attention: Attention;
  branch: string;
  archived: boolean;
  leases: Lease[];
  // The marked notices, oldest first.
  notes: RoomNote[];
}

export const EMPTY_ROOM: RoomMeta = { attention: "", branch: "", archived: false, leases: [], notes: [] };

export type RoomEvent =
  | { k: "session"; id: string; attention: Attention; branch: string; archived: boolean }
  | { k: "deleted"; id: string }
  | { k: "lease"; session: string; lease: Lease; released: boolean }
  | { k: "note"; session: string; note: RoomNote };

export type RoomUpdate = Extract<RoomEvent, { k: "session" }>;

const MAX_NOTES = 20;

const sameLease = (a: Lease, b: Lease): boolean =>
  a.agent === b.agent && a.paths.length === b.paths.length && a.paths.every((p, i) => p === b.paths[i]);

export class RoomMetaStore {
  private rooms: ReadonlyMap<string, RoomMeta> = new Map();
  private readonly listeners = new Set<() => void>();

  get(id: string): RoomMeta {
    return this.rooms.get(id) ?? EMPTY_ROOM;
  }

  // A new map after each change, for useSyncExternalStore.
  readonly snapshot = (): ReadonlyMap<string, RoomMeta> => this.rooms;

  readonly subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };

  apply(ev: RoomEvent): void {
    const next = new Map(this.rooms);
    switch (ev.k) {
      case "session":
        next.set(ev.id, { ...this.get(ev.id), attention: ev.attention, branch: ev.branch, archived: ev.archived });
        break;
      case "deleted":
        next.delete(ev.id);
        break;
      case "lease": {
        const cur = this.get(ev.session);
        // A replayed event changes nothing: the same lease is kept once.
        const leases = cur.leases.filter((l) => !sameLease(l, ev.lease));
        if (!ev.released) leases.push(ev.lease);
        next.set(ev.session, { ...cur, leases });
        break;
      }
      case "note": {
        const cur = this.get(ev.session);
        next.set(ev.session, { ...cur, notes: [...cur.notes, ev.note].slice(-MAX_NOTES) });
        break;
      }
    }
    this.set(next);
  }

  // A full session list: the rooms it names are updated, the others are
  // gone. Leases and notes stay, as the list does not carry them.
  load(list: RoomUpdate[]): void {
    const next = new Map<string, RoomMeta>();
    for (const r of list) next.set(r.id, { ...this.get(r.id), attention: r.attention, branch: r.branch, archived: r.archived });
    this.set(next);
  }

  private set(next: ReadonlyMap<string, RoomMeta>): void {
    this.rooms = next;
    for (const l of this.listeners) l();
  }
}
