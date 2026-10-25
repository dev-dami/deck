import { describe, expect, test } from "vitest";
import { EMPTY_ROOM, RoomMetaStore } from "./roomMeta";
import { isoOf, toCapabilities, toEvent, toFileList, toFiles, toImage, toMessages, toRooms, toRoster, toSearchHits, toSessions } from "./rpcMap";

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

describe("what deck.v1 says of a room", () => {
  test("a session's attention, branch and archived flag, with an unknown attention dropped", () => {
    expect(
      toRooms({ sessions: [{ id: "s1", attention: "blocked", branch: "deck/s1", archived: true }, { id: "s2", attention: "panic" }, { id: "" }] }),
    ).toEqual([
      { k: "session", id: "s1", attention: "blocked", branch: "deck/s1", archived: true },
      { k: "session", id: "s2", attention: "", branch: "", archived: false },
    ]);
    const updated = toEvent({
      seq: 6,
      kind: "session_updated",
      session_updated: { id: "s1", title: "One", updated_at: at, mode: "ask", attention: "question" },
    });
    expect(updated).toMatchObject({ k: "event", event: { e: "session.updated" }, room: { k: "session", id: "s1", attention: "question" } });
    // The shared Session keeps only what a v2 server could send.
    expect(updated?.k === "event" && "attention" in updated.event.p).toBe(false);
  });

  test("a lease change is the room's alone; a marked notice is both", () => {
    expect(
      toEvent({ seq: 7, kind: "lease_changed", lease_changed: { session_id: "s1", agent: "claude-1", paths: ["a.go"], exclusive: true, released: false } }),
    ).toEqual({
      k: "room",
      seq: 7,
      room: { k: "lease", session: "s1", lease: { agent: "claude-1", paths: ["a.go"], exclusive: true }, released: false },
    });
    expect(toEvent({ seq: 8, kind: "lease_changed", lease_changed: { session_id: "s1", agent: "claude-1", paths: [] } })).toBeNull();
    expect(toEvent({ seq: 9, kind: "notice", notice: { session_id: "s1", text: "a and b both changed x.go", via: "overlap" } })).toEqual({
      k: "event",
      seq: 9,
      event: { e: "notice", p: { session_id: "s1", text: "a and b both changed x.go" } },
      room: { k: "note", session: "s1", note: { text: "a and b both changed x.go", via: "overlap" } },
    });
    expect(toEvent({ seq: 10, kind: "notice", notice: { session_id: "s1", text: "hi", via: "" } })).not.toHaveProperty("room");
  });

  test("the store keeps each room, a lease once and the newest notes", () => {
    const rooms = new RoomMetaStore();
    let changes = 0;
    rooms.subscribe(() => changes++);
    const lease = { agent: "claude-1", paths: ["a.go"], exclusive: false };
    rooms.apply({ k: "lease", session: "s1", lease, released: false });
    rooms.apply({ k: "lease", session: "s1", lease, released: false }); // replayed
    expect(rooms.get("s1").leases).toEqual([lease]);
    for (let i = 0; i < 25; i++) rooms.apply({ k: "note", session: "s1", note: { text: `n${i}`, via: "overlap" } });
    expect(rooms.get("s1").notes.map((x) => x.text)).toEqual(Array.from({ length: 20 }, (_, i) => `n${i + 5}`));
    rooms.load([{ k: "session", id: "s1", attention: "error", branch: "", archived: false }]);
    expect(rooms.get("s1")).toMatchObject({ attention: "error", leases: [lease] });
    rooms.apply({ k: "lease", session: "s1", lease, released: true });
    expect(rooms.get("s1").leases).toEqual([]);
    rooms.apply({ k: "deleted", id: "s1" });
    expect(rooms.get("s1")).toBe(EMPTY_ROOM);
    expect(changes).toBe(30);
  });

  test("the answers of the room calls", () => {
    expect(toFiles({ files: ["a.go"] })).toEqual(["a.go"]);
    expect(toFiles({ files: [1] })).toEqual([]);
    expect(toFileList({ paths: ["a.go"], truncated: true })).toEqual({ paths: ["a.go"], truncated: true });
    expect(toCapabilities({ capabilities: ["ListFiles"] })).toEqual(["ListFiles"]);
    expect(toCapabilities({})).toEqual([]);
  });

  test("an image's bytes and the messages a search found", () => {
    const data = new Uint8Array([137, 80, 78, 71]);
    expect(toImage({ data, mime: "image/png" })).toEqual({ data, mime: "image/png" });
    expect(toImage({ data, mime: "text/plain" })).toBeNull();
    expect(toImage({ data: "iVBO", mime: "image/png" })).toBeNull();
    expect(toImage({ data: new Uint8Array(), mime: "image/png" })).toBeNull();
    expect(
      toSearchHits({
        hits: [
          { session_id: "s1", message_id: "m1", agent: "claude-1", snippet: "…the lockout…", at },
          { session_id: "", message_id: "m2", agent: "", snippet: "x", at },
          "junk",
        ],
      }),
    ).toEqual([{ session_id: "s1", message_id: "m1", agent: "claude-1", snippet: "…the lockout…", at }]);
    expect(toSearchHits({})).toEqual([]);
  });
});
