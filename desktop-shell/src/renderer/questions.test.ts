import type { Approval, Question } from "@web/protocol";
import { setOther } from "@web/questions";
import { describe, expect, test } from "vitest";
import { approvalLabel } from "./attention";
import { answeredRecord, cardKey, isQuestion, NEW_CARD, parseQuestionLine, pickAt } from "./questions";
import { toApprovals } from "./rpcMap";

const delivery: Question = {
  id: "How should I deliver them?",
  header: "Delivery",
  text: "How should I deliver them?",
  options: [{ label: "Post as comments", description: "One per finding" }, { label: "Summary only" }],
  other: true,
};
const checks: Question = {
  id: "Re-run?",
  text: "Re-run?",
  options: [{ label: "Lint" }, { label: "Unit" }, { label: "E2E" }],
  multi_select: true,
  other: true,
};
const qs = [delivery, checks];

const ask: Approval = {
  id: "toolu_1",
  session_id: "s1",
  agent: "claude",
  kind: "question",
  description: "claude asks: How should I deliver them?",
  created_at: "2026-10-27T09:00:00Z",
  questions: qs,
};

describe("the question card", () => {
  test("a number picks for the question in turn; one choice moves on", () => {
    let r = cardKey(NEW_CARD, qs, "1", false);
    expect(r !== null && "state" in r).toBe(true);
    let s = r !== null && "state" in r ? r.state : NEW_CARD;
    expect(s.draft.picked[delivery.id]).toEqual(["Post as comments"]);
    expect(s.active).toBe(1);
    // Several stay on their question.
    for (const k of ["1", "3", "1"]) {
      r = cardKey(s, qs, k, false);
      s = r !== null && "state" in r ? r.state : s;
    }
    expect(s.draft.picked[checks.id]).toEqual(["E2E"]);
    expect(s.active).toBe(1);
    // Past the options, or while typing in Other, a number does nothing.
    expect(cardKey(s, qs, "7", false)).toBeNull();
    expect(cardKey(s, qs, "2", true)).toBeNull();
    expect(cardKey(s, qs, "x", false)).toBeNull();
  });

  test("Enter submits what is answered, and only then", () => {
    expect(cardKey(NEW_CARD, qs, "Enter", false)).toBeNull();
    const s = { ...pickAt(NEW_CARD, qs, 0, 0), draft: setOther(pickAt(NEW_CARD, qs, 0, 0).draft, checks, "go vet") };
    expect(cardKey(s, qs, "Enter", true)).toEqual({
      submit: [
        { id: delivery.id, selected: ["Post as comments"] },
        { id: checks.id, other: "go vet" },
      ],
    });
  });

  test("a pick by click makes its question the one the keys pick for", () => {
    const s = pickAt(NEW_CARD, qs, 1, 1);
    expect(s.active).toBe(1);
    expect(s.draft.picked[checks.id]).toEqual(["Unit"]);
  });

  test("an answered card leaves one line", () => {
    expect(answeredRecord([{ id: delivery.id, selected: ["Post as comments"] }])).toBe("You answered: Post as comments");
    expect(answeredRecord([{ id: "a", selected: ["Lint", "Unit"], other: "vet" }, { id: "b", other: "x" }])).toBe("You answered: Lint, Unit, vet; x");
    expect(answeredRecord([])).toBe("You skipped it");
  });

  test("a question is an approval that asks", () => {
    expect(isQuestion(ask)).toBe(true);
    expect(isQuestion({ ...ask, kind: "other" })).toBe(false);
    expect(isQuestion({ ...ask, questions: [] })).toBe(false);
    // The room's status: "needs you · Answer claude".
    expect(approvalLabel(ask)).toBe("Answer claude");
  });
});

describe("the conversation line", () => {
  test("waiting, then what came of it", () => {
    expect(parseQuestionLine("**claude asks:** How should I deliver them? (+1 more)")).toEqual({
      agent: "claude",
      text: "How should I deliver them? (+1 more)",
      record: null,
    });
    expect(parseQuestionLine("**codex-2 asked:** Which? → You answered: Post as comments; Lint")).toEqual({
      agent: "codex-2",
      text: "Which?",
      record: "You answered: Post as comments; Lint",
    });
    expect(parseQuestionLine("**claude asked:** no record")).toBeNull();
    expect(parseQuestionLine("_a notice_")).toBeNull();
  });
});

describe("deck.v1 questions", () => {
  test("map to the wire approval", () => {
    const got = toApprovals({
      approvals: [
        {
          id: "toolu_1",
          session_id: "s1",
          tool_call_id: "toolu_1",
          agent: "claude",
          kind: "question",
          description: "claude asks: How should I deliver them?",
          command: "",
          path: "",
          diff: "",
          raw: "",
          created_at: Date.parse("2026-10-27T09:00:00Z"),
          questions: [
            { id: delivery.id, header: "Delivery", text: delivery.text, multi_select: false, other: true, options: [{ label: "Post as comments", description: "One per finding" }, { label: "Summary only", description: "" }] },
            { id: checks.id, header: "", text: checks.text, multi_select: true, other: true, options: checks.options },
          ],
        },
      ],
    });
    expect(got).toEqual([{ ...ask, tool_call_id: "toolu_1", created_at: "2026-10-27T09:00:00.000Z" }]);
  });
});
