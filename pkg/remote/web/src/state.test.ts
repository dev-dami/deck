import { describe, expect, test } from "bun:test";
import type { Approval, Message, Session } from "./protocol";
import {
  type AppState,
  initialState,
  mergeMessages,
  reduce,
  sessionMode,
  sortSessions,
} from "./state";

const s1: Session = {
  id: "s1",
  title: "One",
  updated_at: "2026-10-12T09:00:00Z",
  busy: false,
  pending: 0,
  mode: "ask",
};
const s2: Session = { ...s1, id: "s2", title: "Two", updated_at: "2026-10-12T10:00:00Z" };

function msg(id: string, updated: string, text: string, created = updated): Message {
  return {
    id,
    session_id: "s1",
    role: "user",
    created_at: created,
    updated_at: updated,
    finished: true,
    parts: [{ type: "text", text }],
  };
}

const apr: Approval = {
  id: "a1",
  session_id: "s1",
  agent: "claude-1",
  kind: "command",
  description: "Run",
  command: "ls",
  created_at: "2026-10-12T09:30:00Z",
};

function withSessions(state: AppState = initialState): AppState {
  return reduce(state, { type: "sessions.loaded", sessions: [s1, s2] });
}

describe("sessions", () => {
  test("sorted newest first and marked loaded", () => {
    const st = withSessions();
    expect(st.sessions.map((s) => s.id)).toEqual(["s2", "s1"]);
    expect(st.sessionsLoaded).toBe(true);
    expect(sortSessions([s1, s2])[0]?.id).toBe("s2");
  });

  test("created, updated, deleted", () => {
    let st = withSessions();
    st = reduce(st, {
      type: "event",
      event: { e: "session.created", p: { ...s1, id: "s3", updated_at: "2026-10-12T11:00:00Z" } },
    });
    expect(st.sessions.map((s) => s.id)).toEqual(["s3", "s2", "s1"]);
    st = reduce(st, {
      type: "event",
      event: { e: "session.updated", p: { ...s1, title: "Renamed" } },
    });
    expect(st.sessions.find((s) => s.id === "s1")?.title).toBe("Renamed");
    st = reduce(st, {
      type: "messages.loaded",
      sessionId: "s1",
      messages: [msg("m1", "2026-10-12T09:00:00Z", "x")],
      more: false,
      cursor: null,
      latest: true,
    });
    st = reduce(st, { type: "event", event: { e: "session.deleted", p: s1 } });
    expect(st.sessions.map((s) => s.id)).toEqual(["s3", "s2"]);
    expect(st.messages["s1"]).toBeUndefined();
    expect(st.pages["s1"]).toBeUndefined();
  });

  test("busy and mode are per session", () => {
    let st = withSessions();
    st = reduce(st, { type: "event", event: { e: "busy", p: { session_id: "s1", busy: true } } });
    st = reduce(st, {
      type: "event",
      event: { e: "mode.changed", p: { session_id: "s2", mode: "yolo" } },
    });
    expect(st.sessions.find((s) => s.id === "s1")?.busy).toBe(true);
    expect(st.sessions.find((s) => s.id === "s2")?.mode).toBe("yolo");
    expect(st.sessions.find((s) => s.id === "s1")?.mode).toBe("ask");
    st = reduce(st, { type: "mode.loaded", sessionId: "s1", mode: "plan" });
    expect(sessionMode(st.sessions.find((s) => s.id === "s1"))).toBe("plan");
    expect(sessionMode({ ...s1, mode: "weird" })).toBeNull();
  });
});

