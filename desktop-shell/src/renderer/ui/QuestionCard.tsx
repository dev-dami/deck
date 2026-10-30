import type { Approval, QuestionAnswer } from "@web/protocol";
import { answersOf, isPicked, setOther } from "@web/questions";
import { type KeyboardEvent, useEffect, useRef, useState } from "react";
import { cardKey, NEW_CARD, pickAt, questionsOf } from "../questions";
import { cn } from "../lib/cn";
import { mayAnswer } from "./ApprovalCard";
import { Check, MessageCircleQuestion } from "./icons";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";

// An agent's question (an approval of kind "question": Claude's
// AskUserQuestion, Codex's request_user_input, a Pi select): each question
// under its header, its options as rows to pick (one, or several), an
// "Other" box, then Submit or Skip. The number keys pick for the question in
// turn and Enter submits, while the card has the focus. Stacked over the
// composer like an approval, and in the inbox.
export function QuestionCard({
  a,
  onAnswer,
  onSkip,
  index,
  count,
  answerers,
  autoFocus = false,
  className,
}: {
  a: Approval;
  onAnswer: (answers: QuestionAnswer[]) => void;
  onSkip: () => void;
  index?: number;
  count?: number;
  answerers?: readonly string[];
  // Take the focus when it appears, so the keys work at once.
  autoFocus?: boolean;
  className?: string;
}) {
  const qs = questionsOf(a);
  const [card, setCard] = useState(NEW_CARD);
  const root = useRef<HTMLDivElement>(null);
  const focusOnShow = useRef(autoFocus);
  focusOnShow.current = autoFocus;
  useEffect(() => {
    setCard(NEW_CARD);
    if (!focusOnShow.current) return;
    // After the room's own focus (the composer takes it when a room opens).
    const t = setTimeout(() => root.current?.focus({ preventScroll: true }), 60);
    return () => clearTimeout(t);
  }, [a.id]);
  const answers = answersOf(qs, card.draft);

  const onKey = (e: KeyboardEvent<HTMLDivElement>) => {
    if (e.ctrlKey || e.metaKey || e.altKey || e.shiftKey) return;
    const typing = e.target instanceof HTMLInputElement;
    const r = cardKey(card, qs, e.key, typing);
    if (r === null) return;
    e.preventDefault();
    e.stopPropagation();
    if ("submit" in r) onAnswer(r.submit);
    else setCard(r.state);
  };

  return (
    <div
      ref={root}
      role="group"
      aria-label={`${a.agent} asks`}
      tabIndex={-1}
      className={cn("question-card card flex flex-col gap-3 rounded-2xl bg-surface-1 px-3.5 pt-3 pb-2.5 outline-none ring-1 ring-hairline", className)}
      data-approval={a.id}
      data-question=""
      onKeyDown={onKey}
    >
      <span className="flex w-full min-w-0 items-baseline gap-2 text-xs">
        <MessageCircleQuestion aria-hidden className="size-3.5 shrink-0 self-center text-warning-foreground" weight="fill" />
        <span className="shrink-0 font-medium text-warning-foreground">Question</span>
        <span className="min-w-0 truncate font-mono text-muted-foreground">{a.agent}</span>
        {count !== undefined && count > 1 && (
          <span className="ml-auto shrink-0 text-2xs text-faint tabular-nums">
            {(index ?? 0) + 1} of {count}
          </span>
        )}
      </span>
      <div className="-mx-1 flex max-h-[min(26rem,50vh)] flex-col gap-3 overflow-y-auto px-1 py-0.5">
        {qs.map((q, qi) => {
          const many = q.multi_select === true;
          return (
            <fieldset key={q.id} className="question flex min-w-0 flex-col gap-1.5" data-active={qi === card.active ? "" : undefined}>
              <legend className="mb-1.5 flex w-full min-w-0 flex-col gap-1">
                {q.header !== undefined && q.header !== "" && (
                  <span className="question-header w-fit rounded-full bg-hover px-2 py-px font-medium text-2xs text-muted-foreground uppercase tracking-wide">
                    {q.header}
                  </span>
                )}
                <span className="question-text text-pretty font-medium text-[13px] text-foreground">{q.text}</span>
              </legend>
              <div role={many ? "group" : "radiogroup"} aria-label={q.text} className="flex flex-col gap-1">
                {(q.options ?? []).map((o, i) => {
                  const on = isPicked(card.draft, q, o.label);
                  return (
                    <button
                      key={o.label}
                      type="button"
                      role={many ? "checkbox" : "radio"}
                      aria-checked={on}
                      data-option={o.label}
                      className={cn(
                        "question-option flex w-full min-w-0 cursor-pointer items-start gap-2.5 rounded-[10px] px-2.5 py-1 text-left outline-none ring-1 transition-colors focus-visible:ring-ring",
                        on ? "bg-selected ring-primary/60" : "ring-hairline hover:bg-hover",
                      )}
                      onClick={() => setCard((s) => ({ ...pickAt({ ...s, active: qi }, qs, qi, i) }))}
                    >
                      <span
                        aria-hidden
                        className={cn(
                          "mt-0.5 flex size-3.5 shrink-0 items-center justify-center border",
                          many ? "rounded-[4px]" : "rounded-full",
                          on ? "border-primary bg-primary text-primary-foreground" : "border-hairline-strong",
                        )}
                      >
                        {on && (many ? <Check className="size-2.5" weight="bold" /> : <span className="size-1.5 rounded-full bg-current" />)}
                      </span>
                      <span className="flex min-w-0 flex-1 flex-col">
                        <span className="text-[13px] text-foreground">{o.label}</span>
                        {o.description !== undefined && o.description !== "" && (
                          <span className="text-pretty text-muted-foreground text-xs">{o.description}</span>
                        )}
                      </span>
                      {qi === card.active && i < 9 && <Kbd className="shrink-0 self-center">{i + 1}</Kbd>}
                    </button>
                  );
                })}
              </div>
              {(q.other === true || (q.options ?? []).length === 0) && (
                <input
                  type="text"
                  className="question-other h-8 w-full min-w-0 rounded-[10px] bg-hover px-2.5 text-[13px] text-foreground outline-none ring-1 ring-hairline placeholder:text-faint focus-visible:ring-ring"
                  placeholder={(q.options ?? []).length === 0 ? "Your answer…" : "Other…"}
                  aria-label={`Other answer: ${q.text}`}
                  value={card.draft.other[q.id] ?? ""}
                  onFocus={() => setCard((s) => ({ ...s, active: qi }))}
                  onChange={(e) => {
                    const text = e.target.value;
                    setCard((s) => ({ ...s, draft: setOther(s.draft, q, text) }));
                  }}
                />
              )}
            </fieldset>
          );
        })}
      </div>
      <div className="question-actions flex flex-wrap items-center justify-end gap-1.5">
        {answerers !== undefined && answerers.length > 0 ? (
          <span className="mr-auto min-w-0 truncate text-2xs text-faint">{mayAnswer(answerers)}</span>
        ) : (
          <span className="mr-auto hidden items-center gap-1 text-2xs text-faint sm:flex">
            <Kbd>1</Kbd>–<Kbd>{Math.min(9, Math.max(1, qs[card.active]?.options?.length ?? 1))}</Kbd>
            <span className="mr-1.5 ml-0.5">pick</span>
            <Kbd>↵</Kbd>
            <span className="ml-0.5">submit</span>
          </span>
        )}
        <Button size="sm" variant="ghost-muted" className="question-skip" onClick={onSkip}>
          Skip
        </Button>
        <Button size="sm" className="question-submit" disabled={answers.length === 0} onClick={() => onAnswer(answers)}>
          Submit
        </Button>
      </div>
    </div>
  );
}

// What an answered card leaves above the composer until the agent moves
// on: "You answered: Post as comments".
export function AnsweredLine({ agent, text, onDismiss }: { agent: string; text: string; onDismiss: () => void }) {
  return (
    <div className="question-answered flex min-w-0 items-center gap-2 rounded-2xl px-3.5 py-2 text-xs" data-answered="">
      <Check aria-hidden className="size-3.5 shrink-0 text-success-foreground" />
      <span className="min-w-0 truncate text-muted-foreground">
        <span className="font-mono">{agent}</span> · <span className="text-foreground">{text}</span>
      </span>
      <Button size="xs" variant="ghost-muted" className="ml-auto" onClick={onDismiss}>
        Dismiss
      </Button>
    </div>
  );
}
