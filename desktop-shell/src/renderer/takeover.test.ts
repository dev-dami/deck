import type { ChangedFile } from "@web/protocol";
import { expect, test } from "vitest";
import { changedSince, takeoverNotice, takeoverText } from "./takeover";

const f = (path: string, added: number, removed: number): ChangedFile => ({ path, added, removed });

test("no change since the stop", () => {
  const list = [f("a.go", 3, 1), f("b.go", 1, 0)];
  expect(changedSince(list, [f("b.go", 1, 0), f("a.go", 3, 1)])).toEqual([]);
  expect(changedSince([], [])).toEqual([]);
});

test("a new file, a gone file and a file with other counts", () => {
  const before = [f("a.go", 3, 1), f("b.go", 1, 0), f("c.go", 2, 2)];
  const after = [f("a.go", 5, 1), f("c.go", 2, 2), f("README.md", 4, 0)];
  expect(changedSince(before, after)).toEqual(["README.md", "a.go", "b.go"]);
});

test("the diff text alone does not count", () => {
  expect(changedSince([{ ...f("a.go", 1, 1), diff: "-x\n+y" }], [{ ...f("a.go", 1, 1), diff: "-x\n+z" }])).toEqual([]);
});

test("the notice and the message", () => {
  expect(takeoverNotice(1)).toBe("You changed 1 file since the stop");
  expect(takeoverNotice(3)).toBe("You changed 3 files since the stop");
  expect(takeoverText("claude-1", ["a.go", "b.go"])).toBe("$claude-1 Continue. I changed: a.go, b.go");
  expect(takeoverText(null, ["a.go"])).toBe("Continue. I changed: a.go");
});
