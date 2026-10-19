import { QUEUEABLE_KINDS, type QueueableKind } from "../protocol3";
import { dbDelete, dbGetAll, dbPut } from "./db";

// Local delivery history, not proof that the computer executed an action.
// Receipts supply that outcome. Never persist payloads, secrets or assertions.
export interface SubmittedAction {
  qid: string;
  cid: string;
  kind: QueueableKind | "unknown";
  created_at: string;
  expires_at: string;
}

const STORE = "submitted";
export const MAX_SUBMITTED_PER_COMPUTER = 200;

function decode(row: Record<string, unknown>): SubmittedAction | null {
  const { qid, cid, kind, created_at, expires_at } = row;
  if (
    typeof qid !== "string" ||
    typeof cid !== "string" ||
    typeof kind !== "string" ||
    typeof created_at !== "string" ||
    typeof expires_at !== "string" ||
    !Number.isFinite(Date.parse(created_at)) ||
    !Number.isFinite(Date.parse(expires_at)) ||
    (kind !== "unknown" && !(QUEUEABLE_KINDS as readonly string[]).includes(kind))
  )
    return null;
  return { qid, cid, kind: kind as SubmittedAction["kind"], created_at, expires_at };
}

export async function listSubmitted(cid: string): Promise<SubmittedAction[]> {
  return (await dbGetAll(STORE))
    .map(decode)
    .filter((row): row is SubmittedAction => row !== null && row.cid === cid)
    .sort(
      (a, b) => Date.parse(b.created_at) - Date.parse(a.created_at) || b.qid.localeCompare(a.qid),
    );
}

export async function recordSubmitted(action: SubmittedAction): Promise<void> {
  // Pick fields explicitly so callers cannot accidentally persist plaintext.
  const safe = decode({
    qid: action.qid,
    cid: action.cid,
    kind: action.kind,
    created_at: action.created_at,
    expires_at: action.expires_at,
  });
  if (safe === null) throw new Error("Invalid submitted action metadata.");
  await dbPut(STORE, safe.qid, { ...safe });
  for (const old of (await listSubmitted(safe.cid)).slice(MAX_SUBMITTED_PER_COMPUTER)) {
    await dbDelete(STORE, old.qid);
  }
}

export async function removeSubmitted(qid: string): Promise<void> {
  await dbDelete(STORE, qid);
}
