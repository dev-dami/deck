import { describe, expect, test } from "bun:test";
import type { Approval, Message, Session } from "./protocol";
import { type Action, type AppState, initialState, mergeMessages, reduce } from "./state";

const session = (id: string, over: Partial<Session> = {}): Session => ({
  id,
  title: id,
  updated_at: 1,
  busy: false,
  pending: 0,
  ...over,
});

const msg = (id: string, over: Partial<Message> = {}): Message => ({
  id,
  session_id: "s1",
  role: "assistant",
  created_at: 1,
  updated_at: 1,
  finished: false,
  parts: [],
  ...over,
});

const approval = (id: string, over: Partial<Approval> = {}): Approval => ({
  id,
  session_id: "s1",
  kind: "command",
  description: "d",
  created_at: 1,
  ...over,
});

function run(actions: Action[], from: AppState = initialState): AppState {
  return actions.reduce(reduce, from);
}

describe("sessions", () => {
  test("loaded sessions are sorted newest first", () => {
    const s = run([
      {
        type: "sessions.loaded",
        sessions: [session("a", { updated_at: 1 }), session("b", { updated_at: 5 })],
      },
    ]);
    expect(s.sessions.map((x) => x.id)).toEqual(["b", "a"]);
    expect(s.sessionsLoaded).toBe(true);
  });

  test("session events upsert and re-sort", () => {
    const s = run([
      { type: "sessions.loaded", sessions: [session("a", { updated_at: 5 })] },
      { type: "event", event: { e: "session.created", p: session("b", { updated_at: 9 }) } },
      {
        type: "event",
        event: { e: "session.updated", p: session("a", { updated_at: 20, title: "new" }) },
      },
    ]);
    expect(s.sessions.map((x) => [x.id, x.title])).toEqual([
      ["a", "new"],
      ["b", "b"],
    ]);
  });

  test("busy event patches only that session", () => {
    const s = run([
      { type: "sessions.loaded", sessions: [session("a"), session("b")] },
      { type: "event", event: { e: "busy", p: { session_id: "b", busy: true } } },
    ]);
    expect(s.sessions.find((x) => x.id === "b")?.busy).toBe(true);
    expect(s.sessions.find((x) => x.id === "a")?.busy).toBe(false);
  });

  test("busy for an unknown session is ignored", () => {
    const before = run([{ type: "sessions.loaded", sessions: [session("a")] }]);
    const after = reduce(before, {
      type: "event",
      event: { e: "busy", p: { session_id: "zz", busy: true } },
    });
    expect(after.sessions).toEqual(before.sessions);
  });

  test("deleting a session drops its data and approvals", () => {
    const s = run([
      { type: "sessions.loaded", sessions: [session("s1")] },
      { type: "messages.loaded", sessionId: "s1", messages: [msg("m1")] },
      { type: "event", event: { e: "approval.requested", p: approval("p1") } },
      { type: "event", event: { e: "session.deleted", p: { id: "s1" } } },
    ]);
    expect(s.sessions).toEqual([]);
    expect(s.messages["s1"]).toBeUndefined();
    expect(s.approvals).toEqual([]);
  });
});

describe("messages", () => {
  test("created then updated keeps one row with the newest content", () => {
    const s = run([
      {
        type: "event",
        event: { e: "message.created", p: msg("m1", { parts: [{ type: "text", text: "he" }] }) },
      },
      {
        type: "event",
        event: {
          e: "message.updated",
          p: msg("m1", { updated_at: 2, parts: [{ type: "text", text: "hello" }], finished: true }),
        },
      },
    ]);
    expect(s.messages["s1"]).toHaveLength(1);
    expect(s.messages["s1"]?.[0]?.parts[0]?.text).toBe("hello");
  });

  test("a stale snapshot never rolls back a streamed message", () => {
    const live = msg("m1", { updated_at: 5, parts: [{ type: "text", text: "newer" }] });
    const stale = msg("m1", { updated_at: 2, parts: [{ type: "text", text: "older" }] });
    const merged = mergeMessages([live], [stale]);
    expect(merged[0]?.parts[0]?.text).toBe("newer");
  });

  test("messages stay ordered by creation time", () => {
    const s = run([
      {
        type: "messages.loaded",
        sessionId: "s1",
        messages: [msg("b", { created_at: 2 }), msg("a", { created_at: 1 })],
      },
      { type: "event", event: { e: "message.created", p: msg("c", { created_at: 3 }) } },
    ]);
    expect(s.messages["s1"]?.map((m) => m.id)).toEqual(["a", "b", "c"]);
    expect(s.messagesLoaded["s1"]).toBe(true);
  });

  test("message.deleted removes by id, with or without a session id", () => {
    const base = run([
      { type: "messages.loaded", sessionId: "s1", messages: [msg("a"), msg("b")] },
    ]);
    const one = reduce(base, {
      type: "event",
      event: { e: "message.deleted", p: { id: "a", session_id: "s1" } },
    });
    expect(one.messages["s1"]?.map((m) => m.id)).toEqual(["b"]);
    const bare = reduce(base, { type: "event", event: { e: "message.deleted", p: { id: "b" } } });
    expect(bare.messages["s1"]?.map((m) => m.id)).toEqual(["a"]);
  });
});

