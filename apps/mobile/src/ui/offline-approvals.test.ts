import { expect, test } from "bun:test";
import type { PendingApproval } from "../../../../pkg/remote/web/src/protocol3";
import { offlineApprovalInput } from "./offline-approvals";

const pending: PendingApproval = {
  pj: "project",
  approval: {
    id: "approval",
    session_id: "session",
    agent: "agent",
    kind: "command",
    description: "Run tests",
    created_at: "2026-10-15T00:00:00Z",
    tier: "normal",
    digest: "digest",
  },
};
test("saved approval answers bind project, session, ID and exact digest", () => {
  expect(offlineApprovalInput(pending, "allow")).toEqual({
    kind: "approvals.answer",
    pj: "project",
    sessionId: "session",
    payload: { session_id: "session", id: "approval", decision: "allow" },
    precond: { approval_id: "approval", digest: "digest" },
    expiresInMs: 3_600_000,
  });
});
test("destructive allow cannot be queued from a saved approval", () => {
  const destructive: PendingApproval = {
    ...pending,
    approval: { ...pending.approval, tier: "destructive" },
  };
  expect(() => offlineApprovalInput(destructive, "allow")).toThrow("passkey");
  expect(offlineApprovalInput(destructive, "deny", "  Changed my mind  ").payload).toEqual({
    session_id: "session",
    id: "approval",
    decision: "deny",
    reason: "Changed my mind",
  });
});
test("incomplete snapshot approval cannot produce a queued answer", () => {
  expect(() =>
    offlineApprovalInput({ ...pending, approval: { ...pending.approval, digest: "" } }, "allow"),
  ).toThrow("incomplete");
});
