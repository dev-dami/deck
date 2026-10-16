import type { QueueInput } from "../../../../pkg/remote/web/src/offline/queue";
import type { PendingApproval } from "../../../../pkg/remote/web/src/protocol3";

export function offlineApprovalInput(
  pending: PendingApproval,
  decision: "allow" | "deny",
  reason = "",
): QueueInput {
  const { approval, pj } = pending;
  if (!pj || !approval.id || !approval.session_id || !approval.digest)
    throw new Error("This saved approval is incomplete. Reconnect to review it.");
  if (decision === "allow" && approval.tier !== "normal")
    throw new Error(
      "Destructive actions require a live passkey verification. Reconnect to review this action.",
    );
  return {
    kind: "approvals.answer",
    pj,
    sessionId: approval.session_id,
    payload: {
      session_id: approval.session_id,
      id: approval.id,
      decision,
      ...(decision === "deny" && reason.trim() ? { reason: reason.trim() } : {}),
    },
    precond: { approval_id: approval.id, digest: approval.digest },
    expiresInMs: 3_600_000,
  };
}