describe("approvals", () => {
  test("requested approvals queue oldest first and drive the pending badge", () => {
    const s = run([
      { type: "sessions.loaded", sessions: [session("s1")] },
      { type: "event", event: { e: "approval.requested", p: approval("p2", { created_at: 5 }) } },
      { type: "event", event: { e: "approval.requested", p: approval("p1", { created_at: 2 }) } },
    ]);
    expect(s.approvals.map((a) => a.id)).toEqual(["p1", "p2"]);
    expect(s.sessions[0]?.pending).toBe(2);
  });

  test("duplicate requests are ignored", () => {
    const s = run([
      { type: "event", event: { e: "approval.requested", p: approval("p1") } },
      { type: "event", event: { e: "approval.requested", p: approval("p1") } },
    ]);
    expect(s.approvals).toHaveLength(1);
  });

  test("resolved removes the approval and the badge", () => {
    const s = run([
      { type: "sessions.loaded", sessions: [session("s1")] },
      { type: "event", event: { e: "approval.requested", p: approval("p1") } },
      { type: "event", event: { e: "approval.resolved", p: { id: "p1" } } },
    ]);
    expect(s.approvals).toEqual([]);
    expect(s.sessions[0]?.pending).toBe(0);
  });

  test("a sessions snapshot recomputes pending from known approvals", () => {
    const s = run([
      { type: "approvals.loaded", approvals: [approval("p1")] },
      { type: "sessions.loaded", sessions: [session("s1", { pending: 0 })] },
    ]);
    expect(s.sessions[0]?.pending).toBe(1);
  });
});

describe("misc state", () => {
  test("queue.changed replaces the session's queue", () => {
    const items = [{ id: "q1", agent: "codex", text: "next", at: 5 }];
    const s = run([
      { type: "event", event: { e: "queue.changed", p: { session_id: "s1", items } } },
    ]);
    expect(s.queue["s1"]).toEqual(items);
  });

  test("mode.changed updates the mode", () => {
    expect(run([{ type: "event", event: { e: "mode.changed", p: { mode: "plan" } } }]).mode).toBe(
      "plan",
    );
  });

  test("notices are capped and dismissable", () => {
    let s = initialState;
    for (let i = 0; i < 6; i++) s = reduce(s, { type: "notice.add", text: `n${i}`, kind: "info" });
    expect(s.notices).toHaveLength(4);
    const first = s.notices[0];
    expect(first?.text).toBe("n2");
    if (first) s = reduce(s, { type: "notice.dismiss", id: first.id });
    expect(s.notices[0]?.text).toBe("n3");
  });

  test("a notice event surfaces as a toast", () => {
    const s = run([{ type: "event", event: { e: "notice", p: { text: "build done" } } }]);
    expect(s.notices[0]?.text).toBe("build done");
  });

  test("reset returns to a clean state at the given auth phase", () => {
    const dirty = run([
      { type: "sessions.loaded", sessions: [session("a")] },
      { type: "mode.loaded", mode: "ask" },
    ]);
    const s = reduce(dirty, { type: "reset", auth: "unpaired", message: "bye" });
    expect(s.sessions).toEqual([]);
    expect(s.mode).toBeNull();
    expect(s.authMessage).toBe("bye");
  });

  test("unchanged link returns the same state object", () => {
    const s = reduce(initialState, { type: "link", link: "connecting" });
    expect(s).toBe(initialState);
  });
});

describe("message edits", () => {
  const userMsg = (text: string, over: Partial<Message> = {}) =>
    msg("m1", {
      role: "user",
      editable: true,
      finished: true,
      parts: [{ type: "text", text }],
      ...over,
    });

  test("message.updated replaces the text in place", () => {
    let s = reduce(initialState, {
      type: "messages.loaded",
      sessionId: "s1",
      messages: [userMsg("old", { updated_at: 1 }), msg("m2", { created_at: 2 })],
    });
    s = reduce(s, {
      type: "event",
      event: { e: "message.updated", p: userMsg("new", { updated_at: 5 }) },
    });
    expect(s.messages["s1"]?.map((m) => m.id)).toEqual(["m1", "m2"]);
    expect(s.messages["s1"]?.[0]?.parts[0]?.text).toBe("new");
    expect(s.messages["s1"]?.[0]?.editable).toBe(true);
  });

  test("the update can also withdraw editability", () => {
    let s = reduce(initialState, {
      type: "event",
      event: { e: "message.created", p: userMsg("old", { updated_at: 1 }) },
    });
    s = reduce(s, {
      type: "event",
      event: { e: "message.updated", p: userMsg("old", { updated_at: 2, editable: false }) },
    });
    expect(s.messages["s1"]?.[0]?.editable).toBe(false);
  });
});

describe("push state", () => {
  test("updates patch the push view and reset clears it", () => {
    let s = reduce(initialState, { type: "push.update", patch: { checked: true, enabled: true } });
    expect(s.push).toMatchObject({ checked: true, enabled: true, approvals: true });
    s = reduce(s, { type: "reset", auth: "unpaired", message: null });
    expect(s.push).toEqual(initialState.push);
  });
});
