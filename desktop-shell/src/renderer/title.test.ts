import { expect, test } from "vitest";
import { titleFrom } from "./title";

test("a title is the first line without the agent mentions", () => {
  expect(titleFrom("$claude write the release notes")).toBe("write the release notes");
  expect(titleFrom("$claude $codex+ split the work\nand compare")).toBe("split the work");
  expect(titleFrom("$codex×3 race   the fix")).toBe("race the fix");
  expect(titleFrom("$all  look at $codex-1 too")).toBe("look at too");
  expect(titleFrom("$claude")).toBe("");
});

test("a long first line is cut with an ellipsis", () => {
  const t = titleFrom(`${"a".repeat(30)} ${"b".repeat(40)}`);
  expect(t).toBe(`${"a".repeat(30)} ${"b".repeat(26)}…`);
});