describe("messages", () => {
  test("merge by id, newer updated_at wins, ordered by created_at", () => {
    const a = msg("m1", "2026-10-12T09:00:00Z", "old");
    const b = msg("m1", "2026-10-12T09:00:05Z", "new");
    const c = msg("m0", "2026-10-12T08:00:00Z", "first");
    expect(mergeMessages([b], [a]).map((m) => m.parts[0]?.text)).toEqual(["new"]);
    expect(mergeMessages([a, c], [b]).map((m) => m.id)).toEqual(["m0", "m1"]);
  });

  test("the latest page sets the cursor; older pages move it back", () => {
    let st = reduce(initialState, {
      type: "messages.loaded",
      sessionId: "s1",
      messages: [msg("m3", "2026-10-12T09:03:00Z", "c")],
      more: true,
      cursor: "cur_m3",
      latest: true,
    });
    expect(st.pages["s1"]).toEqual({ more: true, cursor: "cur_m3" });
    st = reduce(st, {
      type: "messages.loaded",
      sessionId: "s1",
      messages: [msg("m2", "2026-10-12T09:02:00Z", "b")],
      more: true,
      cursor: "cur_m2",
      latest: false,
    });
    expect(st.pages["s1"]).toEqual({ more: true, cursor: "cur_m2" });
    expect(st.messages["s1"]?.map((m) => m.id)).toEqual(["m2", "m3"]);
    st = reduce(st, {
      type: "messages.loaded",
      sessionId: "s1",
      messages: [msg("m1", "2026-10-12T09:01:00Z", "a")],
      more: false,
      cursor: null,
      latest: false,
    });
    expect(st.pages["s1"]).toEqual({ more: false, cursor: "cur_m2" });
    // A refetch of the latest page after a reconnect replaces the cursor.
    st = reduce(st, {
      type: "messages.loaded",
      sessionId: "s1",
      messages: [],
      more: true,
      cursor: "cur_new",
      latest: true,
    });
    expect(st.pages["s1"]).toEqual({ more: true, cursor: "cur_new" });
    expect(st.messages["s1"]).toHaveLength(3);
  });

  test("created, updated and deleted events", () => {
    let st = reduce(initialState, {
      type: "event",
      event: { e: "message.created", p: msg("m1", "2026-10-12T09:00:00Z", "a") },
    });
    st = reduce(st, {
      type: "event",
      event: { e: "message.updated", p: msg("m1", "2026-10-12T09:00:01Z", "b") },
    });
    expect(st.messages["s1"]?.[0]?.parts[0]?.text).toBe("b");
    // A stale update never rolls a message back.
    st = reduce(st, {
      type: "event",
      event: { e: "message.updated", p: msg("m1", "2026-10-12T08:59:00Z", "stale") },
    });
    expect(st.messages["s1"]?.[0]?.parts[0]?.text).toBe("b");
    st = reduce(st, {
      type: "event",
      event: { e: "message.deleted", p: { id: "m1", session_id: "s1" } },
    });
    expect(st.messages["s1"]).toEqual([]);
  });
});

describe("approvals", () => {
  test("requested, resolved, loaded keep the session's pending count", () => {
    let st = withSessions();
    st = reduce(st, { type: "event", event: { e: "approval.requested", p: apr } });
    st = reduce(st, { type: "event", event: { e: "approval.requested", p: apr } });
    expect(st.approvals).toHaveLength(1);
    expect(st.sessions.find((s) => s.id === "s1")?.pending).toBe(1);
    st = reduce(st, {
      type: "event",
      event: { e: "approval.resolved", p: { id: "a1", session_id: "s1", granted: true } },
    });
    expect(st.approvals).toHaveLength(0);
    expect(st.sessions.find((s) => s.id === "s1")?.pending).toBe(0);
    st = reduce(st, { type: "approvals.loaded", approvals: [{ ...apr, id: "a2" }, apr] });
    expect(st.approvals.map((a) => a.id)).toEqual(["a1", "a2"]);
    st = reduce(st, { type: "approval.dropped", id: "a1" });
    expect(st.approvals.map((a) => a.id)).toEqual(["a2"]);
  });
});

describe("queue, roster, devices, theme, toasts", () => {
  test("queue.changed replaces the session's queue", () => {
    const items = [{ id: "q1", agent: "codex-1", text: "x", at: "2026-10-12T09:32:00Z" }];
    const st = reduce(initialState, {
      type: "event",
      event: { e: "queue.changed", p: { session_id: "s1", items } },
    });
    expect(st.queue["s1"]).toEqual(items);
  });

  test("device.paired toasts and updates a loaded device list; device.revoked removes", () => {
    const dev = {
      id: "dev_93c0",
      name: "iPhone",
      created_at: "2026-10-12T10:02:17Z",
      expires_at: "2027-01-10T10:02:17Z",
      allow_yolo: false,
    };
    let st = reduce(initialState, { type: "event", event: { e: "device.paired", p: dev } });
    expect(st.devices).toBeNull();
    expect(st.toasts[0]?.text).toContain("iPhone");
    st = reduce(st, { type: "devices.loaded", devices: [] });
    st = reduce(st, { type: "event", event: { e: "device.paired", p: dev } });
    expect(st.devices?.map((d) => d.id)).toEqual(["dev_93c0"]);
    st = reduce(st, { type: "event", event: { e: "device.revoked", p: { id: "dev_93c0" } } });
    expect(st.devices).toEqual([]);
  });

  test("theme.changed replaces the theme; welcome sets it", () => {
    const theme = { palette: { bg_base: "#000000" }, kinds: {} };
    const st = reduce(initialState, { type: "event", event: { e: "theme.changed", p: theme } });
    expect(st.theme).toEqual(theme);
  });

  test("notices become toasts, capped at four", () => {
    let st = initialState;
    for (let i = 0; i < 6; i++) {
      st = reduce(st, {
        type: "event",
        event: { e: "notice", p: { session_id: "s1", text: `n${i}` } },
      });
    }
    expect(st.toasts.map((t) => t.text)).toEqual(["n2", "n3", "n4", "n5"]);
    st = reduce(st, { type: "toast.dismiss", id: st.toasts[0]?.id ?? 0 });
    expect(st.toasts).toHaveLength(3);
  });

  test("reset keeps only the auth phase and message", () => {
    const st = reduce(withSessions(), { type: "reset", auth: "unpaired", message: "bye" });
    expect(st).toEqual({ ...initialState, auth: "unpaired", authMessage: "bye" });
  });
});
