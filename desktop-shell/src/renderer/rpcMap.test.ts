import { describe, expect, test } from "vitest";
import { isoOf, toEvent, toMessages, toRoster, toSessions } from "./rpcMap";

const at = Date.parse("2026-10-19T09:00:00Z");

describe("deck.v1 to the reducer's entities", () => {
  test("times become RFC 3339 and empty strings become absent", () => {
    expect(isoOf(at)).toBe("2026-10-19T09:00:00.000Z");
    expect(
      toSessions({
        sessions: [{ id: "s1", title: "", updated_at: at, busy: true, pending: 2, mode: "ask", last_text: "" }],
      }),
    ).toEqual([{ id: "s1", title: "", updated_at: "2026-10-19T09:00:00.000Z", busy: true, pending: 2, mode: "ask" }]);
  });

  test("a message keeps its parts and drops empty fields", () => {
    const [m] = toMessages({
      messages: [
        {
          id: "m1",
          session_id: "s1",
          role: "assistant",
          agent: "codex-2",
          created_at: at,
          updated_at: at,
          finished: false,
          editable: false,
          edited: false,
          parts: [
            { type: "tool_call", text: "", id: "t1", name: "bash", summary: "go test", tool_call_id: "", is_error: false, output: "", diff: "", image: null },
            { type: "image", text: "", id: "", name: "", summary: "", tool_call_id: "", is_error: false, output: "", diff: "", image: { name: "a.png", mime: "image/png" } },
          ],
        },
      ],
    });
    expect(m).toEqual({
      id: "m1",
      session_id: "s1",
      role: "assistant",
      agent: "codex-2",
      created_at: "2026-10-19T09:00:00.000Z",
      updated_at: "2026-10-19T09:00:00.000Z",
      finished: false,
      parts: [
        { type: "tool_call", id: "t1", name: "bash", summary: "go test" },
        { type: "image", image: { name: "a.png", mime: "image/png" } },
      ],
    });
  });

  test("an entity that fails the shared guard is dropped", () => {
    expect(toMessages({ messages: [{ id: "m1", session_id: "s1", role: "robot", parts: [] }] })).toEqual([]);
    expect(toRoster({ agents: [{ id: "claude-1", name: "", kind: "claude", busy: false, model: "", effort: "" }] })).toEqual([
      { id: "claude-1", name: "claude-1", kind: "claude", busy: false },
    ]);
  });
});

describe("deck.v1 events", () => {
  test("Ready opens the stream", () => {
    expect(toEvent({ seq: 0, session_id: "", kind: "ready", ready: { epoch: "e1", seq: 7, resync: true } })).toEqual({
      k: "ready",
      ready: { epoch: "e1", seq: 7, resync: true },
    });
  });

  test("each kind maps to the v2 event name and payload", () => {
    expect(toEvent({ seq: 3, kind: "busy", busy: { session_id: "s1", busy: true } })).toEqual({
      k: "event",
      seq: 3,
      event: { e: "busy", p: { session_id: "s1", busy: true } },
    });
    expect(toEvent({ seq: 4, kind: "roster_changed", roster_changed: { session_id: "s1" } })).toMatchObject({
      event: { e: "roster.changed" },
    });
    const created = toEvent({
      seq: 5,
      kind: "message_created",
      message_created: { id: "m", session_id: "s1", role: "user", created_at: at, updated_at: at, finished: true, parts: [] },
    });
    expect(created).toMatchObject({ seq: 5, event: { e: "message.created", p: { id: "m", parts: [] } } });
  });

  test("an unknown kind or a bad payload is null", () => {
    expect(toEvent({ seq: 1, kind: "device_paired", device_paired: {} })).toBeNull();
    expect(toEvent({ seq: 1, kind: "mode_changed", mode_changed: { session_id: "s1", mode: "fast" } })).toBeNull();
    expect(toEvent("nope")).toBeNull();
  });
});
