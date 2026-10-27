import { describe, expect, test } from "vitest";
import { parseSlash, SLASH_COMMANDS, slashCommand } from "./slash";

const parse = (t: string) => parseSlash(t);

describe("parseSlash", () => {
  test("the modes", () => {
    expect(parse("/plan")).toEqual({ cmd: "mode", mode: "plan" });
    expect(parse("  /ask ")).toEqual({ cmd: "mode", mode: "ask" });
    expect(parse("/yolo")).toEqual({ cmd: "mode", mode: "yolo" });
  });

  test("share, stop and clear", () => {
    expect(parse("/share")).toEqual({ cmd: "share" });
    expect(parse("/stop")).toEqual({ cmd: "stop" });
    expect(parse("/clear")).toEqual({ cmd: "clear" });
  });

  test("a command that takes nothing refuses text after it", () => {
    expect(parse("/plan fix it")).toEqual({ error: "/plan takes nothing after it." });
    expect(parse("/stop now")).toEqual({ error: "/stop takes nothing after it." });
  });

  test("new, with or without a title", () => {
    expect(parse("/new")).toEqual({ cmd: "new", title: "" });
    expect(parse("/new  Fix   the docs ")).toEqual({ cmd: "new", title: "Fix the docs" });
  });

  test("race opens the builder, with the rest of the message as its task", () => {
    expect(parse("/race")).toEqual({ cmd: "race", brief: "" });
    expect(parse("/race  fix the flaky limiter test ")).toEqual({ cmd: "race", brief: "fix the flaky limiter test" });
    // The typed counts and kinds of before are part of the task now.
    expect(parse("/race 3 codex fix it")).toEqual({ cmd: "race", brief: "3 codex fix it" });
    expect(slashCommand("race")?.instant).toBe(true);
  });

  test("other text is not a command", () => {
    expect(parse("/usr/bin is on the PATH")).toBeNull();
    expect(parse("/unknown")).toBeNull();
    expect(parse("fix /plan")).toBeNull();
    expect(parse("")).toBeNull();
    expect(parse("/")).toBeNull();
  });

  test("every listed command parses", () => {
    for (const c of SLASH_COMMANDS) {
      expect(parse(`/${c.name}`)).not.toBeNull();
      expect(slashCommand(c.name)).toBe(c);
    }
  });
});
