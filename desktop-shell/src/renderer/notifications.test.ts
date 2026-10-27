import type { Message } from "@web/protocol";
import { describe, expect, test } from "vitest";
import type { RoomStatus } from "./attention";
import type { BoardTask } from "./crew";
import {
  addEntry,
  badgeCount,
  Burst,
  decide,
  doneTasks,
  markRead,
  markRoomRead,
  mentionsUser,
  newMentions,
  type NotifyEvent,
  type NotifySettings,
  parseEntries,
  phrase,
  roomEvent,
  summarize,
  unreadCount,
} from "./notifications";

const st = (state: RoomStatus["state"], label = "", need?: RoomStatus["need"]): RoomStatus => ({ state, label, at: 1, ...(need ? { need } : {}) });

const task = (id: string, status: string): BoardTask => ({
  id,
  title: `Task ${id}`,
  status,
  owner: "",
  deps: [],
  brief: "",
  result: "",
  error: "",
  attempts: 0,
  tries: 0,
  branch: "",
  updatedAt: 5,
  race: false,
  entry: false,
  stats: "",
  files: [],
  reviewer: "",
  reviews: 0,
});

const msg = (id: string, text: string, extra: Partial<Message> = {}): Message => ({
  id,
  session_id: "s",
  role: "assistant",
  agent: "claude-1",
  created_at: "2026-10-25T10:00:00Z",
  updated_at: "2026-10-25T10:00:00Z",
  finished: true,
  parts: [{ type: "text", text }],
  ...extra,
});

const ev = (room: string, kind: NotifyEvent["kind"] = "finished", at = 1, extra: Partial<NotifyEvent> = {}): NotifyEvent => ({
  kind,
  source: "folder:/p",
  projectName: "p",
  room,
  roomTitle: `Room ${room}`,
  text: kind === "finished" ? "Finished" : "Approve command",
  at,
  ...extra,
});

const on: NotifySettings = {
  enabled: true,
  kinds: { approval: true, error: true, mention: true, task: true, finished: true },
  sound: true,
  unfocusedOnly: true,
};

describe("what is news", () => {
  test("a room entering needs-you, by what it needs; a finish once; never a first status or a queued room", () => {
    expect(roomEvent(st("working"), st("needs-you", "Approve command", "approval"), false)).toBe("approval");
    expect(roomEvent(st("working"), st("needs-you", "claude-1 stopped with an error", "error"), false)).toBe("error");
    expect(roomEvent(st("idle"), st("needs-you", "Answer"), false)).toBe("approval");
    expect(roomEvent(st("working"), st("done", "Finished"), false)).toBe("finished");
    expect(roomEvent(st("done"), st("review", "Changes to review"), false)).toBeNull();
    expect(roomEvent(st("working"), st("idle"), false)).toBeNull();
    expect(roomEvent(undefined, st("needs-you", "Approve command", "approval"), false)).toBeNull();
    expect(roomEvent(st("working"), st("needs-you", "Approve command", "approval"), true)).toBeNull();
  });

  test("a new request in a room that already waits is news; the same one is not", () => {
    const a = st("needs-you", "Approve command", "approval");
    expect(roomEvent(a, st("needs-you", "Approve command", "approval"), false)).toBeNull();
    expect(roomEvent(a, st("needs-you", "Task blocked: Map", "error"), false)).toBe("error");
  });

  test("tasks newly done, with no news on the first board", () => {
    expect(doneTasks(undefined, [task("a", "done")])).toEqual([]);
    const done = doneTasks([task("a", "running"), task("b", "done")], [task("a", "done"), task("b", "done"), task("c", "done")]);
    expect(done.map((t) => t.id)).toEqual(["a"]);
  });

  test("a mention of the person counts where the engine reads one", () => {
    const names = ["user", "dami"];
    expect(mentionsUser("@user can you check the key?", names)).toBe(true);
    expect(mentionsUser("Done. $user, over to you.", names)).toBe(true);
    expect(mentionsUser("Ask (@Dami) first", names)).toBe(true);
    expect(mentionsUser("mail user@example.com", names)).toBe(false);
    expect(mentionsUser("see @user/notes.md", names)).toBe(false);
    expect(mentionsUser("@users are many", names)).toBe(false);
    expect(mentionsUser("@user-2 is a member", names)).toBe(false);
    expect(mentionsUser("@user.", names)).toBe(true);
    expect(mentionsUser("@user", [""])).toBe(false);
  });

  test("mentions in finished agent messages not seen before; the first look is the baseline", () => {
    const first = newMentions(null, [msg("m1", "@user hi")], ["user"]);
    expect(first.mentions).toEqual([]);
    const list = [msg("m1", "@user hi"), msg("m2", "streaming @user", { finished: false }), msg("m3", "@user", { role: "user" }), msg("m4", "and @user, look")];
    const next = newMentions(first.seen, list, ["user"]);
    expect(next.mentions.map((m) => m.message.id)).toEqual(["m4"]);
    // The streaming one counts once it finishes.
    const later = newMentions(next.seen, [msg("m2", "streaming @user")], ["user"]);
    expect(later.mentions.map((m) => m.message.id)).toEqual(["m2"]);
    expect(newMentions(later.seen, [msg("m2", "streaming @user")], ["user"]).mentions).toEqual([]);
  });
});

