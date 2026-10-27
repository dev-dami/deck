import { useEffect, useState } from "preact/hooks";
import type { Approval, QuestionAnswer } from "../protocol";
import {
  answersOf,
  EMPTY_DRAFT,
  isPicked,
  pickOption,
  type QuestionDraft,
  setOther,
} from "../questions";

// An agent's question (an approval of kind "question"): each question with
// its options to tap (one, or several), an "Other" box, then Submit or Skip.
export function QuestionForm({
  a,
  disabled,
  onAnswer,
  onSkip,
}: {
  a: Approval;
  disabled: boolean;
  onAnswer: (answers: QuestionAnswer[]) => void;
  onSkip: () => void;
}) {
  const [draft, setDraft] = useState<QuestionDraft>(EMPTY_DRAFT);
  useEffect(() => setDraft(EMPTY_DRAFT), [a.id]);
  const qs = a.questions ?? [];
  const answers = answersOf(qs, draft);
  return (
    <div class="question-form">
      {qs.map((q) => (
        <fieldset key={q.id} class="question">
          <legend>
            {q.header !== undefined && q.header !== "" ? (
              <span class="question-header">{q.header}</span>
            ) : null}
            <span class="question-text">{q.text}</span>
          </legend>
          {(q.options ?? []).map((o) => (
            <label
              key={o.label}
              class={`question-option${isPicked(draft, q, o.label) ? " picked" : ""}`}
            >
              <input
                type={q.multi_select === true ? "checkbox" : "radio"}
                name={q.id}
                checked={isPicked(draft, q, o.label)}
                onClick={() => setDraft((d) => pickOption(d, q, o.label))}
              />
              <span>
                <span class="question-label">{o.label}</span>
                {o.description !== undefined && o.description !== "" ? (
                  <span class="muted small-text"> {o.description}</span>
                ) : null}
              </span>
            </label>
          ))}
          {q.other === true || (q.options ?? []).length === 0 ? (
            <input
              type="text"
              class="question-other"
              placeholder="Other…"
              aria-label={`Other answer to ${q.text}`}
              value={draft.other[q.id] ?? ""}
              onInput={(e) => {
                const text = (e.currentTarget as HTMLInputElement).value;
                setDraft((d) => setOther(d, q, text));
              }}
            />
          ) : null}
        </fieldset>
      ))}
      <div class="approval-actions">
        <button
          type="button"
          class="btn primary block"
          disabled={disabled || answers.length === 0}
          onClick={() => onAnswer(answers)}
        >
          Submit
        </button>
        <button type="button" class="btn block" disabled={disabled} onClick={onSkip}>
          Skip
        </button>
      </div>
    </div>
  );
}
