import type { Approval, QuestionAnswer } from "@web/protocol";
import { useEffect } from "react";
import type { DeckClient } from "../client";
import { answeredRecord, isQuestion } from "../questions";
import { ApprovalCard } from "./ApprovalCard";
import { AnsweredLine, QuestionCard } from "./QuestionCard";

// What an answered question leaves above the composer.
export interface AnsweredRecord {
  session: string;
  agent: string;
  text: string;
}

// What rides on the open thread's composer: its first pending approval or
// question, or, once a question is answered, the one line that says what was
// answered (answered, kept by the room), until the next request or until the
// person dismisses it.
export function RoomAsk({
  c,
  sessionId,
  pending,
  answerers,
  focusQuestion,
  answered,
  setAnswered,
}: {
  c: DeckClient;
  sessionId: string;
  pending: Approval[];
  answerers: readonly string[] | undefined;
  // A question takes the focus when it appears (the draft is empty).
  focusQuestion: boolean;
  answered: AnsweredRecord | null;
  setAnswered: (r: AnsweredRecord | null) => void;
}) {
  const first = pending[0];
  // A new request puts the record away.
  useEffect(() => {
    if (first !== undefined) setAnswered(null);
  }, [first, setAnswered]);
  if (first === undefined) {
    if (answered === null || answered.session !== sessionId) return null;
    return <AnsweredLine agent={answered.agent} text={answered.text} onDismiss={() => setAnswered(null)} />;
  }
  const answer = (answers: QuestionAnswer[] | null) => {
    setAnswered({ session: sessionId, agent: first.agent, text: answeredRecord(answers ?? []) });
    void c.answerApproval(first.session_id, first.id, answers === null ? "deny" : "answer", answers ?? undefined);
  };
  return (
    <div className="flex flex-col gap-1" aria-label="Approvals for this thread">
      {isQuestion(first) ? (
        <QuestionCard
          a={first}
          index={0}
          count={pending.length}
          className="ring-0"
          {...(answerers !== undefined ? { answerers } : {})}
          autoFocus={focusQuestion}
          onAnswer={(answers) => answer(answers)}
          onSkip={() => answer(null)}
        />
      ) : (
        <ApprovalCard
          a={first}
          index={0}
          count={pending.length}
          className="bg-transparent ring-0"
          {...(answerers !== undefined ? { answerers } : {})}
          onAnswer={(d) => void c.answerApproval(first.session_id, first.id, d)}
        />
      )}
    </div>
  );
}
