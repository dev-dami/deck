import { describe, expect, test } from "vitest";
import { moveIndex, trapIndex } from "./focus";

describe("moveIndex", () => {
  test("the arrows step and wrap", () => {
    expect(moveIndex("ArrowDown", 0, 3)).toBe(1);
    expect(moveIndex("ArrowDown", 2, 3)).toBe(0);
    expect(moveIndex("ArrowUp", 0, 3)).toBe(2);
    expect(moveIndex("ArrowUp", 2, 3)).toBe(1);
  });

  test("with no item focused, down goes to the first and up to the last", () => {
    expect(moveIndex("ArrowDown", -1, 3)).toBe(0);
    expect(moveIndex("ArrowUp", -1, 3)).toBe(2);
  });

  test("Home and End go to the ends", () => {
    expect(moveIndex("Home", 2, 4)).toBe(0);
    expect(moveIndex("End", 0, 4)).toBe(3);
  });

  test("other keys and empty menus do not move", () => {
    expect(moveIndex("Enter", 1, 3)).toBeNull();
    expect(moveIndex("a", 1, 3)).toBeNull();
    expect(moveIndex("ArrowDown", -1, 0)).toBeNull();
  });
});

describe("trapIndex", () => {
  test("Tab wraps from the last element to the first", () => {
    expect(trapIndex(0, 3, false)).toBe(1);
    expect(trapIndex(2, 3, false)).toBe(0);
  });

  test("Shift+Tab wraps from the first element to the last", () => {
    expect(trapIndex(1, 3, true)).toBe(0);
    expect(trapIndex(0, 3, true)).toBe(2);
  });

  test("from outside the trap, focus enters at an end", () => {
    expect(trapIndex(-1, 3, false)).toBe(0);
    expect(trapIndex(-1, 3, true)).toBe(2);
  });
});
