import { Store } from "@web/controller";
import type { Message, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import { describe, expect, test } from "vitest";
import {
  kindsBySession,
  type Memo,
  panelSlice,
  pick,
  sameData,
  sameKinds,
  shallowEqual,
  shellSlice,
  threadSlice,
} from "./select";

const s = (id: string): Session => ({
  id,
  title: id,
  updated_at: "2026-10-19T09:00:00Z",
  busy: false,
  pending: 0,
  mode: "ask",
  last_text: "",
});

const msg = (id: string, session_id: string, agent: string, text: string): Message => ({
  id,
  session_id,
  role: "assistant",
  agent,
  created_at: "2026-10-19T09:00:00Z",
  updated_at: `2026-10-19T09:00:0${text.length % 10}Z`,
  finished: false,
  parts: [{ type: "text", text }],
});

// A store with an open thread "a" and a thread "b" in the background.
function deck(): Store {
  const store = new Store();
  store.dispatch({ type: "sessions.loaded", sessions: [s("a"), s("b")] });
  store.dispatch({ type: "messages.loaded", sessionId: "a", messages: [msg("m1", "a", "claude-1", "hi")], more: false, cursor: null, latest: true });
  store.dispatch({ type: "roster.loaded", sessionId: "a", roster: [{ id: "claude-1", name: "claude-1", kind: "claude", busy: false }] });
  return store;
}

// A token: one more character in a streaming message.
function token(store: Store, session: string, text: string, agent = "codex-1"): void {
  store.dispatch({ type: "event", event: { e: "message.updated", p: msg(`${session}-reply`, session, agent, text) } });
}

// Reads a slice the way useSelect does, across store changes.
function reader<T>(store: Store, select: (s: AppState) => T, equal: (a: T, b: T) => boolean) {
  const memo: { current: Memo<AppState, T> | null } = { current: null };
  return () => pick(memo, store.state, select, equal);
}

describe("slices", () => {
  test("a background thread's tokens leave the shell's slice as it was", () => {
    const store = deck();
    const shell = reader(store, shellSlice, shallowEqual);
    const panel = reader(store, panelSlice, shallowEqual);
    const first = shell();
    const firstPanel = panel();
    token(store, "b", "w");
    token(store, "b", "wo");
    token(store, "b", "wor");
    expect(shell()).toBe(first);
    expect(panel()).toBe(firstPanel);
    // A change the shell reads gives a new slice.
    store.dispatch({ type: "event", event: { e: "busy", p: { session_id: "b", busy: true } } });
    expect(shell()).not.toBe(first);
  });

  test("the open timeline reads its own thread only", () => {
    const store = deck();
    const open = reader(store, threadSlice("a"), shallowEqual);
    const first = open();
    expect(first.messages.map((m) => m.id)).toEqual(["m1"]);
    token(store, "b", "w");
    token(store, "b", "wo");
    expect(open()).toBe(first);
    token(store, "a", "x");
    const next = open();
    expect(next).not.toBe(first);
    expect(next.messages.map((m) => m.id).sort()).toEqual(["a-reply", "m1"]);
    // The first message is the same object: its row does not render again.
    expect(next.messages.find((m) => m.id === "m1")).toBe(first.messages[0]);
  });

  test("a thread with nothing loaded reads the same empty lists each time", () => {
    const store = deck();
    const none = reader(store, threadSlice("c"), shallowEqual);
    const first = none();
    token(store, "b", "w");
    expect(none()).toBe(first);
    expect(threadSlice("c")(store.state).messages).toBe(first.messages);
    expect(threadSlice("c")(store.state).roster).toBe(first.roster);
  });

  test("the sidebar's agent marks change only when an agent kind joins", () => {
    const store = deck();
    const kinds = reader(store, kindsBySession, sameKinds);
    token(store, "b", "w");
    const first = kinds();
    expect(first).toEqual({ a: ["claude"], b: ["codex"] });
    token(store, "b", "wo");
    token(store, "b", "wor");
    token(store, "a", "x", "claude-1");
    expect(kinds()).toBe(first);
    store.dispatch({ type: "event", event: { e: "message.created", p: msg("m9", "b", "claude-2", "late") } });
    expect(kinds()).toEqual({ a: ["claude"], b: ["codex", "claude"] });
  });

  test("a select that changes with the same state is applied", () => {
    const store = deck();
    const memo: { current: Memo<AppState, unknown> | null } = { current: null };
    const a = pick(memo, store.state, threadSlice("a"), shallowEqual) as { messages: Message[] };
    const b = pick(memo, store.state, threadSlice("b"), shallowEqual) as { messages: Message[] };
    expect(a.messages).toHaveLength(1);
    expect(b.messages).toHaveLength(0);
  });

  test("shallowEqual and sameData", () => {
    const x = { a: 1 };
    expect(shallowEqual({ x, n: 1 }, { x, n: 1 })).toBe(true);
    expect(shallowEqual({ x, n: 1 }, { x: { a: 1 }, n: 1 })).toBe(false);
    expect(shallowEqual({ x }, { x, n: 1 })).toBe(false);
    // Rows compare their data and leave their handlers out.
    expect(sameData({ s: x, on: () => 1 }, { s: x, on: () => 2 })).toBe(true);
    expect(sameData({ s: x, on: () => 1 }, { s: { a: 1 }, on: () => 1 })).toBe(false);
  });
});
