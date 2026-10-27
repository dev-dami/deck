import type { AgentLine, Message, Part } from "@web/protocol";
import { describe, expect, test } from "vitest";
import { askOf, diffStat, editors, editsOf, fileWork, parseAskLine, pendingAsks, samePath } from "./fileWork";
import { buildGraph, type FileGraphNode } from "./graph";
import { buildHistory, type HistoryThread } from "./workHistory";

let clock = Date.UTC(2026, 9, 27, 9, 0, 0);
let n = 0;
const msg = (role: Message["role"], parts: Part[], agent?: string): Message => {
  const at = new Date((clock += 1000)).toISOString();
  return { id: `m${n++}`, session_id: "s1", role, ...(agent === undefined ? {} : { agent }), created_at: at, updated_at: at, finished: true, parts };
};
const say = (t: string): Part => ({ type: "text", text: t });
const edit = (id: string, path: string, diff: string, agent: string): Message[] => [
  msg("assistant", [{ type: "tool_call", id, name: "Edit", summary: path }], agent),
  msg("assistant", [{ type: "tool_result", tool_call_id: id, name: "Edit", output: "ok", diff }]),
];
const line = (text: string) => msg("system", [say(text)]);

// The engine's lines (session.AskLine; coord_test.go checks the same).
const PENDING = "@bob asked @ada about `pkg/auth/limit.go`";
const GRANTED = "@bob asked @ada about `pkg/auth/limit.go` → ada: go ahead, I'm done";

// ada edits limit.go; bob asks ada, who answers; bob edits it; ada asks bob
// about prune.go, still waiting.
function shared(): Message[] {
  return [
    msg("user", [say("@ada fix the limiter, @bob prune the buckets")]),
    ...edit("e1", "pkg/auth/limit.go", "@@ -1 +1 @@\n-global.fail()\n+l.fail(addr)\n", "ada"),
    ...edit("e2", "pkg/auth/prune.go", "@@ -0,0 +1,2 @@\n+func prune() {}\n+// idle\n", "bob"),
    line(GRANTED),
    ...edit("e3", "pkg/auth/limit.go", "@@ -3 +3 @@\n-x\n+y\n", "bob"),
    line("@ada asked @bob about `pkg/auth/prune.go`"),
  ];
}

const roster: AgentLine[] = [
  { id: "ada", name: "ada", kind: "claude", busy: true },
  { id: "bob", name: "bob", kind: "codex", busy: true },
];

describe("ask lines", () => {
  test("each state of the engine's line", () => {
    expect(parseAskLine(PENDING)).toEqual({ from: "bob", to: "ada", path: "pkg/auth/limit.go", state: "pending", by: null, answer: "" });
    expect(parseAskLine(GRANTED)).toMatchObject({ state: "granted", by: "ada", answer: "go ahead, I'm done" });
    expect(parseAskLine("@bob asked @ada about `a.go` → ada (wait): still on it")).toMatchObject({ state: "held", by: "ada", answer: "still on it" });
    expect(parseAskLine("@bob asked @ada about `a.go` → deck: ada finished its turn")).toMatchObject({ state: "released", by: null, answer: "ada finished its turn" });
    expect(parseAskLine("bob asked ada about a.go")).toBeNull();
    expect(askOf(msg("assistant", [say(PENDING)], "bob"))).toBeNull();
    expect(askOf(line(PENDING))).toMatchObject({ from: "bob", state: "pending" });
  });

  test("paths and diffs", () => {
    expect(samePath("/repo/pkg/a.go", "pkg/a.go")).toBe(true);
    expect(samePath("./pkg/a.go", "pkg/a.go")).toBe(true);
    expect(samePath("pkg/a.go", "pkg/b.go")).toBe(false);
    expect(diffStat("--- a/x\n+++ b/x\n@@\n-a\n+b\n+c\n")).toEqual({ added: 2, removed: 1 });
  });
});

