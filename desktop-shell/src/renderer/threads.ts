import type { Session } from "@web/protocol";
import { toMillis } from "@web/time";

// The sidebar's view of a thread, after T3 Code's thread rows: a status
// that a person may need to act on, else the time.
export type ThreadStatus = "approval" | "working" | "done" | null;

export function threadStatus(s: Session, unread: boolean): ThreadStatus {
  if (s.pending > 0) return "approval";
  if (s.busy) return "working";
  if (unread) return "done";
  return null;
}

// A thread settles when nothing has happened in it for this long.
export const SETTLE_AFTER_MS = 3 * 24 * 60 * 60 * 1000;

// Live threads first, as Deck orders them; the settled ones after, behind
// a fold. A thread that is open, works, waits or has news never settles.
export function splitSettled(
  sessions: Session[],
  now: number,
  openId: string | null,
  unread: Record<string, true>,
): { live: Session[]; settled: Session[] } {
  const live: Session[] = [];
  const settled: Session[] = [];
  for (const s of sessions) {
    const quiet = now - toMillis(s.updated_at) > SETTLE_AFTER_MS;
    const holds = s.busy || s.pending > 0 || unread[s.id] === true || s.id === openId;
    (quiet && !holds ? settled : live).push(s);
  }
  return { live, settled };
}
