import type { Message } from "@web/protocol";
import { parseQuestionLine, type QuestionLine } from "../questions";
import { Check, MessageCircleQuestion } from "./icons";

// The engine's line for an agent's question (workspace questions.go), or
// null for another system message.
export function questionLineOf(m: Message): QuestionLine | null {
  if (m.role !== "system") return null;
  const text = m.parts
    .filter((p) => p.type === "text")
    .map((p) => p.text ?? "")
    .join("");
  return parseQuestionLine(text);
}

// An agent's question in the conversation, one quiet line: what it asks,
// in the warning hue while it waits for the card over the composer, then
// what came of it ("You answered: Post as comments").
export function QuestionRow({ m, q }: { m: Message; q: QuestionLine }) {
  const waiting = q.record === null;
  return (
    <p
      className="msg msg-question mx-auto flex max-w-[90%] flex-wrap items-baseline justify-center gap-x-1.5 text-pretty text-center text-muted-foreground text-xs"
      data-role="system"
      data-mid={m.id}
      data-question={waiting ? "pending" : "settled"}
    >
      {waiting ? (
        <MessageCircleQuestion aria-hidden className="size-3.5 shrink-0 self-center text-warning-foreground" weight="fill" />
      ) : (
        <Check aria-hidden className="size-3.5 shrink-0 self-center text-faint" />
      )}
      <span className="font-mono">{q.agent}</span>
      <span>{waiting ? "asks" : "asked"}</span>
      <span className="text-foreground">{q.text}</span>
      {waiting ? (
        <span className="text-warning-foreground">· answer below</span>
      ) : (
        <span className="question-record">
          → <span className={q.record?.startsWith("You answered") === true ? "text-foreground" : "text-faint"}>{q.record}</span>
        </span>
      )}
    </p>
  );
}
