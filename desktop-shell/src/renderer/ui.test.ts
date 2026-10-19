import type { Session } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { initialUi, reduceUi, type UiState } from "./ui";

const s = (id: string, last_text = ""): Session => ({
  id,
  title: id,
  updated_at: "2026-10-19T09:00:00Z",
  busy: false,
  pending: 0,
  mode: "ask",
  last_text,
});

function run(...actions: Parameters<typeof reduceUi>[1][]): UiState {
  return actions.reduce(reduceUi, initialUi);
}

describe("unread", () => {
  test("the first list marks nothing", () => {
    expect(run({ type: "sessions", sessions: [s("a", "hi"), s("b", "yo")] }).unread).toEqual({});
  });

  test("new text in a background session marks it; the open one stays read", () => {
    const st = run(
      { type: "sessions", sessions: [s("a", "hi"), s("b", "yo")] },
      { type: "open", id: "a" },
      { type: "sessions", sessions: [s("a", "hi again"), s("b", "yo again")] },
    );
    expect(st.unread).toEqual({ b: true });
  });

  test("it stays unread until the person opens it", () => {
    let st = run(
      { type: "sessions", sessions: [s("a", "1")] },
      { type: "sessions", sessions: [s("a", "2")] },
      { type: "sessions", sessions: [s("a", "2")] },
    );
    expect(st.unread).toEqual({ a: true });
    st = reduceUi(st, { type: "open", id: "a" });
    expect(st.unread).toEqual({});
  });

  test("a deleted session drops its mark and closes when open", () => {
    const st = run(
      { type: "sessions", sessions: [s("a", "1"), s("b")] },
      { type: "open", id: "b" },
      { type: "sessions", sessions: [s("a", "2"), s("b")] },
      { type: "sessions", sessions: [s("a", "2")] },
    );
    expect(st.openId).toBeNull();
    expect(st.unread).toEqual({ a: true });
    expect(reduceUi(st, { type: "sessions", sessions: [] }).unread).toEqual({});
  });
});

describe("activity in the background", () => {
  test("a new message list for a session that is not open marks it", () => {
    const a1: unknown[] = [];
    const b1: unknown[] = [];
    let st = run({ type: "open", id: "a" }, { type: "messages", messages: { a: a1 } });
    expect(st.unread).toEqual({});
    // The open session changes: no mark. Another one appears: a mark.
    st = reduceUi(st, { type: "messages", messages: { a: [...a1], b: b1 } });
    expect(st.unread).toEqual({ b: true });
    // Switching away from a leaves it read while its list stays the same.
    st = reduceUi(st, { type: "open", id: "b" });
    st = reduceUi(st, { type: "messages", messages: st.lists });
    expect(st.unread).toEqual({});
  });
});

describe("panel and palette", () => {
  test("a panel toggles and another one replaces it", () => {
    expect(run({ type: "panel", panel: "changes" }).panel).toBe("changes");
    expect(run({ type: "panel", panel: "changes" }, { type: "panel", panel: "changes" }).panel).toBeNull();
    expect(run({ type: "panel", panel: "changes" }, { type: "panel", panel: "queue" }).panel).toBe("queue");
    expect(run({ type: "panel", panel: "changes" }, { type: "panel", panel: null }).panel).toBeNull();
  });

  test("opening a session closes the palette", () => {
    expect(run({ type: "palette", open: true }, { type: "open", id: "a" }).palette).toBe(false);
  });
});

describe("settings", () => {
  test("settings open in place of the chat and a thread closes them", () => {
    const st = run({ type: "settings", section: "appearance" });
    expect(st.settings).toBe("appearance");
    expect(reduceUi(st, { type: "open", id: "a" }).settings).toBeNull();
  });
});
