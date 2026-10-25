import { Store } from "@web/controller";
import type { Approval, Message, Session } from "@web/protocol";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";
import {
  addNotice,
  attachProject,
  dispatchUi,
  draftOf,
  dropProject,
  projectState,
  roomsBySource,
  setDraft,
  setEngine,
  showProject,
  subscribe,
} from "./appState";
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

  test("each room's status is kept with the project, and the sidebar reads it by source", () => {
    const store = deck();
    attachProject("r1", "folder:/r1", store);
    expect(projectState("r1").rooms["a"]?.state).toBe("idle");
    store.dispatch({ type: "event", event: { e: "busy", p: { session_id: "a", busy: true } } });
    expect(projectState("r1").rooms["a"]?.state).toBe("working");
    const working = projectState("r1").rooms["a"];
    // A token elsewhere keeps the room's status object.
    store.dispatch({ type: "event", event: { e: "message.created", p: msg("m1", "b", "x") } });
    expect(projectState("r1").rooms["a"]).toBe(working);
    expect(roomsBySource()["folder:/r1"]).toBe(projectState("r1").rooms);
    dropProject("r1");
    expect(roomsBySource()["folder:/r1"]).toBeUndefined();
  });

  test("needs-you stays when the room is opened; done goes", () => {
    const store = deck();
    attachProject("r2", "folder:/r2", store);
    const a: Approval = {
      id: "p1",
      session_id: "a",
      agent: "claude-1",
      kind: "command",
      description: "Run a command",
      command: "go test",
      created_at: "2026-10-19T09:00:00Z",
    };
    store.dispatch({ type: "event", event: { e: "approval.requested", p: a } });
    expect(projectState("r2").rooms["a"]).toMatchObject({ state: "needs-you", label: "Approve command" });
    dispatchUi("r2", { type: "open", id: "a" });
    expect(projectState("r2").rooms["a"]?.state).toBe("needs-you");
    store.dispatch({ type: "event", event: { e: "approval.resolved", p: { id: "p1", session_id: "a", granted: true } } });
    expect(projectState("r2").rooms["a"]?.state).toBe("idle");
    // b finishes out of sight: done until the person opens it.
    store.dispatch({ type: "event", event: { e: "session.updated", p: s("b", "all done") } });
    expect(projectState("r2").rooms["b"]).toMatchObject({ state: "done", label: "Finished" });
    dispatchUi("r2", { type: "open", id: "b" });
    expect(projectState("r2").rooms["b"]?.state).toBe("idle");
    dropProject("r2");
  });

  test("a room open in a project off screen gathers news", () => {
    const store = deck();
    attachProject("r3", "folder:/r3", store);
    attachProject("r4", "folder:/r4", deck());
    dispatchUi("r3", { type: "open", id: "a" });
    showProject("r4");
    store.dispatch({ type: "event", event: { e: "session.updated", p: s("a", "finished") } });
    expect(projectState("r3").rooms["a"]?.state).toBe("done");
    // Back on screen, the open room is read.
    showProject("r3");
    expect(projectState("r3").rooms["a"]?.state).toBe("idle");
    dropProject("r3");
    dropProject("r4");
  });

  test("a notice toasts only for the room on screen; the others wait in the inbox", () => {
    const store = deck();
    attachProject("r5", "folder:/r5", store);
    dispatchUi("r5", { type: "open", id: "a" });
    const overlap = "codex-2 and claude-1 both changed auth.go in the last few minutes; one may undo the other";
    store.dispatch({ type: "event", event: { e: "notice", p: { session_id: "a", text: "on screen" } } });
    store.dispatch({ type: "event", event: { e: "notice", p: { session_id: "b", text: overlap } } });
    store.dispatch({ type: "event", event: { e: "notice", p: { session_id: "", text: "for everyone" } } });
    expect(store.state.toasts.map((t) => t.text)).toEqual(["on screen", "for everyone"]);
    expect(projectState("r5").notices.map((n) => [n.session, n.seen])).toEqual([
      ["a", true],
      ["b", false],
    ]);
    expect(projectState("r5").rooms["b"]).toMatchObject({ state: "overlap", label: "codex-2 and claude-1 both changed auth.go" });
    // Opening the room sees the notice.
    dispatchUi("r5", { type: "open", id: "b" });
    expect(projectState("r5").rooms["b"]?.state).toBe("idle");
    expect(addNotice("r5", { session_id: "b", text: "seen at once" })).toBe(true);
    dropProject("r5");
    // A dropped project's store toasts every notice again.
    store.dispatch({ type: "event", event: { e: "notice", p: { session_id: "b", text: "late" } } });
    expect(store.state.toasts.at(-1)?.text).toBe("late");
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
