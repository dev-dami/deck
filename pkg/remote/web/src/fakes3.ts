import type { ComputerRecord } from "./store/computers";

// TS fake computer for hosted v3 PWA tests (plan §8 B12). It speaks no crypto
// yet: A0 will freeze the Noise vectors and v3 fixtures, and this fake will
// grow to terminate them. Until then it drives the B5–B10 UI flows (pairing,
// Computers list, inbox, queue) with plain JSON over an in-memory transport,
// so hosted tests never need Go.

export interface FakeInboxItem {
  pj: string;
  session_id: string;
  title: string;
  state: "needs_you" | "running" | "done";
  approvals: number;
  last_activity: string;
  summary: string;
  unread: boolean;
}

export interface FakeQueuedAction {
  qid: string;
  kind: string;
  pj: string;
  session_id: string | null;
  payload: Record<string, unknown>;
  expires_at: string;
}

export class FakeComputer3 {
  readonly computer: ComputerRecord;
  inbox: FakeInboxItem[] = [];
  queue: FakeQueuedAction[] = [];
  online = true;
  // Queued approvals are bound to id+digest (§4); the fake enforces the same
  // shape so UI tests cover stale-drop behavior early.
  private approvals = new Map<string, string>();

  constructor(computer: ComputerRecord) {
    this.computer = computer;
  }

  setInbox(items: FakeInboxItem[]): void {
    this.inbox = [...items];
  }

  needsYou(): FakeInboxItem[] {
    return this.inbox.filter((i) => i.state === "needs_you");
  }

  advertiseApproval(id: string, digest: string): void {
    this.approvals.set(id, digest);
  }

  withdrawApproval(id: string): void {
    this.approvals.delete(id);
  }

  // Returns applied when the approval id is still pending with the same
  // digest, dropped otherwise — the same rule the Go wake replay uses.
  answerApproval(id: string, digest: string): "applied" | "dropped" {
    return this.approvals.get(id) === digest ? "applied" : "dropped";
  }

  enqueue(a: FakeQueuedAction): void {
    if (this.queue.length >= 20) this.queue.shift();
    this.queue.push(a);
  }

  cancelQueued(qid: string): boolean {
    const before = this.queue.length;
    this.queue = this.queue.filter((q) => q.qid !== qid);
    return this.queue.length !== before;
  }

  snapshot(): { online: boolean; inbox: FakeInboxItem[]; receipts: number } {
    return { online: this.online, inbox: [...this.inbox], receipts: this.queue.length };
  }
}

export function fakeComputerRecord(over: Partial<ComputerRecord> = {}): ComputerRecord {
  return {
    cid: "cid_test_123456789012",
    name: "Test PC",
    relayUrl: "https://relay.example",
    lan: ["192.168.1.10:7879"],
    deviceId: "dev_1",
    relayToken: "token_opaque",
    online: true,
    lastSeen: null,
    createdAt: "2026-10-14T00:00:00Z",
    ...over,
  };
}
