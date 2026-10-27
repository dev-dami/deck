import { useEffect, useState } from "preact/hooks";
import type { Approval, Decision, QuestionAnswer } from "../protocol";
import { type AppState, sessionTitle } from "../state";
import { DiffView } from "./DiffView";
import { useController } from "./hooks";
import { QuestionForm } from "./QuestionForm";
import { Sheet } from "./Sheet";

function Detail({ a }: { a: Approval }) {
  if (a.kind === "command") {
    return (
      <>
        <pre class="codeblock command">{a.command ?? a.description}</pre>
        {a.command_truncated === true ? (
          <p class="muted small-text">Command truncated by Deck.</p>
        ) : null}
      </>
    );
  }
  if (a.kind === "edit") {
    return (
      <>
        {a.path !== undefined ? <p class="path">{a.path}</p> : null}
        {a.diff !== undefined && a.diff !== "" ? (
          <DiffView diff={a.diff} truncated={a.diff_truncated === true} />
        ) : (
          <p class="muted">No diff was provided.</p>
        )}
      </>
    );
  }
  return a.raw !== undefined && a.raw !== "" ? (
    <>
      <pre class="codeblock command">{a.raw}</pre>
      {a.raw_truncated === true ? <p class="muted small-text">Truncated by Deck.</p> : null}
    </>
  ) : null;
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
  const agent = state.roster[current.session_id]?.find((a) => a.id === current.agent);

  const answer = async (decision: Decision, answers?: QuestionAnswer[]) => {
    setBusy(true);
    await controller.answerApproval(current.session_id, current.id, decision, answers);
    setBusy(false);
  };

  if (current.kind === "question") {
    return (
      <Sheet
        title={
          total > 1
            ? `${agent?.name ?? current.agent} asks (1 of ${total})`
            : `${agent?.name ?? current.agent} asks`
        }
        persistent
      >
        <p class="muted small-text">in {sessionTitle(session)}</p>
        <QuestionForm
          a={current}
          disabled={busy || state.link !== "online"}
          onAnswer={(answers) => void answer("answer", answers)}
          onSkip={() => void answer("deny")}
        />
        <button type="button" class="btn ghost block" onClick={() => setSnoozed(current.id)}>
          Decide later
        </button>
      </Sheet>
    );
  }

  return (
    <Sheet title={total > 1 ? `Approval needed (1 of ${total})` : "Approval needed"} persistent>
      <p class="approval-desc">{current.description}</p>
      <p class="muted small-text">
        ${agent?.name ?? current.agent} in {sessionTitle(session)}
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
          Allow for this session
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
