import type { Approval, Message, Session } from "@web/protocol";
import { describe, expect, test } from "vitest";
import {
  actedAt,
  byAttention,
  type InboxProject,
  inboxItems,
  needsYouRooms,
  nextNeedsYou,
  parseOverlap,
  type RoomInput,
  roomStatus,
  shouldNotify,
} from "./attention";
import type { AgentNode, BoardTask, TeamContract } from "./crew";

const T0 = Date.parse("2026-10-25T10:00:00Z");
const iso = (ms: number) => new Date(ms).toISOString();

const session = (id: string, extra: Partial<Session> = {}): Session => ({
  id,
  title: id,
  updated_at: iso(T0),
  busy: false,
  pending: 0,
  mode: "ask",
  ...extra,
});

const approval = (id: string, sid: string, at: number, extra: Partial<Approval> = {}): Approval => ({
  id,
  session_id: sid,
  agent: "claude-1",
  kind: "command",
  description: "Run a command",
  command: "go test ./...",
  created_at: iso(at),
  ...extra,
});

const task = (id: string, status: string, updatedAt: number): BoardTask => ({
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
  updatedAt,
  race: false,
  entry: false,
  stats: "",
  files: [],
  reviewer: "",
  reviews: 0,
});

const agent = (name: string, status: string, since = 0): AgentNode => ({
  name,
  kind: name.split("-")[0] ?? "claude",
  model: "",
  effort: "",
  status,
  contextTokens: 0,
  contextWindow: 0,
  costUsd: 0,
  pending: 0,
  since,
  depth: 0,
  parent: "",
  branch: "",
  leases: "",
  profile: "",
  role: "",
});

const room = (extra: Partial<RoomInput> = {}): RoomInput => ({
  session: session("a"),
  approvals: [],
  tasks: [],
  agents: [],
  notices: [],
  actedAt: 0,
  unread: false,
  open: false,
  changed: 0,
  attention: null,
  ...extra,
});

const OVERLAP = "codex-2 and claude-1 both changed internal/auth/auth.go in the last few minutes; one may undo the other";

