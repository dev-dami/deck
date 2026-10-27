import { describe, expect, test } from "vitest";
import { toBoard } from "./crew";
import { DEFAULT_LEVEL, isLevel, LEVELS, levelInfo, levelOf, schedules } from "./orchestration";
import { defaultPrefs, merge } from "./prefs";

describe("orchestration levels", () => {
  test("four levels, least to most, auto by default", () => {
    expect(LEVELS.map((l) => l.level)).toEqual(["off", "assist", "auto", "full"]);
    expect(DEFAULT_LEVEL).toBe("auto");
    expect(defaultPrefs.orchestration).toBe("auto");
    expect(LEVELS.filter((l) => schedules(l.level)).map((l) => l.level)).toEqual(["auto", "full"]);
    expect(levelInfo("full").blurb).toContain("races");
    expect(isLevel("max")).toBe(false);
  });

  test("a board names its level; one from a Deck before levels has the bool", () => {
    expect(toBoard({ level: "assist", orchestrate: false }).level).toBe("assist");
    expect(toBoard({ orchestrate: true }).level).toBe("auto");
    expect(toBoard({}).level).toBe("off");
    expect(levelOf("max", true)).toBe("auto");
  });

  test("the level for new threads survives a load, and an unknown one is the default", () => {
    expect(merge({ orchestration: "full" }).orchestration).toBe("full");
    expect(merge({ orchestration: "max" }).orchestration).toBe("auto");
    expect(merge({}).orchestration).toBe("auto");
  });
});
