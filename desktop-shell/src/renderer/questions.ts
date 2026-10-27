import type { Approval, Question, QuestionAnswer } from "@web/protocol";
import { answeredLine, answersOf, EMPTY_DRAFT, pickOption, type QuestionDraft } from "@web/questions";

// The question card's state (ui/QuestionCard.tsx): what the person picked
// and typed (the phone's QuestionDraft) and which question the number keys
// pick for.

export interface CardState {
  draft: QuestionDraft;
  active: number;
}

export const NEW_CARD: CardState = { draft: EMPTY_DRAFT, active: 0 };

export const questionsOf = (a: Approval): Question[] => a.questions ?? [];

export const isQuestion = (a: Approval): boolean => a.kind === "question" && questionsOf(a).length > 0;

// Picks option i of question q; a pick of one choice moves the keys on to
// the next question.
export function pickAt(s: CardState, qs: readonly Question[], q: number, i: number): CardState {
  const question = qs[q];
  const option = question?.options?.[i];
  if (question === undefined || option === undefined) return s;
  const draft = pickOption(s.draft, question, option.label);
  const chose = (draft.picked[question.id] ?? []).length > 0;
  const next = question.multi_select !== true && chose && q + 1 < qs.length ? q + 1 : q;
  return { draft, active: next };
}

export type CardKey = { state: CardState } | { submit: QuestionAnswer[] } | null;

// What a key does on the card: 1-9 picks an option of the active question
// (not while typing in "Other"), Enter submits once something is answered.
export function cardKey(s: CardState, qs: readonly Question[], key: string, typing: boolean): CardKey {
  if (key === "Enter") {
    const answers = answersOf(qs, s.draft);
    return answers.length > 0 ? { submit: answers } : null;
  }
  if (typing || !/^[1-9]$/.test(key)) return null;
  const next = pickAt(s, qs, s.active, Number(key) - 1);
  return next === s ? null : { state: next };
}

// The one line an answered card leaves: "You answered: Post as comments".
export function answeredRecord(answers: readonly QuestionAnswer[]): string {
  return answers.length === 0 ? "You skipped it" : `You answered: ${answeredLine(answers)}`;
}

// A conversation line the engine writes for a question (workspace
// questions.go): "**claude asks:** text", rewritten once settled to
// "**claude asked:** text → You answered: …".
export interface QuestionLine {
  agent: string;
  text: string;
  // What came of it; null while it waits.
  record: string | null;
}

export function parseQuestionLine(text: string): QuestionLine | null {
  const m = /^\*\*(\S+) (asks|asked):\*\* ([\s\S]+)$/.exec(text.trim());
  if (m === null) return null;
  const [, agent = "", verb, rest = ""] = m;
  if (verb === "asks") return { agent, text: rest, record: null };
  const at = rest.lastIndexOf(" → ");
  if (at < 0) return null;
  return { agent, text: rest.slice(0, at), record: rest.slice(at + 3) };
}