describe("roomStatus", () => {
  test("a quiet room is idle", () => {
    expect(roomStatus(room())).toEqual({ state: "idle", label: "", at: T0 });
  });

  test("a busy room works, and names who works", () => {
    expect(roomStatus(room({ session: session("a", { busy: true }) }))).toMatchObject({ state: "working", label: "Working" });
    const agents = [agent("claude-1", "tool"), agent("codex-2", "working"), agent("opencode-1", "idle")];
    expect(roomStatus(room({ session: session("a", { busy: true }), agents })).label).toBe("claude-1 and codex-2 working");
  });

  test("an approval needs you and names the thing", () => {
    const st = roomStatus(room({ session: session("a", { busy: true }), approvals: [approval("p1", "a", T0 + 5)] }));
    expect(st).toEqual({ state: "needs-you", label: "Approve command", at: T0 + 5 });
    const edit = approval("p2", "a", T0, { kind: "edit", path: "internal/auth/auth.go" });
    expect(roomStatus(room({ approvals: [edit] })).label).toBe("Approve edit to auth.go");
    expect(roomStatus(room({ approvals: [approval("p3", "a", T0, { kind: "other" })] })).label).toBe("Answer claude-1");
  });

  test("an agent that waits, a blocked task and a failed one need you", () => {
    expect(roomStatus(room({ agents: [agent("claude-1", "approval")] })).label).toBe("Answer claude-1");
    const blocked = roomStatus(room({ tasks: [task("t1", "blocked", T0 + 9)] }));
    expect(blocked).toEqual({ state: "needs-you", label: "Task blocked: Task t1", at: T0 + 9 });
    expect(roomStatus(room({ tasks: [task("t2", "failed", T0 + 9)] })).label).toBe("Task failed: Task t2");
    expect(roomStatus(room({ agents: [agent("codex-2", "error", T0 + 9)] })).label).toBe("codex-2 stopped with an error");
  });

  test("needs-you clears when the person acts, never because the room was opened", () => {
    const blocked = { tasks: [task("t1", "blocked", T0 + 9)] };
    // Opening the room and reading it changes nothing.
    expect(roomStatus(room({ ...blocked, open: true, unread: false })).state).toBe("needs-you");
    // A message (or a queued one) after the task stopped clears it.
    expect(roomStatus(room({ ...blocked, actedAt: T0 + 10 })).state).toBe("idle");
    // The agent moves on: the task runs again.
    expect(roomStatus(room({ tasks: [task("t1", "running", T0 + 12)] })).state).toBe("idle");
    // An answered approval is gone from the list.
    expect(roomStatus(room({ approvals: [], open: true })).state).toBe("idle");
    // An approval holds even after the person wrote.
    expect(roomStatus(room({ approvals: [approval("p1", "a", T0)], actedAt: T0 + 100 })).state).toBe("needs-you");
    // A task with no time never outranks what the person did.
    expect(roomStatus(room({ tasks: [task("t1", "failed", 0)] })).state).toBe("idle");
  });

  test("an unseen overlap notice shows until it is seen", () => {
    const notice = { session: "a", text: OVERLAP, at: T0 + 3, seen: false, via: null };
    expect(roomStatus(room({ session: session("a", { busy: true }), notices: [notice] }))).toEqual({
      state: "overlap",
      label: "codex-2 and claude-1 both changed auth.go",
      at: T0 + 3,
    });
    expect(roomStatus(room({ notices: [{ ...notice, seen: true }] })).state).toBe("idle");
    expect(roomStatus(room({ notices: [{ ...notice, text: "PR #12 opened" }] })).state).toBe("idle");
  });

  test("a room that finished out of sight is done, or has changes to review, until opened", () => {
    expect(roomStatus(room({ unread: true }))).toEqual({ state: "done", label: "Finished", at: T0 });
    expect(roomStatus(room({ unread: true, changed: 2 }))).toEqual({ state: "review", label: "Changes to review", at: T0 });
    // Opening the room clears the unread mark (ui.ts), and with it the state.
    expect(roomStatus(room({ unread: false, changed: 2 })).state).toBe("idle");
    expect(roomStatus(room({ unread: true, open: true })).state).toBe("idle");
    // Still working: not done yet.
    expect(roomStatus(room({ unread: true, session: session("a", { busy: true }) })).state).toBe("working");
  });

  test("needs-you wins over an overlap, an overlap over work", () => {
    const notice = { session: "a", text: OVERLAP, at: T0, seen: false, via: null };
    const busy = session("a", { busy: true });
    expect(roomStatus(room({ session: busy, notices: [notice], approvals: [approval("p", "a", T0)] })).state).toBe("needs-you");
    expect(roomStatus(room({ session: busy, notices: [notice] })).state).toBe("overlap");
  });

  test("an engine that marks its notices: via overlap is an overlap, the text alone is not", () => {
    const marked = { session: "a", text: OVERLAP, at: T0 + 3, seen: false, via: "overlap" };
    expect(roomStatus(room({ notices: [marked] }))).toEqual({ state: "overlap", label: "codex-2 and claude-1 both changed auth.go", at: T0 + 3 });
    // A note in other words is still an overlap, under its own text.
    expect(roomStatus(room({ notices: [{ ...marked, text: "claude-1 and codex-2 touch a.go" }] })).label).toBe("claude-1 and codex-2 touch a.go");
    // The same words without the mark: a plain notice.
    expect(roomStatus(room({ notices: [{ ...marked, via: "" }] })).state).toBe("idle");
    expect(roomStatus(room({ notices: [{ ...marked, via: "local", text: "You reverted a.go" }] })).state).toBe("idle");
  });

  test("the engine's attention needs you, and the shell keeps its clearing rules", () => {
    expect(roomStatus(room({ attention: "approval" }))).toEqual({ state: "needs-you", label: "Approve a request", at: T0 });
    expect(roomStatus(room({ attention: "question" })).label).toBe("Answer a question");
    // The shell's own list names the thing better.
    expect(roomStatus(room({ attention: "approval", approvals: [approval("p1", "a", T0 + 5)] })).label).toBe("Approve command");
    expect(roomStatus(room({ attention: "" })).state).toBe("idle");
    // An error clears when the person writes again.
    expect(roomStatus(room({ attention: "error" })).label).toBe("An agent stopped with an error");
    expect(roomStatus(room({ attention: "error", actedAt: T0 })).state).toBe("idle");
    expect(roomStatus(room({ attention: "error", agents: [agent("codex-2", "error", T0 - 5)], actedAt: T0 })).state).toBe("idle");
  });

  test("the engine's blocked holds until the task changes, whatever the person writes", () => {
    const tasks = [task("t1", "blocked", T0 + 9)];
    expect(roomStatus(room({ attention: "blocked", tasks, actedAt: T0 + 100 }))).toEqual({ state: "needs-you", label: "Task blocked: Task t1", at: T0 + 9 });
    expect(roomStatus(room({ attention: "blocked" })).label).toBe("Task blocked");
    // A retry or a cancel: the engine says nothing more, and the board no longer counts.
    expect(roomStatus(room({ attention: "", tasks })).state).toBe("idle");
  });
});

