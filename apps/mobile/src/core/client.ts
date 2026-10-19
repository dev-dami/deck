import { ChatSession } from "../../../../pkg/remote/web/src/chat3";
import { importStaticKey } from "../../../../pkg/remote/web/src/e2e/noise";
import { type PairOptions, pairWithComputer } from "../../../../pkg/remote/web/src/hosted/pairing";
import {
  flushOutbox,
  type QueueInput,
  queueAction,
} from "../../../../pkg/remote/web/src/offline/queue";
import { loadSnapshot, savedSnapshot } from "../../../../pkg/remote/web/src/offline/snapshot";
import {
  type ApprovalV3,
  decodeApprovalV3,
  decodeReceipt,
  type Receipt,
} from "../../../../pkg/remote/web/src/protocol3";
import {
  type ComputerView,
  FleetState,
  type SessionLike,
  type SessionOpener,
} from "../../../../pkg/remote/web/src/state3";
import {
  forgetOnRevoke,
  listComputers,
  removeComputer,
} from "../../../../pkg/remote/web/src/store/computers";
import { setBackend } from "../../../../pkg/remote/web/src/store/db";
import { outboxList, outboxRemove } from "../../../../pkg/remote/web/src/store/outbox";
import { listSubmitted } from "../../../../pkg/remote/web/src/store/submitted";
import { verifyNativeCrypto } from "../crypto/verify";
import type { NativeStoreBackend } from "../storage/backend";
import { openNativeStorage } from "../storage/native";
import { parseNativePairInput } from "./pair-input";

export type { QueueInput, QueueResult } from "../../../../pkg/remote/web/src/offline/queue";
export type { LoadedSnapshot } from "../../../../pkg/remote/web/src/offline/snapshot";
export type { ApprovalV3, Receipt, Snapshot } from "../../../../pkg/remote/web/src/protocol3";
export type { ComputerView, SessionLike } from "../../../../pkg/remote/web/src/state3";
export type { OutboxEntry } from "../../../../pkg/remote/web/src/store/outbox";
export type { SubmittedAction } from "../../../../pkg/remote/web/src/store/submitted";

let booting: Promise<NativeClient> | null = null;

export type CancelResult =
  | { state: "removed"; qid: string }
  | { state: "requested"; qid: string; cancellationQid: string; delivery: "sent" | "outbox" };

export class NativeClient {
  readonly fleet: FleetState;
  lastError: string | null = null;
  private active = false;
  private listeners = new Set<() => void>();
  private computerWork = new Map<string, Promise<void>>();

  // An injected opener permits real protocol tests without a running relay.
  // Production initialization always goes through bootstrap's crypto checks.
  constructor(
    private readonly backend: NativeStoreBackend,
    opener?: SessionOpener,
    private readonly pairTransport?: PairOptions["openPair"],
  ) {
    setBackend(backend);
    this.fleet = new FleetState(opener, { list: listComputers, forget: forgetOnRevoke });
    this.fleet.subscribe(() => this.emit());
  }

  static bootstrap(): Promise<NativeClient> {
    if (booting !== null) return booting;
    booting = (async () => {
      await verifyNativeCrypto();
      if (typeof globalThis.CryptoKey === "undefined") {
        throw new Error(
          "The native key provider is unavailable. Rebuild the Deck development client.",
        );
      }
      const client = new NativeClient(await openNativeStorage());
      await client.reload();
      return client;
    })().catch((error: unknown) => {
      booting = null;
      throw error;
    });
    return booting;
  }

  private emit(): void {
    for (const listener of this.listeners) listener();
  }

  private serialized<T>(cid: string, work: () => Promise<T>): Promise<T> {
    const previous = this.computerWork.get(cid) ?? Promise.resolve();
    const result = previous.then(work);
    const tail = result.then(
      () => undefined,
      () => undefined,
    );
    this.computerWork.set(cid, tail);
    void tail.then(() => {
      if (this.computerWork.get(cid) === tail) this.computerWork.delete(cid);
    });
    return result;
  }

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  views(): ComputerView[] {
    return this.fleet.views();
  }

  get(cid: string): ComputerView | null {
    return this.fleet.get(cid);
  }

  session(cid: string): SessionLike | null {
    return this.fleet.session(cid);
  }

  async reload(): Promise<void> {
    await this.fleet.reload();
    this.lastError = null;
    if (this.active) this.fleet.startReconnect();
  }

  start(): void {
    this.active = true;
    this.fleet.startReconnect();
    for (const computer of this.views()) {
      if (computer.record.relayToken === null) continue;
      void this.flush(computer.record.cid).catch(() => {
        this.lastError = "Some waiting actions could not be delivered. Open the queue to retry.";
        this.emit();
      });
    }
  }

  stop(): void {
    this.active = false;
    this.fleet.stopReconnect();
    for (const computer of this.views()) this.fleet.disconnect(computer.record.cid);
  }

  connect(cid: string): Promise<void> {
    return this.fleet.connect(cid);
  }

