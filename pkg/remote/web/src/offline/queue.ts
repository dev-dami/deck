// Queueing work for a computer that is away (docs/mobile-contract.md §6): build
// the action, seal it so only that computer can read it (Noise K), and leave it
// in the relay's mailbox, or in the local outbox when the phone itself is
// offline. Nothing here needs the computer to be awake.
import { b64Encode, b64urlDecode } from "../b64";
import { seal } from "../e2e/seal";
import {
  type Assertion,
  type Precond,
  QUEUEABLE_KINDS,
  type QueueableKind,
  type QueuedAction,
} from "../protocol3";
import { prologueFor } from "../session3";
import type { ComputerRecord } from "../store/computers";
import { type OutboxEntry, outboxAdd, outboxList, outboxRemove } from "../store/outbox";
import { recordSubmitted } from "../store/submitted";
import { uuidv7 } from "./uuid";

const HOUR_MS = 3_600_000;

// What the phone offers (contract §6): 1, 8 or 24 hours; 8 by default.
export const EXPIRY_CHOICES_MS = [1 * HOUR_MS, 8 * HOUR_MS, 24 * HOUR_MS] as const;
export const DEFAULT_EXPIRY_MS = 8 * HOUR_MS;
export const MAX_EXPIRY_MS = 24 * HOUR_MS;
export const MAX_QUEUED_PER_DEVICE = 20;

// Actions that must never wait for a sleeping computer: there is nothing to
// stop after wake, and open-ended authority must not fire unattended.
export const NEVER_QUEUED: readonly string[] = ["agent.interrupt", "chat.cancel", "mode.set"];

export type QueueErrorCode =
  | "not_paired" // no keys or token for this computer
  | "not_queueable"
  | "invalid"
  | "too_many" // 20 already waiting
  | "revoked" // the relay no longer knows this phone
  | "too_large"
  | "rejected"; // the relay refused it for another reason

export class QueueError extends Error {
  constructor(
    readonly code: QueueErrorCode,
    message: string,
  ) {
    super(message);
    this.name = "QueueError";
  }
}

export function isQueueableKind(kind: string): kind is QueueableKind {
  return (QUEUEABLE_KINDS as readonly string[]).includes(kind);
}

export interface QueueInput {
  kind: QueueableKind;
  pj?: string;
  sessionId?: string;
  payload?: unknown;
  // A queued approval answers exactly the request the person saw.
  precond?: Precond;
  // A destructive allow carries a passkey assertion over the qid and digest.
  assertion?: Assertion;
  expiresInMs?: number;
}

export interface QueueDeps {
  fetch?: typeof fetch;
  now?: () => number;
}

export type QueueState = "sent" | "outbox";

export interface QueueResult {
  qid: string;
  state: QueueState;
  expiresAt: string;
}

// The same rules as the computer's QueuedAction.Validate, so a bad action is
// caught on the phone and not left to fail after the wake.
export function validateAction(a: QueuedAction): string | null {
  if (a.qid === "" || a.device_id === "") return "missing qid or device";
  const created = Date.parse(a.created_at);
  const expires = Date.parse(a.expires_at);
  if (Number.isNaN(created) || Number.isNaN(expires) || expires <= created) {
    return "expiry out of range";
  }
  if (expires - created > MAX_EXPIRY_MS) return "expiry out of range";
  switch (a.kind) {
    case "chat.send":
    case "chat.continue":
    case "models.set":
      return a.pj && a.session_id ? null : "missing session";
    case "session.new":
      return a.pj ? null : "missing project";
    case "approvals.answer":
      return a.pj && a.session_id && a.precond?.approval_id && a.precond.digest
        ? null
        : "an approval needs its approval id and digest";
    case "queue.cancel":
      return null;
  }
}

interface PairedComputer {
  cid: string;
  relayUrl: string;
  relayToken: string;
  deviceId: string;
  computerStatic: Uint8Array;
  phoneKey: NonNullable<ComputerRecord["phoneKey"]>;
}

function paired(c: ComputerRecord): PairedComputer {
  const statik = c.computerStatic !== undefined ? b64urlDecode(c.computerStatic) : null;
  if (
    c.phoneKey === undefined ||
    statik === null ||
    c.relayToken === null ||
    c.deviceId === null ||
    c.deviceId === ""
  ) {
    throw new QueueError("not_paired", "This computer is not paired on this phone any more.");
  }
  return {
    cid: c.cid,
    relayUrl: c.relayUrl,
    relayToken: c.relayToken,
    deviceId: c.deviceId,
    computerStatic: statik,
    phoneKey: c.phoneKey,
  };
}

