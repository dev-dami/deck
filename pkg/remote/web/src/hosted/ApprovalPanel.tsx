import type * as preact from "preact";
import { useEffect, useState } from "preact/hooks";
import {
  answerApproval,
  enableYolo,
  listPasskeys,
  registerPasskey,
  removePasskey,
} from "../passkey/client";
import {
  type AnswerDecision,
  type ApprovalV3,
  decodeApprovalV3,
  type PasskeyInfo,
} from "../protocol3";
import type { SessionLike } from "../state3";

export interface ApprovalPanelProps {
  session: SessionLike;
  pj: string;
  sessionId: string;
}

export function ApprovalPanel({ session, pj, sessionId }: ApprovalPanelProps): preact.JSX.Element {
  const [approvals, setApprovals] = useState<ApprovalV3[]>([]);
  const [keys, setKeys] = useState<PasskeyInfo[]>([]);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const [revision, setRevision] = useState(0);

  useEffect(() => {
    let active = true;
    session.onEvent((event) => {
      if (!active || event.pj !== pj) return;
      if (event.name === "approval.requested" || event.name === "approval.resolved") {
        setRevision((n) => n + 1);
      }
    });
    return () => {
      active = false;
    };
  }, [session, pj]);

  useEffect(() => {
    let active = true;
    const refresh = async (): Promise<void> => {
      try {
        const result = await session.call("approvals.list", { session_id: sessionId }, pj);
        if (!Array.isArray(result)) throw new Error("The computer sent unreadable approvals.");
        const parsed = result.map((value) => decodeApprovalV3(value));
        if (parsed.some((value) => value === null || value.session_id !== sessionId)) {
          throw new Error("The computer sent unreadable approvals. Reconnect before answering.");
        }
        if (active) {
          setApprovals(parsed.filter((value): value is ApprovalV3 => value !== null));
          setError(null);
        }
      } catch (err) {
        if (active) {
          setApprovals([]);
          setError(err instanceof Error ? err.message : "Could not load approvals.");
        }
      }
    };
    void refresh();
    void listPasskeys(session).then(
      (result) => {
        if (active) setKeys(result);
      },
      () => {
        if (active) setKeys([]);
      },
    );
    return () => {
      active = false;
    };
  }, [session, pj, sessionId, revision]);

  const run = async (action: () => Promise<void>): Promise<void> => {
    setBusy(true);
    setError(null);
    try {
      await action();
      setRevision((n) => n + 1);
    } catch (err) {
      setError(err instanceof Error ? err.message : "The action failed. Please try again.");
    } finally {
      setBusy(false);
    }
  };

  return (
    <section aria-label="Approvals">
      <h2>Approvals</h2>
      {error === null ? null : <p role="alert">{error}</p>}
      {approvals.length === 0 ? <p>No pending approvals.</p> : null}
      {approvals.map((approval) => (
        <ApprovalCard
          key={approval.id}
          approval={approval}
          busy={busy}
          answer={(decision, reason) =>
            run(() => answerApproval(session, pj, approval, decision, reason))
          }
        />
      ))}
      <details>
        <summary>Session mode</summary>
        <button
          type="button"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              await session.call("mode.set", { session_id: sessionId, mode: "plan" }, pj);
            })
          }
        >
          Plan
        </button>
        <button
          type="button"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              await session.call("mode.set", { session_id: sessionId, mode: "ask" }, pj);
            })
          }
        >
          Ask before changes
        </button>
        {session.welcome.server.allow_yolo && (
          <form
            onSubmit={(event) => {
              event.preventDefault();
              void run(() => enableYolo(session, pj, sessionId));
            }}
          >
            <label>
              <input type="checkbox" required disabled={busy} />
              Allow this session to run commands and edits without asking.
            </label>
            <button type="submit" disabled={busy || keys.length === 0}>
              Verify passkey and enable yolo
            </button>
          </form>
        )}
      </details>
      <details>
        <summary>Passkeys ({keys.length})</summary>
        <p>Destructive approvals require a passkey and verification on your device.</p>
        <button
          type="button"
          disabled={busy || keys.length >= 5}
          onClick={() => void run(() => registerPasskey(session))}
        >
          Add passkey
        </button>
        <ul>
          {keys.map((key) => (
            <li key={key.id}>
              Added {new Date(key.created_at).toLocaleDateString()}
              <button
                type="button"
                disabled={busy}
                onClick={() => void run(() => removePasskey(session, key.id))}
              >
                Remove passkey
              </button>
            </li>
          ))}
        </ul>
      </details>
    </section>
  );
}

function ApprovalCard({
  approval,
  busy,
  answer,
}: {
  approval: ApprovalV3;
  busy: boolean;
  answer: (decision: AnswerDecision, reason: string) => Promise<void>;
}): preact.JSX.Element {
  const [reason, setReason] = useState("");
  const destructive = approval.tier !== "normal";
  return (
    <article>
      <h3>{approval.description || "Approval requested"}</h3>
      <p>
        {approval.agent} · {destructive ? "Destructive · passkey required" : "Normal"}
      </p>
      {approval.path ? <p>{approval.path}</p> : null}
      {approval.command ? <pre>{approval.command}</pre> : null}
      {approval.command_truncated ? <p>The command was truncated.</p> : null}
      {approval.diff ? (
        <details>
          <summary>Proposed change</summary>
          <pre>{approval.diff}</pre>
          {approval.diff_truncated ? <p>The diff was truncated.</p> : null}
        </details>
      ) : null}
      {approval.raw ? (
        <details>
          <summary>Tool details</summary>
          <pre>{approval.raw}</pre>
          {approval.raw_truncated ? <p>Tool details were truncated.</p> : null}
        </details>
      ) : null}
      <label>
        Reason for denying (optional)
        <textarea
          value={reason}
          maxLength={2000}
          disabled={busy}
          onInput={(event) => setReason(event.currentTarget.value)}
        />
      </label>
      <div>
        <button type="button" disabled={busy} onClick={() => void answer("deny", reason)}>
          Deny
        </button>
        <button type="button" disabled={busy} onClick={() => void answer("allow", "")}>
          {destructive ? "Verify and approve" : "Approve once"}
        </button>
        <button type="button" disabled={busy} onClick={() => void answer("allow_session", "")}>
          {destructive ? "Verify and allow for session" : "Allow for session"}
        </button>
      </div>
    </article>
  );
}