describe("whether and how to tell", () => {
  test("off, or the kind off: nothing", () => {
    expect(decide("approval", { ...on, enabled: false }, { focused: false, viewing: false })).toEqual({ record: false, native: false, read: false });
    expect(decide("finished", { ...on, kinds: { ...on.kinds, finished: false } }, { focused: false, viewing: false }).record).toBe(false);
  });

  test("in the background: a native notification, unread", () => {
    expect(decide("finished", on, { focused: false, viewing: true })).toEqual({ record: true, native: true, read: false });
  });

  test("in front: the room on screen is read and silent; another room pops only when the person asked for it", () => {
    expect(decide("approval", on, { focused: true, viewing: true })).toEqual({ record: true, native: false, read: true });
    expect(decide("approval", on, { focused: true, viewing: false })).toEqual({ record: true, native: false, read: false });
    expect(decide("approval", { ...on, unfocusedOnly: false }, { focused: true, viewing: false })).toEqual({ record: true, native: true, read: false });
  });
});

describe("bursts", () => {
  test("one event is itself", () => {
    expect(summarize([ev("a", "approval")])).toEqual({ title: "p · Room a", body: "Approve command", source: "folder:/p", room: "a" });
    expect(summarize([])).toBeNull();
  });

  test("four finishes at once are one notification", () => {
    const n = summarize([ev("a", "finished", 1), ev("b", "finished", 2), ev("c", "finished", 3), ev("d", "finished", 4), ev("e", "finished", 5)]);
    expect(n?.title).toBe("5 rooms finished");
    expect(n?.body.split("\n")).toEqual(["Room e: Finished", "Room d: Finished", "Room c: Finished", "Room b: Finished", "and 1 more"]);
    expect(n?.room).toBe("e");
  });

  test("a mixed burst leads with the most urgent, and a click opens it", () => {
    const n = summarize([ev("a", "finished", 9), ev("b", "approval", 1), ev("c", "finished", 8)]);
    expect(n?.title).toBe("3 updates · 1 room needs your answer");
    expect(n?.body.split("\n")[0]).toBe("Room b: Approve command");
    expect(n?.room).toBe("b");
    expect(phrase("task", 2)).toBe("2 tasks done");
    expect(phrase("mention", 1)).toBe("1 mention of you");
  });

  test("events within the window flush together; the next window starts afresh", () => {
    const timers: (() => void)[] = [];
    const out: string[][] = [];
    const b = new Burst(1000, (es) => out.push(es.map((e) => e.room)), { set: (f) => timers.push(f), clear: () => {} });
    b.push(ev("a"));
    b.push(ev("b"));
    expect(timers).toHaveLength(1);
    timers[0]?.();
    b.push(ev("c"));
    expect(timers).toHaveLength(2);
    timers[1]?.();
    expect(out).toEqual([["a", "b"], ["c"]]);
    b.push(ev("d"));
    b.dispose();
    expect(out).toHaveLength(2);
  });
});

describe("the centre", () => {
  test("a room's repeats fold into one unread entry, newest on top", () => {
    let list = addEntry([], ev("a", "finished", 1), false);
    list = addEntry(list, ev("b", "approval", 2), false);
    list = addEntry(list, ev("a", "finished", 3), false);
    expect(list.map((e) => [e.room, e.count, e.at])).toEqual([
      ["a", 2, 3],
      ["b", 1, 2],
    ]);
    // Read, the next one starts a new entry.
    list = markRead(list);
    list = addEntry(list, ev("a", "finished", 4), false);
    expect(list.map((e) => [e.room, e.count, e.read])).toEqual([
      ["a", 1, false],
      ["a", 2, true],
      ["b", 1, true],
    ]);
  });

  test("an entry the person saw arrives read and folds into nothing", () => {
    const list = addEntry(addEntry([], ev("a"), false), ev("a"), true);
    expect(list.map((e) => [e.count, e.read])).toEqual([
      [1, true],
      [1, false],
    ]);
    expect(new Set(list.map((e) => e.id)).size).toBe(2);
  });

  test("the list keeps its newest entries", () => {
    let list = addEntry([], ev("r0"), false, 3);
    for (let i = 1; i < 6; i++) list = addEntry(list, ev(`r${i}`), false, 3);
    expect(list.map((e) => e.room)).toEqual(["r5", "r4", "r3"]);
  });

  test("unread counts, mark read by id or by room, and nothing changes when nothing is unread", () => {
    let list = addEntry(addEntry(addEntry([], ev("a"), false), ev("b", "approval"), false), ev("c"), false);
    expect(unreadCount(list)).toBe(3);
    list = markRead(list, [list[0]!.id]);
    expect(unreadCount(list)).toBe(2);
    list = markRoomRead(list, "folder:/p", "b");
    expect(unreadCount(list)).toBe(1);
    expect(markRoomRead(list, "folder:/other", "a")).toBe(list);
    const all = markRead(list);
    expect(unreadCount(all)).toBe(0);
    expect(markRead(all)).toBe(all);
  });

  test("the badge counts each room once, waiting or unread", () => {
    const list = addEntry(addEntry(addEntry([], ev("a"), false), ev("b"), false), ev("c"), true);
    expect(badgeCount([], list)).toBe(2);
    expect(badgeCount([{ source: "folder:/p", room: "a" }, { source: "folder:/p", room: "z" }], list)).toBe(3);
    expect(badgeCount([], [])).toBe(0);
  });

  test("only well-formed entries load", () => {
    const good = addEntry([], ev("a"), false);
    expect(parseEntries(JSON.parse(JSON.stringify(good)))).toEqual(good);
    expect(parseEntries([{ ...good[0], kind: "spam" }, { ...good[0], at: "x" }, null, 3])).toEqual([]);
    expect(parseEntries("nope")).toEqual([]);
  });
});
