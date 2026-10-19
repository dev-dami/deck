import { expect, test } from "vitest";
import { boardRows, raceMessage, toBoard } from "./crew";

test("race entries sit under their race", () => {
  const b = toBoard({
    tasks: [
      { id: "t1", title: "plan", status: "done" },
      { id: "t2", title: "race", status: "running", race: true },
      { id: "t3", title: "a", entry: true },
      { id: "t4", title: "b", entry: true },
      { id: "t5", title: "after", status: "pending" },
    ],
  });
  const rows = boardRows(b);
  expect(rows.map((r) => r.task.id)).toEqual(["t1", "t2", "t5"]);
  expect(rows[1]?.entries.map((e) => e.id)).toEqual(["t3", "t4"]);
});

test("a race is written in the composer's syntax", () => {
  expect(raceMessage({ claude: 2, codex: 1, opencode: 0 }, " fix the flaky test ")).toBe("$claude×2 $codex×1 fix the flaky test");
});
