import { expect, test } from "vitest";
import { asks, boardRows, decisionText, mirrorRow, raceMessage, raceText, taskPreview, toBoard } from "./crew";

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

test("the race form writes one kind, two to six entries", () => {
  expect(raceText("codex", 3, " make the limiter per address ")).toBe("$codex×3 make the limiter per address");
  expect(raceText("claude", 1, "x")).toBe("$claude×2 x");
  expect(raceText("claude", 9, "x")).toBe("$claude×6 x");
  expect(raceText("claude", 3, "  ")).toBeNull();
  expect(raceText("", 3, "x")).toBeNull();
});

const task = (over: Record<string, unknown>) => toBoard({ tasks: [{ id: "t3", title: "entry", status: "done", ...over }] }).tasks[0]!;

test("apply and discard ask first; the others do not", () => {
  expect(["apply", "discard", "cancel", "retry", "steer"].filter(asks)).toEqual(["apply", "discard"]);
});

test("the preview shows the diff when the tree is on the task's branch, else the files", () => {
  const changes = [
    { path: "a.go", added: 3, removed: 1, diff: "@@ a" },
    { path: "c.go", added: 1, removed: 0, diff: "@@ c" },
  ];
  const t = task({ branch: "deck/t3", files: ["a.go", "b.go"] });
  expect(taskPreview(t, "deck/t3", changes)).toEqual([
    { path: "a.go", diff: "@@ a", added: 3, removed: 1 },
    { path: "b.go", diff: null, added: 0, removed: 0 },
  ]);
  expect(taskPreview(t, "main", changes).map((f) => f.diff)).toEqual([null, null]);
  // A task that names no files shows what Changes holds on its branch.
  expect(taskPreview(task({ branch: "deck/t3" }), "deck/t3", changes).map((f) => f.path)).toEqual(["a.go", "c.go"]);
  expect(taskPreview(task({ branch: "deck/t3" }), "main", changes)).toEqual([]);
});

test("the question names what an apply or a discard does", () => {
  const entry = task({ entry: true, owner: "claude-sub", branch: "deck/t3", files: ["a.go", "b.go"] });
  expect(decisionText("apply", entry, 2)).toEqual({
    title: "Apply claude-sub's entry?",
    description: "Deck writes 2 files from deck/t3 into the work tree. The other entries of the race are discarded.",
    button: "Apply",
  });
  expect(decisionText("discard", task({ title: "Port the cache" }), 1).title).toBe("Discard Port the cache?");
});

test("a shared room's lines name who said what", () => {
  expect(mirrorRow("[claude2] building the image")).toEqual({ who: "claude2", to: "", text: "building the image", note: false });
  expect(mirrorRow("[ana → claude-1] look at the cache")).toEqual({ who: "ana", to: "claude-1", text: "look at the cache", note: false });
  expect(mirrorRow("[claude2 ran: go test ./...]")).toEqual({ who: "claude2", to: "", text: "ran: go test ./...", note: true });
  expect(mirrorRow("[... more of the thread was cut here ...]")).toMatchObject({ who: "", note: true });
});
