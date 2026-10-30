import { expect, test } from "vitest";
import { tailStart } from "./path";

test("a path's name, and a glob's pattern with its folder", () => {
  const tail = (p: string) => p.slice(tailStart(p));
  expect(tail("main.go")).toBe("main.go");
  expect(tail("pkg/remote/auth/limit.go")).toBe("limit.go");
  expect(tail("pkg/remote/**")).toBe("remote/**");
  expect(tail("internal/deck/session/*.go")).toBe("session/*.go");
  expect(tail("**/*.go")).toBe("**/*.go");
  expect(tail("*.md")).toBe("*.md");
  expect(tail("docs/{a,b}.md")).toBe("docs/{a,b}.md");
});
