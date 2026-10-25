import { useSyncExternalStore } from "react";

// Archive with Undo. Deck has no call to unarchive a thread (a new message
// brings it back), so the shell waits ARCHIVE_UNDO_MS before it asks Deck to
// archive, and Undo cancels the wait. Meanwhile the room shows as archived.

export const ARCHIVE_UNDO_MS = 5000;

export class ArchiveQueue {
  // The timer of each waiting key; null once the call went to Deck.
  private readonly waiting = new Map<string, ReturnType<typeof setTimeout> | null>();
  private keys: ReadonlySet<string> = new Set();
  private readonly listeners = new Set<() => void>();

  // Runs archive after ARCHIVE_UNDO_MS unless undo(key) comes first. The key
  // stays waiting until archive settles, so the room does not show live
  // between the call and Deck's answer.
  schedule(key: string, archive: () => Promise<unknown>): void {
    if (this.waiting.has(key)) return;
    const fire = () => {
      this.waiting.set(key, null);
      void archive().finally(() => this.drop(key));
    };
    this.waiting.set(key, setTimeout(fire, ARCHIVE_UNDO_MS));
    this.changed();
  }

  // Cancels a waiting archive; false when it was not waiting or already went.
  undo(key: string): boolean {
    const t = this.waiting.get(key);
    if (t === undefined || t === null) return false;
    clearTimeout(t);
    this.drop(key);
    return true;
  }

  private drop(key: string): void {
    if (this.waiting.delete(key)) this.changed();
  }

  private changed(): void {
    this.keys = new Set(this.waiting.keys());
    for (const l of this.listeners) l();
  }

  // The keys that wait, a new set after each change.
  readonly snapshot = (): ReadonlySet<string> => this.keys;

  readonly subscribe = (fn: () => void): (() => void) => {
    this.listeners.add(fn);
    return () => {
      this.listeners.delete(fn);
    };
  };
}

// The window's queue; keys are "<project source>/<session id>".
export const archiving = new ArchiveQueue();

export function useArchiving(): ReadonlySet<string> {
  return useSyncExternalStore(archiving.subscribe, archiving.snapshot);
}