describe("helpers", () => {
  test("overlap notes parse by their pattern", () => {
    expect(parseOverlap(OVERLAP)).toEqual({ agents: ["codex-2", "claude-1"], file: "internal/auth/auth.go" });
    expect(parseOverlap("claude and codex both changed a.go")).toEqual({ agents: ["claude", "codex"], file: "a.go" });
    expect(parseOverlap("claude-1 finished")).toBeNull();
  });

  test("the person's last word is their newest message or a queued one", () => {
    const m = (role: Message["role"], at: number): Message => ({
      id: String(at),
      session_id: "a",
      role,
      created_at: iso(at),
      updated_at: iso(at),
      finished: true,
      parts: [],
    });
    expect(actedAt([m("user", T0), m("assistant", T0 + 5)], undefined)).toBe(T0);
    expect(actedAt([m("user", T0)], [{ id: "q", agent: "claude-1", text: "x", at: iso(T0 + 7) }])).toBe(T0 + 7);
    expect(actedAt(undefined, undefined)).toBe(0);
  });

  test("the sidebar puts what needs you first, then what finished, then the rest", () => {
    const rows = [
      { s: session("old", { updated_at: iso(T0 - 50) }), status: undefined },
      { s: session("done", { updated_at: iso(T0 - 100) }), status: { state: "done" as const, label: "Finished", at: T0 - 100 } },
      { s: session("new", { updated_at: iso(T0) }), status: { state: "working" as const, label: "", at: T0 } },
      { s: session("ask", { updated_at: iso(T0 - 200) }), status: { state: "needs-you" as const, label: "", at: T0 - 200 } },
    ];
    expect([...rows].sort(byAttention).map((r) => r.s.id)).toEqual(["ask", "done", "new", "old"]);
  });

  test("notifications: entering needs-you, or finishing once, unless queued or off", () => {
    expect(shouldNotify("working", "needs-you", "all", false)).toBe(true);
    expect(shouldNotify("working", "done", "all", false)).toBe(true);
    expect(shouldNotify("done", "review", "all", false)).toBe(false);
    expect(shouldNotify("working", "done", "needs-you", false)).toBe(false);
    expect(shouldNotify("working", "needs-you", "needs-you", false)).toBe(true);
    expect(shouldNotify("working", "needs-you", "none", false)).toBe(false);
    expect(shouldNotify("working", "needs-you", "all", true)).toBe(false);
    expect(shouldNotify(undefined, "needs-you", "all", false)).toBe(false);
    expect(shouldNotify("needs-you", "needs-you", "all", false)).toBe(false);
  });
});

