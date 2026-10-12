import { useEffect, useState } from "preact/hooks";
import type { Approval } from "../protocol";
import { type AppState, sessionTitle } from "../state";
import { DiffView } from "./DiffView";
import { useController } from "./hooks";
import { Sheet } from "./Sheet";

function Detail({ a }: { a: Approval }) {
  if (a.kind === "command") {
    return (
      <>
        <pre class="codeblock command">{a.command ?? a.description}</pre>
        {a.cwd !== undefined ? <p class="muted small-text">in {a.cwd}</p> : null}
      </>
    );
  }
  if (a.kind === "edit") {
    return (
      <>
        {a.path !== undefined ? <p class="path">{a.path}</p> : null}
        {a.diff !== undefined && a.diff !== "" ? (
          <DiffView diff={a.diff} />
        ) : (
          <p class="muted">No diff was provided.</p>
        )}
      </>
    );
  }
  return a.raw !== undefined && a.raw !== "" ? <pre class="codeblock">{a.raw}</pre> : null;
}

export function ApprovalSheet({ state }: { state: AppState }) {
  const controller = useController();
  const current = state.approvals[0];
  const [busy, setBusy] = useState(false);
  const [snoozed, setSnoozed] = useState<string | null>(null);
  const id = current?.id;

  // A fresh approval can arrive while the previous answer is still in flight.
  useEffect(() => setBusy(false), [id]);

  if (current === undefined) return null;
  if (snoozed === current.id) {
    return (
      <button type="button" class="approval-pill btn primary" onClick={() => setSnoozed(null)}>
        {state.approvals.length} approval{state.approvals.length === 1 ? "" : "s"} waiting
      </button>
    );
  }
  const total = state.approvals.length;
  const session = state.sessions.find((s) => s.id === current.session_id);

  const answer = async (decision: "allow" | "allow_session" | "deny") => {
    setBusy(true);
    await controller.answerApproval(current.id, decision);
    setBusy(false);
  };

  return (
    <Sheet title={total > 1 ? `Approval needed (1 of ${total})` : "Approval needed"} persistent>
      <p class="approval-desc">{current.description}</p>
      <p class="muted small-text">
        {current.agent !== undefined ? `$${current.agent} in ` : ""}
        {sessionTitle(session)}
      </p>
      <Detail a={current} />
      <div class="approval-actions">
        <button
          type="button"
          class="btn primary block"
          disabled={busy || state.link !== "online"}
          onClick={() => void answer("allow")}
        >
          Allow
        </button>
        <button
          type="button"
          class="btn block"
          disabled={busy || state.link !== "online"}
          onClick={() => void answer("allow_session")}
        >
          Allow for session
        </button>
        <button
          type="button"
          class="btn danger block"
          disabled={busy || state.link !== "online"}
          onClick={() => void answer("deny")}
        >
          Deny
        </button>
        <button type="button" class="btn ghost block" onClick={() => setSnoozed(current.id)}>
          Decide later
        </button>
      </div>
    </Sheet>
  );
}
