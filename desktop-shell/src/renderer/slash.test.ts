import { describe, expect, test } from "vitest";
import { parseSlash, SLASH_COMMANDS, slashCommand } from "./slash";

const kinds = ["claude", "codex", "opencode"];
const parse = (t: string, fallback: string | null = "claude") => parseSlash(t, kinds, fallback);

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

  test("a race composes the race text", () => {
    expect(parse("/race 3 codex fix the flaky test")).toEqual({ cmd: "race", text: "$codex×3 fix the flaky test" });
    expect(parse("/race 2 Codex fix it")).toEqual({ cmd: "race", text: "$codex×2 fix it" });
    // No kind: the fallback's.
    expect(parse("/race 4 fix the flaky test")).toEqual({ cmd: "race", text: "$claude×4 fix the flaky test" });
    // A brief whose first word is not a kind keeps it.
    expect(parse("/race 2 refactor auth", "opencode")).toEqual({ cmd: "race", text: "$opencode×2 refactor auth" });
  });

  test("a race that cannot start says why", () => {
    expect(parse("/race")).toHaveProperty("error");
    expect(parse("/race three fix it")).toHaveProperty("error");
    expect(parse("/race 3")).toHaveProperty("error");
    expect(parse("/race 3 codex")).toHaveProperty("error");
    expect(parse("/race 9 fix it")).toEqual({ error: "A race takes 2 to 6 agents." });
    expect(parse("/race 1 fix it")).toEqual({ error: "A race takes 2 to 6 agents." });
    expect(parse("/race 3 fix it", null)).toEqual({ error: "Name the kind of agent to race: /race 3 codex <brief>." });
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
      const sample = c.name === "race" ? "/race 2 codex go" : `/${c.name}`;
      expect(parse(sample)).not.toBeNull();
      expect(slashCommand(c.name)).toBe(c);
    }
  });
});
