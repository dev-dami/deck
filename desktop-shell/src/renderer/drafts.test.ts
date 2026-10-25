import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { loadDraft, saveDraft } from "./drafts";

// A localStorage in memory, as the window has one.
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

describe("drafts", () => {
  let storage: Storage;
  beforeEach(() => {
    storage = memoryStorage();
    vi.stubGlobal("window", { localStorage: storage });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test("a draft comes back per project and thread", () => {
    saveDraft("folder:/a", "ses_1", "half a thought");
    saveDraft("folder:/a", "", "a new thread");
    saveDraft("folder:/b", "ses_1", "another project");
    expect(loadDraft("folder:/a", "ses_1")).toBe("half a thought");
    expect(loadDraft("folder:/a", "")).toBe("a new thread");
    expect(loadDraft("folder:/b", "ses_1")).toBe("another project");
    expect(loadDraft("folder:/b", "ses_2")).toBe("");
  });

  test("keys keep projects and threads apart whatever they contain", () => {
    saveDraft("folder:/a:b", "c", "one");
    saveDraft("folder:/a", "b:c", "two");
    expect(loadDraft("folder:/a:b", "c")).toBe("one");
    expect(loadDraft("folder:/a", "b:c")).toBe("two");
  });

  test("an empty draft removes its entry", () => {
    saveDraft("folder:/a", "ses_1", "text");
    expect(storage.length).toBe(1);
    saveDraft("folder:/a", "ses_1", "");
    expect(storage.length).toBe(0);
    expect(loadDraft("folder:/a", "ses_1")).toBe("");
  });

  test("a storage that throws keeps the app working", () => {
    vi.stubGlobal("window", {
      localStorage: {
        getItem: () => {
          throw new Error("locked");
        },
        setItem: () => {
          throw new Error("full");
        },
        removeItem: () => {
          throw new Error("locked");
        },
      },
    });
    expect(() => saveDraft("folder:/a", "ses_1", "text")).not.toThrow();
    expect(loadDraft("folder:/a", "ses_1")).toBe("");
  });

  test("no window, no storage: nothing is kept and nothing throws", () => {
    vi.unstubAllGlobals();
    expect(() => saveDraft("folder:/a", "ses_1", "text")).not.toThrow();
    expect(loadDraft("folder:/a", "ses_1")).toBe("");
  });
});
