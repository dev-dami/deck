import { describe, expect, test } from "vitest";
import { crewOf, workflowMessage, workflows } from "./workflows";

describe("workflows", () => {
  test("the default agent leads; another kind reviews or answers too", () => {
    expect(crewOf(["claude", "codex"], "claude")).toEqual({ lead: "claude", other: "codex" });
    expect(crewOf(["codex", "claude"], "codex")).toEqual({ lead: "codex", other: "claude" });
    expect(crewOf(["opencode"], "claude")).toEqual({ lead: "opencode", other: "opencode" });
  });

  test("each preset has a level and an end, and only one kind has no second opinion", () => {
    const two = workflows(["claude", "codex"], "claude");
    expect(two.map((w) => w.id)).toEqual(["plan", "review", "second", "race", "split", "factory"]);
    expect(two.map((w) => w.level)).toEqual(["assist", "assist", "off", "off", "auto", "full"]);
    expect(two.find((w) => w.id === "plan")?.mode).toBe("plan");
    for (const w of two) expect(w.ends).not.toBe("");
    expect(workflows(["claude"], "claude").map((w) => w.id)).not.toContain("second");
  });

  test("the first message addresses who starts and states the terms", () => {
    const [, review, second] = workflows(["claude", "codex"], "claude");
    const m = workflowMessage(review!, " fix the login timeout ");
    expect(m.startsWith("@claude fix the login timeout\n")).toBe(true);
    expect(m).toContain('task.create {title, owner: "claude", review: "codex"}');
    expect(m).not.toMatch(/^@codex/m);
    expect(workflowMessage(second!, "which store?")).toMatch(/^@claude @codex which store\?/);
    const race = workflows(["claude", "codex"], "claude").find((w) => w.race)!;
    expect(workflowMessage(race, "fix it")).toBe("fix it");
  });
});
