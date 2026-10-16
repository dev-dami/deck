import type { JSX } from "preact";
import { useRef, useState } from "preact/hooks";
import { type QueueInput, type QueueResult, queueAction } from "../offline/queue";
import type { PendingApproval, Snapshot } from "../protocol3";
import type { ComputerRecord } from "../store/computers";

export interface OfflineApprovalsProps {
  computer: ComputerRecord;
  snapshot: Snapshot;
}

export type OfflineApprovalDecision = "allow" | "deny";
export type ApprovalExpiryHours = 1 | 8 | 24;
type Enqueue = (computer: ComputerRecord, input: QueueInput) => Promise<QueueResult>;

// Keep the policy check at the action boundary as well as in the buttons.
// An offline destructive allow needs an assertion over the eventual qid;
// the current queue API generates that id internally, so it cannot be signed.
export async function queueSnapshotApproval(
  computer: ComputerRecord,
  pending: PendingApproval,
  decision: OfflineApprovalDecision,
  reason: string,
  expiryHours: ApprovalExpiryHours,
  enqueue: Enqueue = queueAction,
): Promise<QueueResult> {
  if (decision !== "allow" && decision !== "deny") throw new Error("Invalid approval decision.");
  if (decision === "allow" && pending.approval.tier !== "normal") {
    throw new Error("Reconnect to verify a destructive approval with your passkey.");
  }
  if (![1, 8, 24].includes(expiryHours)) throw new Error("Choose an expiry of 1, 8 or 24 hours.");
  if (
    !pending.pj ||
    !pending.approval.id ||
    !pending.approval.session_id ||
    !pending.approval.digest
  ) {
    throw new Error("This saved approval is incomplete. Reconnect before answering.");
  }
  return enqueue(computer, {
    kind: "approvals.answer",
    pj: pending.pj,
    sessionId: pending.approval.session_id,
    payload: {
      decision,
      ...(decision === "deny" && reason.trim() ? { reason: reason.trim() } : {}),
    },
    precond: { approval_id: pending.approval.id, digest: pending.approval.digest },
    expiresInMs: expiryHours * 3_600_000,
  });
}

export function OfflineApprovals({ computer, snapshot }: OfflineApprovalsProps): JSX.Element {
  return (
    <section aria-label="Saved approvals">
      <h2>Saved approvals</h2>
      <p>
        Saved {new Date(snapshot.at).toLocaleString()}. A queued answer applies only if the same
        approval is still waiting when the computer reconnects.
      </p>
      {snapshot.approvals.length === 0 ? <p>No approvals in this snapshot.</p> : null}
      {snapshot.approvals.map((pending) => (
        <OfflineApprovalCard
          key={`${computer.deviceId}:${pending.pj}:${pending.approval.id}:${pending.approval.digest}`}
          computer={computer}
          pending={pending}
        />
      ))}
    </section>
  );
}

function OfflineApprovalCard({
  computer,
  pending,
}: {
  computer: ComputerRecord;
  pending: PendingApproval;
}): JSX.Element {
  const { approval } = pending;
  const [reason, setReason] = useState("");
  const [expiry, setExpiry] = useState<ApprovalExpiryHours>(8);
  const [busy, setBusy] = useState(false);
  const [result, setResult] = useState<QueueResult | null>(null);
  const [error, setError] = useState<string | null>(null);
  const lock = useRef(false);
  const disabled = busy || result !== null;
  const answer = async (decision: OfflineApprovalDecision): Promise<void> => {
    if (lock.current || result !== null) return;
    lock.current = true;
    setBusy(true);
    setError(null);
    try {
      setResult(await queueSnapshotApproval(computer, pending, decision, reason, expiry));
    } catch (err) {
      setError(err instanceof Error ? err.message : "Could not queue this answer.");
    } finally {
      lock.current = false;
      setBusy(false);
    }
  };
  return (
    <article>
      <h3>{approval.description || "Approval requested"}</h3>
      <p>
        {approval.agent} · {approval.tier === "normal" ? "Normal" : "Destructive"}
      </p>
      {approval.path ? <p>{approval.path}</p> : null}
      {approval.command ? <pre>{approval.command}</pre> : null}
      {approval.command_truncated ? <p>The command was truncated.</p> : null}
      {approval.diff ? (
        <details>
          <summary>Saved change</summary>
          <pre>{approval.diff}</pre>
          {approval.diff_truncated ? <p>The diff was truncated.</p> : null}
        </details>
      ) : null}
      <label>
        Answer expires after
        <select
          value={expiry}
          disabled={disabled}
          onChange={(event) => {
            const hours = Number(event.currentTarget.value);
            if (hours === 1 || hours === 8 || hours === 24) setExpiry(hours);
          }}
        >
          <option value={1}>1 hour</option>
          <option value={8}>8 hours</option>
          <option value={24}>24 hours</option>
        </select>
      </label>
      <label>
        Reason for denying (optional)
        <textarea
          value={reason}
          disabled={disabled}
          maxLength={2000}
          onInput={(event) => setReason(event.currentTarget.value)}
        />
      </label>
      <div>
        <button type="button" disabled={disabled} onClick={() => void answer("deny")}>
          Queue denial
        </button>
        {approval.tier === "normal" ? (
          <button type="button" disabled={disabled} onClick={() => void answer("allow")}>
            Queue approval
          </button>
        ) : (
          <p>Reconnect to approve this action with your passkey. You can queue a denial now.</p>
        )}
      </div>
      {result !== null ? (
        <p role="status">
          {result.state === "outbox"
            ? "Saved on this phone"
            : "Delivered to the computer's mailbox"}
          {" · "}
          {result.qid}. Waiting for a receipt; this action has not been confirmed.
        </p>
      ) : null}
      {error !== null ? <p role="alert">{error}</p> : null}
    </article>
  );
}