  async pair(urlOrQr: string, deviceName: string) {
    const blob = parseNativePairInput(urlOrQr);
    const name = deviceName.trim();
    if (!name || Array.from(name).length > 80)
      throw new Error("Choose a phone name of 1 to 80 characters.");
    const raw = crypto.getRandomValues(new Uint8Array(32));
    try {
      const phoneKey = await importStaticKey(raw);
      const record = await pairWithComputer({
        blob,
        deviceName: name,
        phoneKey,
        save: (paired) => this.backend.savePaired(paired, raw),
        ...(this.pairTransport === undefined ? {} : { openPair: this.pairTransport }),
      });
      await this.reload();
      if (this.active) await this.connect(record.cid);
      return record;
    } finally {
      raw.fill(0);
    }
  }

  private record(cid: string) {
    const computer = this.get(cid);
    if (computer === null) throw new Error("This computer is not saved on the phone.");
    return computer.record;
  }

  async queue(cid: string, input: QueueInput) {
    return this.serialized(cid, async () => {
      const result = await queueAction(this.record(cid), input);
      this.emit();
      return result;
    });
  }

  async flush(cid: string) {
    return this.serialized(cid, async () => {
      const result = await flushOutbox(this.record(cid));
      this.emit();
      return result;
    });
  }

  async cancel(cid: string, qid: string): Promise<CancelResult> {
    if (!qid.trim()) throw new Error("Choose an action to cancel.");
    return this.serialized(cid, async () => {
      const waiting = (await outboxList(cid)).some((entry) => entry.qid === qid);
      if (waiting) {
        await outboxRemove(qid);
        this.emit();
        return { state: "removed", qid };
      }
      // A prior flush may have delivered the row while the screen still
      // showed it as unsent. Once this lock is acquired, it is safe to choose
      // between deleting local mail and sending a computer-side cancellation.
      const cancellation = await queueAction(this.record(cid), {
        kind: "queue.cancel",
        payload: { qid },
      });
      this.emit();
      return {
        state: "requested",
        qid,
        cancellationQid: cancellation.qid,
        delivery: cancellation.state,
      };
    });
  }

  outbox(cid: string) {
    return outboxList(cid);
  }

  history(cid: string) {
    return listSubmitted(cid);
  }

  async removeOutbox(qid: string): Promise<void> {
    const entry = (await outboxList()).find((row) => row.qid === qid);
    if (entry !== undefined) await this.cancel(entry.cid, qid);
  }

  loadSnapshot(cid: string) {
    return loadSnapshot(this.record(cid));
  }

  savedSnapshot(cid: string) {
    return savedSnapshot(cid);
  }

  ack(cid: string, pj: string, sessionId: string): Promise<void> {
    return this.fleet.ack(cid, pj, sessionId);
  }

  async chat(cid: string, pj: string, sessionId: string): Promise<ChatSession> {
    const session = this.session(cid);
    if (session === null) throw new Error("Reconnect to read this conversation.");
    return ChatSession.open(session, pj, sessionId);
  }

  async approvals(cid: string, pj: string, sessionId: string): Promise<ApprovalV3[]> {
    const session = this.session(cid);
    if (session === null) throw new Error("Reconnect to load current approvals.");
    const result = await session.call("approvals.list", { session_id: sessionId }, pj);
    if (!Array.isArray(result)) throw new Error("The computer sent unreadable approvals.");
    return result.map((item) => {
      const approval = decodeApprovalV3(item);
      if (approval === null || approval.session_id !== sessionId)
        throw new Error("The computer sent unreadable approvals.");
      return approval;
    });
  }

  async answer(
    cid: string,
    pj: string,
    approval: ApprovalV3,
    decision: "allow" | "allow_session" | "deny",
    reason = "",
  ): Promise<void> {
    const session = this.session(cid);
    if (session === null) throw new Error("Reconnect or queue this approval answer.");
    if (decision !== "deny" && approval.tier !== "normal") {
      throw new Error(
        "This action needs a passkey. Approve it in the hosted app or on your computer.",
      );
    }
    await session.call(
      "approvals.answer",
      {
        session_id: approval.session_id,
        id: approval.id,
        decision,
        ...(decision === "deny" && reason.trim() ? { reason: reason.trim() } : {}),
      },
      pj,
    );
  }

  async receipts(cid: string): Promise<Receipt[]> {
    const session = this.session(cid);
    if (session === null) return (await this.savedSnapshot(cid))?.snapshot.receipts ?? [];
    const result = await session.call("queue.status");
    if (!Array.isArray(result)) throw new Error("The computer sent unreadable receipts.");
    return result.map((item) => {
      const receipt = decodeReceipt(item);
      if (receipt === null) throw new Error("The computer sent unreadable receipts.");
      return receipt;
    });
  }

  async forget(cid: string): Promise<void> {
    this.fleet.disconnect(cid);
    await this.backend.del("snapshots", cid);
    await removeComputer(cid);
    for (const store of ["outbox", "submitted"]) {
      for (const row of await this.backend.getAll(store)) {
        if (row["cid"] === cid) {
          const key = row["qid"];
          if (typeof key === "string") await this.backend.del(store, key);
        }
      }
    }
    await this.reload();
  }
}