describe("fileWork", () => {
  test("each file's edits, asks and owner", () => {
    const w = fileWork(shared());
    const limit = w.get("pkg/auth/limit.go")!;
    expect(limit.edits.map((e) => [e.agent, e.added, e.removed])).toEqual([
      ["ada", 1, 1],
      ["bob", 1, 1],
    ]);
    expect(limit.edits[0]!.diff).toContain("+l.fail(addr)");
    expect(limit.asks.map((a) => a.state)).toEqual(["granted"]);
    // Handed to bob, who edited it last.
    expect(limit.owner).toBe("bob");
    expect(editors(limit)).toEqual(["ada", "bob"]);
    const prune = w.get("pkg/auth/prune.go")!;
    expect(pendingAsks(prune).map((a) => `${a.from}>${a.to}`)).toEqual(["ada>bob"]);
    expect(prune.owner).toBe("bob");
  });

  test("a lease's holder owns the file; failed and non-edit calls do not count", () => {
    const msgs = [
      ...shared(),
      msg("assistant", [{ type: "tool_call", id: "r1", name: "Read", summary: "pkg/auth/limit.go" }], "ada"),
      msg("assistant", [{ type: "tool_call", id: "e9", name: "Edit", summary: "pkg/auth/limit.go" }], "ada"),
      msg("assistant", [{ type: "tool_result", tool_call_id: "e9", name: "Edit", output: "no match", is_error: true }]),
    ];
    expect(editsOf(msgs).filter((e) => e.path === "pkg/auth/limit.go")).toHaveLength(2);
    const w = fileWork(msgs, [{ agent: "ada", paths: ["pkg/auth/**"], exclusive: false }]);
    expect(w.get("pkg/auth/limit.go")).toMatchObject({ owner: "ada", holders: ["ada"] });
  });
});

describe("the Work Graph's files", () => {
  test("now: the owner's edge in its hue, an ask waiting on a file", () => {
    const g = buildGraph({ roster, crew: null, board: null, files: null, work: fileWork(shared()) });
    const limit = g.nodes.find((x) => x.id === "file:pkg/auth/limit.go") as FileGraphNode;
    expect(limit).toMatchObject({ owner: "bob", editors: ["ada", "bob"], asking: [] });
    expect(limit.h).toBeGreaterThan(30);
    const prune = g.nodes.find((x) => x.id === "file:pkg/auth/prune.go") as FileGraphNode;
    expect(prune.asking).toEqual([{ from: "ada", fromKind: "claude", to: "bob", state: "pending" }]);
    const edge = (id: string) => g.edges.find((e) => e.id === id);
    expect(edge("edited:agent:bob>file:pkg/auth/limit.go")).toMatchObject({ tint: "agent:bob" });
    expect(edge("edited:agent:ada>file:pkg/auth/limit.go")).toMatchObject({ tint: null });
    expect(edge("asks:agent:ada>file:pkg/auth/prune.go")).toMatchObject({ live: true, tint: "agent:ada" });
    // An ask stands for the asker's edge to the file.
    expect(edge("edited:agent:ada>file:pkg/auth/prune.go")).toBeUndefined();
  });

  test("history: a row per contested file with its edits and asks", () => {
    const t: HistoryThread = { id: "s1", title: "Shared", messages: shared(), roster };
    const g = buildHistory([t]);
    const row = (path: string) => g.nodes.filter((x) => x.lane === `files/${path}`);
    // limit.go: ada's edit is the file's own node; bob's ask and edit follow.
    expect(row("pkg/auth/limit.go").map((x) => `${x.kind}:${x.agent}`)).toEqual(["ask:bob", "edit:bob"]);
    const ask = row("pkg/auth/limit.go")[0]!;
    expect(ask).toMatchObject({ title: "asked ada", meta: "ada: go ahead, I'm done" });
    // It jumps to its line in the conversation.
    expect(ask.mid).toBe(t.messages.find((m) => m.role === "system")!.id);
    const bobEdit = row("pkg/auth/limit.go")[1]!;
    expect(bobEdit.diff).toContain("+y");
    expect(bobEdit.meta).toBe("+1 −1");
    // The ask comes from bob's work before it, and leads to bob's edit.
    const kinds = (to: string) => g.edges.filter((e) => e.to === to).map((e) => e.kind).sort();
    expect(kinds(ask.id)).toEqual(["requests"]);
    expect(kinds(bobEdit.id)).toEqual(["answers", "touches"]);
    // prune.go: one editor, but an ask waiting.
    expect(row("pkg/auth/prune.go").map((x) => `${x.kind}:${x.ask?.state ?? ""}`)).toEqual(["ask:pending"]);
    // The rows sit in time order, left to right, without overlapping.
    const r = row("pkg/auth/limit.go");
    const file = g.nodes.find((x) => x.id === "f:pkg/auth/limit.go")!;
    expect(file.y).toBe(r[0]!.y);
    expect(r[0]!.x).toBeGreaterThanOrEqual(file.x + file.w);
    expect(r[1]!.x).toBeGreaterThanOrEqual(r[0]!.x + r[0]!.w);
    expect(g.lanes.map((l) => l.label)).toEqual(["you", "ada", "bob", "limit.go", "prune.go"]);
    // Without files, no timelines.
    const noFiles = buildHistory([t], { agents: null, threads: null, since: null, until: null, kinds: ["messages", "actions", "tasks"] });
    expect(noFiles.nodes.some((x) => x.kind === "edit" || x.kind === "ask")).toBe(false);
  });
});
