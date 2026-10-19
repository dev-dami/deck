import { expect, test } from "bun:test";
import type { QueueInput } from "../offline/queue";
import type { PendingApproval } from "../protocol3";
import type { ComputerRecord } from "../store/computers";
import { type ApprovalExpiryHours, queueSnapshotApproval } from "./OfflineApprovals";

const computer: ComputerRecord = {
  cid: "pc",
  name: "Office",
  relayUrl: "https://app.test",
  lan: [],
  deviceId: "dev_1",
  relayToken: "token",
  createdAt: "2026-10-15T00:00:00Z",
};
const pending: PendingApproval = {
  pj: "project-1",
  approval: {
    id: "approval-1",
    session_id: "session-1",
    agent: "claude",
    kind: "command",
    description: "Read source",
    created_at: "2026-10-15T00:00:00Z",
    tier: "normal",
    digest: "bound-digest",
  },
};

function capture() {
  const calls: Array<{ record: ComputerRecord; input: QueueInput }> = [];
  return {
    calls,
    enqueue: async (record: ComputerRecord, input: QueueInput) => {
      calls.push({ record, input });
      return { qid: "qid-1", state: "outbox" as const, expiresAt: "2026-10-16T00:00:00Z" };
    },
  };
}

test("normal approval binds the saved id, digest, project and session", async () => {
  const f = capture();
  const result = await queueSnapshotApproval(computer, pending, "allow", "", 8, f.enqueue);
  expect(result).toMatchObject({ qid: "qid-1", state: "outbox" });
  expect(f.calls).toEqual([
    {
      record: computer,
      input: {
        kind: "approvals.answer",
        pj: "project-1",
        sessionId: "session-1",
        payload: { decision: "allow" },
        precond: { approval_id: "approval-1", digest: "bound-digest" },
        expiresInMs: 8 * 3_600_000,
      },
    },
  ]);
});

test("destructive approval cannot be allowed offline", async () => {
  const f = capture();
  await expect(
    queueSnapshotApproval(
      computer,
      { ...pending, approval: { ...pending.approval, tier: "destructive" } },
      "allow",
      "",
      1,
      f.enqueue,
    ),
  ).rejects.toThrow("Reconnect");
  expect(f.calls).toEqual([]);
});

test("destructive denial queues its reason with the same precondition", async () => {
  const f = capture();
  await queueSnapshotApproval(
    computer,
    { ...pending, approval: { ...pending.approval, tier: "destructive" } },
    "deny",
    "  Keep the files.  ",
    24,
    f.enqueue,
  );
  expect(f.calls[0]?.input.payload).toEqual({ decision: "deny", reason: "Keep the files." });
  expect(f.calls[0]?.input.precond).toEqual({ approval_id: "approval-1", digest: "bound-digest" });
});

for (const hours of [1, 8, 24] as const) {
  test(`${hours} hour expiry is sent explicitly`, async () => {
    const f = capture();
    await queueSnapshotApproval(computer, pending, "deny", "", hours, f.enqueue);
    expect(f.calls[0]?.input.expiresInMs).toBe(hours * 3_600_000);
  });
}

test("invalid expiry and missing binding never enqueue", async () => {
  const f = capture();
  await expect(
    queueSnapshotApproval(computer, pending, "allow", "", 2 as ApprovalExpiryHours, f.enqueue),
  ).rejects.toThrow("expiry");
  await expect(
    queueSnapshotApproval(
      computer,
      { ...pending, approval: { ...pending.approval, digest: "" } },
      "deny",
      "",
      8,
      f.enqueue,
    ),
  ).rejects.toThrow("incomplete");
  expect(f.calls).toEqual([]);
});

test("delivery failure propagates without pretending an answer was queued", async () => {
  await expect(
    queueSnapshotApproval(computer, pending, "deny", "", 8, async () => {
      throw new Error("mailbox full");
    }),
  ).rejects.toThrow("mailbox full");
});
