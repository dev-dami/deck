import { describe, expect, test } from "bun:test";
import type { Question } from "./protocol";
import { answeredLine, answersOf, EMPTY_DRAFT, isPicked, pickOption, setOther } from "./questions";

const one: Question = {
  id: "q1",
  text: "How?",
  options: [{ label: "Comments" }, { label: "Summary" }],
  other: true,
};
const many: Question = {
  id: "q2",
  text: "Which?",
  options: [{ label: "Lint" }, { label: "Unit" }],
  multi_select: true,
  other: true,
};

describe("question drafts", () => {
  test("one choice replaces the last and the typed answer", () => {
    let d = setOther(EMPTY_DRAFT, one, "something else");
    d = pickOption(d, one, "Comments");
    expect(isPicked(d, one, "Comments")).toBe(true);
    expect(d.other["q1"]).toBeUndefined();
    d = pickOption(d, one, "Summary");
    expect(answersOf([one], d)).toEqual([{ id: "q1", selected: ["Summary"] }]);
    d = setOther(d, one, "neither");
    expect(answersOf([one], d)).toEqual([{ id: "q1", other: "neither" }]);
  });

  test("several toggle and go with the typed answer", () => {
    let d = pickOption(EMPTY_DRAFT, many, "Lint");
    d = pickOption(d, many, "Unit");
    d = pickOption(d, many, "Lint");
    d = setOther(d, many, " vet ");
    expect(answersOf([one, many], d)).toEqual([{ id: "q2", selected: ["Unit"], other: "vet" }]);
    expect(answeredLine(answersOf([one, many], pickOption(d, one, "Comments")))).toBe(
      "Comments; Unit, vet",
    );
  });

  test("nothing picked is no answer", () => {
    expect(answersOf([one, many], EMPTY_DRAFT)).toEqual([]);
    expect(answersOf([one], setOther(EMPTY_DRAFT, one, "  "))).toEqual([]);
  });
});
