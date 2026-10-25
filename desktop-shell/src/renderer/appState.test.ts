import { Store } from "@web/controller";
import type { Message, Session } from "@web/protocol";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import { attachProject, dispatchUi, draftOf, dropProject, projectState, setDraft, setEngine, subscribe } from "./appState";
import { loadDraft } from "./drafts";

const s = (id: string, last_text = ""): Session => ({
  id,
  title: id,
  updated_at: "2026-10-19T09:00:00Z",
  busy: false,
  pending: 0,
  mode: "ask",
  last_text,
});

const msg = (id: string, session_id: string, text: string): Message => ({
  id,
  session_id,
  role: "assistant",
  agent: "claude-1",
  created_at: "2026-10-19T09:00:00Z",
  updated_at: "2026-10-19T09:00:00Z",
  finished: false,
  parts: [{ type: "text", text }],
});

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

// A Deck's store with two threads loaded.
function deck(): Store {
  const store = new Store();
  store.dispatch({ type: "sessions.loaded", sessions: [s("a"), s("b")] });
  return store;
}

describe("the per-project app state", () => {
  beforeEach(() => {
    vi.stubGlobal("window", { localStorage: memoryStorage() });
  });
  afterEach(() => {
    vi.unstubAllGlobals();
  });

  test("each project keeps its own open thread and panel", () => {
    attachProject("p1", "folder:/one", deck());
    attachProject("p2", "folder:/two", deck());
    dispatchUi("p1", { type: "open", id: "a" });
    dispatchUi("p1", { type: "panel", panel: "changes" });
    dispatchUi("p2", { type: "open", id: "b" });
    expect(projectState("p1").ui).toMatchObject({ openId: "a", panel: "changes" });
    expect(projectState("p2").ui).toMatchObject({ openId: "b", panel: null });
    dropProject("p1");
    dropProject("p2");
  });

  test("a background thread's tokens mark it unread, with or without a Shell", () => {
    const store = deck();
    attachProject("p3", "folder:/three", store);
    dispatchUi("p3", { type: "open", id: "a" });
    store.dispatch({ type: "event", event: { e: "message.created", p: msg("m1", "b", "hel") } });
    store.dispatch({ type: "event", event: { e: "message.created", p: msg("m2", "a", "lo") } });
    expect(projectState("p3").ui.unread).toEqual({ b: true });
    // The marks stay while the project is open; opening the thread reads it.
    dispatchUi("p3", { type: "open", id: "b" });
    expect(projectState("p3").ui.unread).toEqual({});
    dropProject("p3");
  });

  test("a closed project is forgotten and its Deck no longer feeds it", () => {
    const store = deck();
    attachProject("p4", "folder:/four", store);
    dispatchUi("p4", { type: "open", id: "a" });
    dropProject("p4");
    store.dispatch({ type: "event", event: { e: "message.created", p: msg("m1", "b", "x") } });
    expect(projectState("p4").ui.openId).toBeNull();
    expect(projectState("p4").ui.unread).toEqual({});
  });

  test("an unchanged state notifies nobody", () => {
    attachProject("p5", "folder:/five", deck());
    let calls = 0;
    const off = subscribe(() => calls++);
    const before = projectState("p5");
    dispatchUi("p5", { type: "palette", open: false });
    setEngine("p5", null);
    setDraft("p5", "a", "");
    expect(calls).toBe(0);
    expect(projectState("p5")).toBe(before);
    dispatchUi("p5", { type: "palette", open: true });
    expect(calls).toBe(1);
    off();
    dropProject("p5");
  });

  test("drafts are kept per thread and persist under the project's source", () => {
    attachProject("p6", "folder:/six", deck());
    setDraft("p6", "a", "fix the limiter");
    setDraft("p6", "", "a new thread");
    expect(draftOf("p6", "a")).toBe("fix the limiter");
    expect(draftOf("p6", "b")).toBe("");
    expect(loadDraft("folder:/six", "a")).toBe("fix the limiter");
    dropProject("p6");
    // The same folder opened again, under a new key, finds its drafts.
    attachProject("p7", "folder:/six", deck());
    expect(draftOf("p7", "a")).toBe("fix the limiter");
    expect(draftOf("p7", "")).toBe("a new thread");
    dropProject("p7");
  });

  test("the engine state is the project's", () => {
    attachProject("p8", "folder:/eight", deck());
    const stop = { message: "Deck stopped (exit 3).", stderr: ["panic: boom"] };
    setEngine("p8", stop);
    expect(projectState("p8").engine).toBe(stop);
    expect(projectState("p9").engine).toBeNull();
    setEngine("p8", null);
    expect(projectState("p8").engine).toBeNull();
    dropProject("p8");
  });
});
