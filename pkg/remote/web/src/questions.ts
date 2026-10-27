// What a person is answering to an agent's question approval (kind
// "question"), shared by the phone and the desktop: the options picked and
// the "Other" text, per question id.

import type { Question, QuestionAnswer } from "./protocol";

export interface QuestionDraft {
  picked: Readonly<Record<string, readonly string[]>>;
  other: Readonly<Record<string, string>>;
}

export const EMPTY_DRAFT: QuestionDraft = { picked: {}, other: {} };

// Picks label: one choice replaces the last (and the typed answer), several
// toggle.
export function pickOption(d: QuestionDraft, q: Question, label: string): QuestionDraft {
  const was = d.picked[q.id] ?? [];
  if (q.multi_select === true) {
    const next = was.includes(label) ? was.filter((l) => l !== label) : [...was, label];
    return { ...d, picked: { ...d.picked, [q.id]: next } };
  }
  const other = { ...d.other };
  delete other[q.id];
  return {
    picked: { ...d.picked, [q.id]: was.length === 1 && was[0] === label ? [] : [label] },
    other,
  };
}

// Types the "Other" answer: for one choice it stands instead of the options.
export function setOther(d: QuestionDraft, q: Question, text: string): QuestionDraft {
  const picked =
    q.multi_select === true || text.trim() === "" ? d.picked : { ...d.picked, [q.id]: [] };
  return { picked, other: { ...d.other, [q.id]: text } };
}

export function isPicked(d: QuestionDraft, q: Question, label: string): boolean {
  return (d.picked[q.id] ?? []).includes(label);
}

// The answers to send: one per question with something picked or typed.
export function answersOf(qs: readonly Question[], d: QuestionDraft): QuestionAnswer[] {
  const out: QuestionAnswer[] = [];
  for (const q of qs) {
    const selected = (d.picked[q.id] ?? []).filter((l) =>
      (q.options ?? []).some((o) => o.label === l),
    );
    const other = (d.other[q.id] ?? "").trim();
    if (selected.length === 0 && other === "") continue;
    out.push({
      id: q.id,
      ...(selected.length > 0 ? { selected } : {}),
      ...(other !== "" ? { other } : {}),
    });
  }
  return out;
}

// What an answer says, the way the agent reads it: "Lint, Unit, go vet".
export function answerText(a: QuestionAnswer): string {
  return [
    ...(a.selected ?? []),
    ...(a.other !== undefined && a.other !== "" ? [a.other] : []),
  ].join(", ");
}

// The one line that records what was answered: "Post as comments; Lint, Unit".
export function answeredLine(answers: readonly QuestionAnswer[]): string {
  return answers.map(answerText).join("; ");
}