// Seals an action for the computer and tries to leave it in the mailbox.
export async function queueAction(
  computer: ComputerRecord,
  input: QueueInput,
  deps: QueueDeps = {},
): Promise<QueueResult> {
  if (!isQueueableKind(input.kind)) {
    throw new QueueError("not_queueable", "That cannot wait for the computer to wake.");
  }
  const c = paired(computer);
  const now = (deps.now ?? Date.now)();
  const expiresIn = Math.min(input.expiresInMs ?? DEFAULT_EXPIRY_MS, MAX_EXPIRY_MS);

  if ((await outboxList(c.cid)).length >= MAX_QUEUED_PER_DEVICE) {
    throw new QueueError(
      "too_many",
      "Too many actions are waiting to be sent. Wait for some to go.",
    );
  }

  const action: QueuedAction = {
    qid: uuidv7(now),
    device_id: c.deviceId,
    created_at: new Date(now).toISOString(),
    expires_at: new Date(now + expiresIn).toISOString(),
    kind: input.kind,
    payload: input.payload ?? {},
    ...(input.pj !== undefined ? { pj: input.pj } : {}),
    ...(input.sessionId !== undefined ? { session_id: input.sessionId } : {}),
    ...(input.precond !== undefined ? { precond: input.precond } : {}),
    ...(input.assertion !== undefined ? { assertion: input.assertion } : {}),
  };
  const bad = validateAction(action);
  if (bad !== null) throw new QueueError("invalid", bad);

  const sealed = await seal({
    senderStatic: c.phoneKey,
    recipientStaticPub: c.computerStatic,
    prologue: prologueFor(c.cid),
    plaintext: new TextEncoder().encode(JSON.stringify(action)),
  });
  const entry: OutboxEntry = {
    qid: action.qid,
    cid: c.cid,
    id: action.qid,
    expires_at: action.expires_at,
    b64: b64Encode(sealed),
    createdAt: action.created_at,
    attempts: 0,
    kind: action.kind,
  };

  const outcome = await postMail(c, entry, deps);
  if (outcome === "sent") {
    await rememberSubmission(entry);
    return { qid: action.qid, state: "sent", expiresAt: action.expires_at };
  }
  await outboxAdd({ ...entry, attempts: 1 });
  return { qid: action.qid, state: "outbox", expiresAt: action.expires_at };
}

type PostOutcome = "sent" | "retry";

async function rememberSubmission(entry: OutboxEntry): Promise<void> {
  await recordSubmitted({
    qid: entry.qid,
    cid: entry.cid,
    kind: entry.kind ?? "unknown",
    created_at: entry.createdAt,
    expires_at: entry.expires_at,
  });
}

// POST /r/v1/c/:cid/mail. A network failure, a rate limit or a server error is
// worth retrying; anything else is the relay saying no, and says why.
async function postMail(c: PairedComputer, e: OutboxEntry, deps: QueueDeps): Promise<PostOutcome> {
  const doFetch = deps.fetch ?? fetch;
  let res: Response;
  try {
    res = await doFetch(
      `${c.relayUrl.replace(/\/+$/, "")}/r/v1/c/${encodeURIComponent(c.cid)}/mail`,
      {
        method: "POST",
        headers: { "content-type": "application/json", authorization: `Bearer ${c.relayToken}` },
        body: JSON.stringify({ id: e.id, expires_at: e.expires_at, b64: e.b64 }),
      },
    );
  } catch {
    return "retry";
  }
  if (res.ok) return "sent";
  if (res.status === 429 || res.status >= 500) return "retry";
  if (res.status === 401) {
    throw new QueueError("revoked", "This phone is no longer paired with the computer.");
  }
  if (res.status === 413) {
    throw new QueueError("too_large", "The computer's mailbox is full. Try again after it wakes.");
  }
  if (res.status === 400) throw new QueueError("invalid", "The relay refused that action.");
  throw new QueueError("rejected", `The relay refused that action (${res.status}).`);
}

export interface FlushResult {
  sent: string[];
  // Expired before they could be sent, or refused for good.
  dropped: string[];
  remaining: number;
}

// Sends what is waiting in the outbox, oldest first. Run it when the phone
// comes back online.
export async function flushOutbox(
  computer: ComputerRecord,
  deps: QueueDeps = {},
): Promise<FlushResult> {
  const c = paired(computer);
  const now = (deps.now ?? Date.now)();
  const out: FlushResult = { sent: [], dropped: [], remaining: 0 };
  for (const e of await outboxList(c.cid)) {
    if (Date.parse(e.expires_at) <= now) {
      await outboxRemove(e.qid);
      out.dropped.push(e.qid);
      continue;
    }
    let outcome: PostOutcome;
    try {
      outcome = await postMail(c, e, deps);
    } catch (err) {
      if (err instanceof QueueError && err.code === "revoked") throw err;
      await outboxRemove(e.qid);
      out.dropped.push(e.qid);
      continue;
    }
    if (outcome === "sent") {
      await rememberSubmission(e);
      await outboxRemove(e.qid);
      out.sent.push(e.qid);
    } else {
      await outboxAdd({ ...e, attempts: e.attempts + 1 });
      out.remaining += 1;
    }
  }
  return out;
}
