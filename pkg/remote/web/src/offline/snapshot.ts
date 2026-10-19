// What the phone shows while the computer is away: the last snapshot the
// computer left on the relay, opened with the computer's key and kept locally
// (docs/mobile-contract.md §6).
import { b64Decode, b64urlDecode } from "../b64";
import { open } from "../e2e/seal";
import { decodeSnapshot, type Snapshot } from "../protocol3";
import { prologueFor } from "../session3";
import type { ComputerRecord } from "../store/computers";
import { dbGetAll, dbPut } from "../store/db";

export interface ComputerState {
  online: boolean;
  lastSeen: string | null;
}

export interface LoadedSnapshot {
  snapshot: Snapshot;
  computer: ComputerState;
  // When this copy was fetched from the relay.
  fetchedAt: string;
  // True when the relay could not be reached and this is the saved copy.
  stale: boolean;
}

export interface SnapshotDeps {
  fetch?: typeof fetch;
  now?: () => number;
}

const STORE = "snapshots";

async function cached(cid: string): Promise<LoadedSnapshot | null> {
  const row = (await dbGetAll(STORE)).find((r) => r["cid"] === cid);
  if (row === undefined) return null;
  const snapshot = decodeSnapshot(row["snapshot"]);
  const computer = row["computer"] as { online?: unknown; lastSeen?: unknown } | undefined;
  if (snapshot === null || typeof row["fetchedAt"] !== "string") return null;
  return {
    snapshot,
    computer: {
      online: computer?.online === true,
      lastSeen: typeof computer?.lastSeen === "string" ? computer.lastSeen : null,
    },
    fetchedAt: row["fetchedAt"],
    stale: true,
  };
}

// The copy saved on this phone, if any.
export function savedSnapshot(cid: string): Promise<LoadedSnapshot | null> {
  return cached(cid);
}

// Fetches the newest snapshot from the relay and saves it. If the relay cannot
// be reached, falls back to the saved copy (marked stale); null when there is
// nothing to show.
export async function loadSnapshot(
  computer: ComputerRecord,
  deps: SnapshotDeps = {},
): Promise<LoadedSnapshot | null> {
  const statik =
    computer.computerStatic !== undefined ? b64urlDecode(computer.computerStatic) : null;
  if (computer.phoneKey === undefined || statik === null || computer.relayToken === null) {
    return null;
  }
  const doFetch = deps.fetch ?? fetch;
  let res: Response;
  try {
    res = await doFetch(
      `${computer.relayUrl.replace(/\/+$/, "")}/r/v1/c/${encodeURIComponent(computer.cid)}/snap`,
      { headers: { authorization: `Bearer ${computer.relayToken}` } },
    );
  } catch {
    return cached(computer.cid);
  }
  if (res.status === 404) return cached(computer.cid); // none written yet
  if (!res.ok) return cached(computer.cid);

  let body: unknown;
  try {
    body = await res.json();
  } catch {
    return cached(computer.cid);
  }
  const b = body as { b64?: unknown; computer?: { online?: unknown; last_seen?: unknown } };
  const sealed = typeof b.b64 === "string" ? b64Decode(b.b64) : null;
  if (sealed === null) return cached(computer.cid);

  let json: unknown;
  try {
    // The snapshot is a Noise K message with the computer as initiator.
    const opened = await open({
      recipientStatic: computer.phoneKey,
      senderStaticPub: statik,
      prologue: prologueFor(computer.cid),
      message: sealed,
    });
    json = JSON.parse(new TextDecoder().decode(opened.plaintext));
  } catch {
    // Not from this computer, or damaged: never show it.
    return cached(computer.cid);
  }
  const snapshot = decodeSnapshot(json);
  if (snapshot === null) return cached(computer.cid);

  const state: ComputerState = {
    online: b.computer?.online === true,
    lastSeen: typeof b.computer?.last_seen === "string" ? b.computer.last_seen : null,
  };
  const fetchedAt = new Date((deps.now ?? Date.now)()).toISOString();
  await dbPut(STORE, computer.cid, {
    cid: computer.cid,
    snapshot: json,
    computer: { online: state.online, lastSeen: state.lastSeen },
    fetchedAt,
  });
  return { snapshot, computer: state, fetchedAt, stale: false };
}

// "offline 12m", "offline 3h", "offline 2d": how long the computer has been
// away, from the relay's last_seen.
export function offlineLabel(lastSeen: string | null, nowMs: number = Date.now()): string {
  if (lastSeen === null) return "offline";
  const t = Date.parse(lastSeen);
  if (Number.isNaN(t)) return "offline";
  const mins = Math.max(0, Math.floor((nowMs - t) / 60_000));
  if (mins < 1) return "offline just now";
  if (mins < 60) return `offline ${mins}m`;
  const hours = Math.floor(mins / 60);
  if (hours < 48) return `offline ${hours}h`;
  return `offline ${Math.floor(hours / 24)}d`;
}
