import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { HISTORY_MAX, loadHistory, pushHistory, remember, walk } from "./history";

function memoryStorage(): Storage {
  const m = new Map<string, string>();
  return {
    get length() {
      return m.size;
    },
    key: (i) => [...m.keys()][i] ?? null,
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => {
      m.set(k, String(v));
    },
    removeItem: (k) => {
      m.delete(k);
    },
    clear: () => m.clear(),
  };
}

describe("remember", () => {
  test("puts the newest first and keeps the last fifty", () => {
    let list: string[] = [];
    for (let i = 1; i <= HISTORY_MAX + 5; i++) list = remember(list, `m${i}`);
    expect(list).toHaveLength(HISTORY_MAX);
    expect(list[0]).toBe(`m${HISTORY_MAX + 5}`);
    expect(list.at(-1)).toBe("m6");
  });

  test("keeps a repeat once and skips blank text", () => {
    expect(remember(["a", "b"], "a")).toEqual(["a", "b"]);
    expect(remember(["a", "b"], "b")).toEqual(["b", "a", "b"]);
    expect(remember(["a"], "  \n")).toEqual(["a"]);
  });
});

describe("walk", () => {
  const list = ["newest", "older", "oldest"];

  test("up goes back in time and stops at the oldest", () => {
    expect(walk(list, -1, "up")).toEqual({ at: 0, text: "newest" });
    expect(walk(list, 0, "up")).toEqual({ at: 1, text: "older" });
    expect(walk(list, 2, "up")).toBeNull();
    expect(walk([], -1, "up")).toBeNull();
  });

  test("down comes forward and ends on an empty composer", () => {
    expect(walk(list, 2, "down")).toEqual({ at: 1, text: "older" });
    expect(walk(list, 0, "down")).toEqual({ at: -1, text: "" });
    expect(walk(list, -1, "down")).toBeNull();
  });
});

describe("the stored history", () => {
  beforeEach(() => vi.stubGlobal("window", { localStorage: memoryStorage() }));
  afterEach(() => vi.unstubAllGlobals());

  test("is kept per project and room", () => {
    pushHistory("folder:/a", "ses_1", "first");
    pushHistory("folder:/a", "ses_1", "second");
    pushHistory("folder:/a", "ses_2", "elsewhere");
    pushHistory("folder:/b", "ses_1", "other project");
    expect(loadHistory("folder:/a", "ses_1")).toEqual(["second", "first"]);
    expect(loadHistory("folder:/a", "ses_2")).toEqual(["elsewhere"]);
    expect(loadHistory("folder:/b", "ses_1")).toEqual(["other project"]);
    expect(loadHistory("folder:/c", "ses_1")).toEqual([]);
  });

  test("survives bad data and a locked storage", () => {
    window.localStorage.setItem('deck.desktop.history:["p","r"]', "{not json");
    expect(loadHistory("p", "r")).toEqual([]);
    window.localStorage.setItem('deck.desktop.history:["p","r"]', JSON.stringify(["ok", 3, null]));
    expect(loadHistory("p", "r")).toEqual(["ok"]);
    vi.stubGlobal("window", {
      localStorage: {
        getItem: () => {
          throw new Error("locked");
        },
        setItem: () => {
          throw new Error("locked");
        },
      },
    });
    expect(loadHistory("p", "r")).toEqual([]);
    expect(() => pushHistory("p", "r", "x")).not.toThrow();
  });
});