describe("inboxItems", () => {
  const contract = (id: string, state: string, inbound = true): TeamContract => ({
    id,
    peer: "vps",
    inbound,
    title: "Port the cache",
    brief: "",
    state,
    agent: "",
    note: "",
    summary: "",
    kind: "",
  });
  const project = (key: string, extra: Partial<InboxProject>): InboxProject => ({
    key,
    source: `folder:/${key}`,
    name: key,
    sessions: [],
    rooms: {},
    approvals: [],
    notices: [],
    contracts: [],
    ...extra,
  });

  test("lists what waits across projects: needs-you first, newest first within a state", () => {
    const deck = project("deck", {
      sessions: [session("a"), session("b"), session("c"), session("w")],
      rooms: {
        a: { state: "needs-you", label: "Approve command", at: T0 + 1 },
        b: { state: "done", label: "Finished", at: T0 + 50 },
        c: { state: "needs-you", label: "Task blocked: Map", at: T0 + 30 },
        w: { state: "working", label: "Working", at: T0 + 99 },
      },
      approvals: [approval("p1", "a", T0 + 1)],
      notices: [
        { session: "b", text: "PR #12 opened", at: T0 + 60, seen: false, via: null },
        { session: "b", text: "old news", at: T0 + 2, seen: true, via: null },
        { session: "b", text: OVERLAP, at: T0 + 61, seen: false, via: null },
        { session: "b", text: "codex-2 touches a.go too", at: T0 + 62, seen: false, via: "overlap" },
      ],
      contracts: [contract("c1", "proposed"), contract("c2", "accepted"), contract("c3", "proposed", false)],
    });
    const web = project("web", {
      sessions: [session("x")],
      rooms: { x: { state: "review", label: "Changes to review", at: T0 + 40 }, gone: { state: "done", label: "", at: 0 } },
      approvals: [approval("p2", "x", T0 + 20), approval("p9", "nope", T0 + 99)],
    });
    const items = inboxItems([deck, web]);
    expect(items.map((i) => `${i.project}:${i.state}:${i.action}:${i.room ?? "-"}`)).toEqual([
      "deck:needs-you:open:c",
      "web:needs-you:answer:x",
      "deck:needs-you:answer:a",
      "deck:needs-you:contract:-",
      "web:review:open:x",
      "deck:done:open:b",
      "deck:notice:open:b",
    ]);
    expect(items[1]).toMatchObject({ projectName: "web", title: "x", label: "Approve command", source: "folder:/web" });
    expect(items[1]?.approval?.id).toBe("p2");
    expect(items[3]).toMatchObject({ label: "Contract from vps", title: "Port the cache" });
    expect(items[6]?.label).toBe("PR #12 opened");
  });

  test("Ctrl+Alt+L walks the rooms that need you, across projects, round and round", () => {
    const items = inboxItems([
      project("deck", { sessions: [session("a")], approvals: [approval("p1", "a", T0), approval("p2", "a", T0 + 1)] }),
      project("web", { sessions: [session("x")], rooms: { x: { state: "needs-you", label: "Answer codex-1", at: T0 + 5 } } }),
    ]);
    const rooms = needsYouRooms(items);
    expect(rooms).toEqual([
      { project: "web", room: "x" },
      { project: "deck", room: "a" },
    ]);
    expect(nextNeedsYou(rooms, { project: "deck", room: null })).toEqual({ project: "web", room: "x" });
    expect(nextNeedsYou(rooms, { project: "web", room: "x" })).toEqual({ project: "deck", room: "a" });
    expect(nextNeedsYou(rooms, { project: "deck", room: "a" })).toEqual({ project: "web", room: "x" });
    expect(nextNeedsYou([], { project: "deck", room: "a" })).toBeNull();
  });
});
