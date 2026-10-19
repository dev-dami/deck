import { QUEUEABLE_KINDS, type QueueableKind } from "../protocol3";
import { dbDelete, dbGetAll, dbPut } from "./db";

// A sealed action the phone could not hand to the relay yet (it was offline
// itself). It is the exact mail body: nothing here is readable without the
// computer's key.
export interface OutboxEntry {
  qid: string;
  cid: string;
  // Mail body for POST /r/v1/c/:cid/mail.
  id: string;
  expires_at: string;
  b64: string;
  createdAt: string;
  attempts: number;
  // Optional for records written before delivery history was introduced.
  kind?: QueueableKind;
}

const STORE = "outbox";

function isEntry(v: Record<string, unknown>): v is Record<string, unknown> & OutboxEntry {
  return (
    typeof v["qid"] === "string" &&
    typeof v["cid"] === "string" &&
    typeof v["id"] === "string" &&
    typeof v["expires_at"] === "string" &&
    typeof v["b64"] === "string" &&
    typeof v["createdAt"] === "string" &&
    typeof v["attempts"] === "number"
  );
}

export async function outboxList(cid?: string): Promise<OutboxEntry[]> {
  const rows = await dbGetAll(STORE);
  return rows
    .filter(isEntry)
    .filter((r) => cid === undefined || r.cid === cid)
    .map((r) => ({
      qid: r.qid,
      cid: r.cid,
      id: r.id,
      expires_at: r.expires_at,
      b64: r.b64,
      createdAt: r.createdAt,
      attempts: r.attempts,
      ...(typeof r["kind"] === "string" &&
      (QUEUEABLE_KINDS as readonly string[]).includes(r["kind"])
        ? { kind: r["kind"] as QueueableKind }
        : {}),
    }))
    .sort((a, b) => (a.createdAt < b.createdAt ? -1 : a.createdAt > b.createdAt ? 1 : 0));
}

export async function outboxAdd(e: OutboxEntry): Promise<void> {
  await dbPut(STORE, e.qid, { ...e });
}

export async function outboxRemove(qid: string): Promise<void> {
  await dbDelete(STORE, qid);
}
