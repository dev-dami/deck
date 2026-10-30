import type { Message, Part } from "@web/protocol";
import type { Chunk } from "@web/transcript";
import { describe, expect, test } from "vitest";
import { actionKind, actionStats, actionSummary, buildTurns, groupId, isNote, isTurn, pairResults, pieces, type Turn } from "./actions";

const text = (t: string): Chunk => ({ k: "text", text: t, truncated: false });
const think = (t: string): Chunk => ({ k: "reasoning", text: t, truncated: false });
const tool = (name: string, summary = "", result: Partial<Part> | null = {}): Chunk => ({
  k: "tool",
  call: { type: "tool_call", id: `c_${name}_${summary}`, name, summary },
  result: result === null ? null : { type: "tool_result", name, output: "", ...result },
});

let seq = 0;
const at = () => new Date(Date.UTC(2026, 9, 27, 9, 0, seq++)).toISOString();
const msg = (role: Message["role"], parts: Part[], agent?: string, finished = true): Message => ({
  id: `m${seq}`,
  session_id: "s",
  role,
  ...(agent === undefined ? {} : { agent }),
  created_at: at(),
  updated_at: at(),
  finished,
  parts,
});
const call = (id: string, name = "bash", summary = "go test"): Part => ({ type: "tool_call", id, name, summary });
const result = (id: string, output = "ok", is_error = false): Part => ({ type: "tool_result", tool_call_id: id, name: "bash", output, is_error });
const say = (t: string): Part => ({ type: "text", text: t });

const kinds = (ps: ReturnType<typeof pieces>) => ps.map((p) => (p.k === "actions" ? `actions(${p.g.items.length})` : p.c.k));

describe("action kinds", () => {
  test("a tool's name says what it does", () => {
    expect(actionKind("Bash")).toBe("command");
    expect(actionKind("run_tests")).toBe("command");
    expect(actionKind("Edit")).toBe("edit");
    expect(actionKind("MultiEdit")).toBe("edit");
    expect(actionKind("Grep")).toBe("search");
    expect(actionKind("Read")).toBe("read");
    expect(actionKind("WebFetch")).toBe("web");
    expect(actionKind("TodoWrite")).toBe("edit");
    expect(actionKind("Task")).toBe("other");
  });
});

describe("pieces", () => {
  test("a run of calls is one group; the text around it stays", () => {
    const ps = pieces([text("Looking."), tool("Read", "a.go"), tool("Read", "b.go"), tool("Bash", "go test"), text("Fixed: the limiter.")], "ada");
    expect(kinds(ps)).toEqual(["text", "actions(3)", "text"]);
    const g = ps[1]!;
    expect(g.k === "actions" && g.g.agent).toBe("ada");
    expect(g.k === "actions" && g.g.id).toBe("1");
  });

  test("thinking between calls joins the run; thinking alone stays", () => {
    expect(kinds(pieces([think("hm"), tool("Read"), think("so"), tool("Bash")], "a"))).toEqual(["actions(4)"]);
    expect(kinds(pieces([think("hm"), text("Hello.")], "a"))).toEqual(["reasoning", "text"]);
  });

  test("a short note between two calls folds in; long text or a list breaks the run", () => {
    expect(kinds(pieces([tool("Read"), text("Now the tests."), tool("Bash"), text("Done.")], "a"))).toEqual(["actions(3)", "text"]);
    const long = "x".repeat(200);
    expect(kinds(pieces([tool("Read"), text(long), tool("Bash")], "a"))).toEqual(["actions(1)", "text", "actions(1)"]);
    expect(kinds(pieces([tool("Read"), text("Plan:\n- one\n- two"), tool("Bash")], "a"))).toEqual(["actions(1)", "text", "actions(1)"]);
  });

  test("a note after the last call is the answer, not part of the run", () => {
    expect(kinds(pieces([tool("Read"), tool("Bash"), text("All green.")], "a"))).toEqual(["actions(2)", "text"]);
  });

  test("an image breaks the run", () => {
    const img: Chunk = { k: "image", name: "shot.png", mime: "image/png" };
    expect(kinds(pieces([tool("Read"), img, tool("Bash")], "a"))).toEqual(["actions(1)", "image", "actions(1)"]);
  });

  test("notes are short single paragraphs", () => {
    expect(isNote(text("Now the tests."))).toBe(true);
    expect(isNote(text("a\n\nb"))).toBe(false);
    expect(isNote(text("```go\nx\n```"))).toBe(false);
    expect(isNote(tool("Read"))).toBe(false);
  });
});

describe("stats and summary", () => {
  test("files count once each; the most common action reads first", () => {
    const items = [
      tool("Read", "a.go"),
      tool("Read", "b.go"),
      tool("Read", "a.go"),
      tool("Bash", "go test"),
      tool("Edit", "a.go"),
      tool("Bash", "go vet", { is_error: true }),
    ];
    const s = actionStats(items, false);
    expect(s.actions).toBe(6);
    expect(s.failed).toBe(1);
    expect(s.read).toEqual(["a.go", "b.go"]);
    expect(s.edited).toEqual(["a.go"]);
    expect(actionSummary(s)).toBe("read 2 files, ran 2 commands, edited 1 file");
  });

  test("while running, the call without a result is the current one", () => {
    const items = [tool("Read", "a.go"), tool("Bash", "go test", null), tool("Read", "b.go")];
    const s = actionStats(items, true);
    expect(s.pending).toBe(1);
    expect(s.current?.call?.summary).toBe("go test");
    // Once the turn ends, a call with no result is no longer pending.
    expect(actionStats(items, false).pending).toBe(0);
    expect(actionStats(items, false).current?.call?.summary).toBe("b.go");
  });

  test("calls that name no file count as calls", () => {
    expect(actionSummary(actionStats([tool("Read"), tool("Read"), tool("WebFetch"), tool("Task")], false))).toBe(
      "read 2 files, fetched 1 page, used 1 other tool",
    );
  });
});

describe("turns", () => {
  const isMsg = (r: unknown): r is Message => typeof r === "object" && r !== null && "role" in r;
  const turns = (rows: Message[], prev?: Map<string, Turn>) => buildTurns<never>(rows, isMsg as (r: Message) => r is Message, prev);

  test("Deck's result messages fold into the calls they answer", () => {
    const user = msg("user", [say("fix it")]);
    const step = msg("assistant", [say("Looking."), call("t1"), call("t2")], "ada");
    const r1 = msg("assistant", [result("t1", "PASS")]);
    const r2 = msg("assistant", [result("t2", "FAIL", true)]);
    const answer = msg("assistant", [say("Done.")], "ada");
    const rows = turns([user, step, r1, r2, answer]);
    expect(rows).toHaveLength(2);
    const t = rows[1]!;
    expect(isTurn(t)).toBe(true);
    if (!isTurn(t)) return;
    expect(t.agent).toBe("ada");
    expect(t.msgs.map((m) => m.id)).toEqual([step.id, answer.id]);
    const tools = t.chunks.filter((c) => c.k === "tool");
    expect(tools.map((c) => c.k === "tool" && c.result?.output)).toEqual(["PASS", "FAIL"]);
    expect(kinds(pieces(t.chunks, t.agent))).toEqual(["text", "actions(2)", "text"]);
  });

  test("interleaved agents get turns of their own", () => {
    const a1 = msg("assistant", [call("a")], "ada");
    const b1 = msg("assistant", [call("b")], "bob");
    const ra = msg("assistant", [result("a")]);
    const rb = msg("assistant", [result("b")]);
    const a2 = msg("assistant", [say("ada done")], "ada");
    const rows = turns([a1, b1, ra, rb, a2]);
    expect(rows.map((r) => (isTurn(r) ? r.agent : "?"))).toEqual(["ada", "bob", "ada"]);
    // Each call still finds its result across the other agent's turn.
    expect(rows.every((r) => !isTurn(r) || r.chunks.every((c) => c.k !== "tool" || c.result !== null))).toBe(true);
  });

  test("a result whose call is out of the window stays, as its agent's", () => {
    const lone = msg("assistant", [result("old")]);
    const rows = turns([msg("assistant", [say("hi")], "ada"), lone]);
    expect(rows).toHaveLength(1);
    const t = rows[0]!;
    expect(isTurn(t) && t.msgs.length).toBe(2);
  });

  test("a turn runs while any of its messages does", () => {
    const rows = turns([msg("assistant", [call("x")], "ada", true), msg("assistant", [say("…")], "ada", false)]);
    expect(isTurn(rows[0]) && rows[0].finished).toBe(false);
  });

  test("a turn that did not change is the same object", () => {
    const a = msg("assistant", [call("k")], "ada");
    const b = msg("user", [say("next")]);
    const first = turns([a, b]);
    const prev = new Map(first.filter(isTurn).map((t) => [t.id, t]));
    const again = turns([a, b, msg("assistant", [say("more")], "bob")], prev);
    expect(again[0]).toBe(first[0]);
    // A result arriving changes it.
    const later = turns([a, msg("assistant", [result("k")]), b], prev);
    expect(later[0]).not.toBe(first[0]);
  });

  test("a group's id is where it starts, so it holds as the turn grows", () => {
    const a = msg("assistant", [say("Look."), call("g1")], "ada");
    const t1 = turns([a]).find(isTurn)!;
    const t2 = turns([a, msg("assistant", [call("g2")], "ada")]).find(isTurn)!;
    const id = (t: Turn) => {
      const p = pieces(t.chunks, t.agent, (i) => groupId(t, i)).find((x) => x.k === "actions");
      return p?.k === "actions" ? p.g.id : "";
    };
    expect(id(t1)).toBe(`${a.id}:1`);
    expect(id(t2)).toBe(id(t1));
  });

  test("pairing marks only messages made of foreign results as consumed", () => {
    const a = msg("assistant", [call("p")], "ada");
    const r = msg("assistant", [result("p")]);
    const mixed = msg("assistant", [result("p"), say("and")]);
    const p = pairResults([a, r, mixed]);
    expect(p.consumed.has(r.id)).toBe(true);
    expect(p.consumed.has(mixed.id)).toBe(false);
  });
});

describe("buildTurns: CLI-native subagents", () => {
  test("each subagent's work is one block under its member's run, its siblings' messages between or not", () => {
    const rows = [
      msg("user", [say("spin up sub agents to review each of them")]),
      msg("assistant", [say("Starting two reviewers."), call("ta", "Task", "Review PR #295"), call("tb", "Task", "Review PR #296")], "claude"),
      msg("assistant", [call("a1", "Bash", "gh pr diff 295")], "claude/review-pr-295"),
      msg("assistant", [call("b1", "Bash", "gh pr diff 296")], "claude/review-pr-296"),
      msg("assistant", [result("a1")]),
      msg("assistant", [result("b1", "rate limit", true)]),
      msg("assistant", [say("PR #295 looks good.")], "claude/review-pr-295"),
      msg("assistant", [result("ta", "PR #295 looks good.")]),
      msg("assistant", [result("tb", "stopped", true)]),
      msg("assistant", [say("The #296 reviewer stopped; I restarted it."), call("tc", "Task", "Review PR #296")], "claude"),
      msg("assistant", [call("c1", "Grep", "TODO")], "claude/review-pr-296-2", false),
    ];
    const out = buildTurns(rows, (_r): _r is Message => true);
    const turns = out.filter(isTurn);
    expect(turns.map((t) => `${t.agent}<${t.parent ?? ""}> ${t.msgs.length}`)).toEqual([
      "claude<> 2",
      "claude/review-pr-295<claude> 2",
      "claude/review-pr-296<claude> 1",
      "claude/review-pr-296-2<claude> 1",
    ]);
    // The results pair with their calls inside each block.
    const b = turns[2]!;
    expect(b.chunks).toHaveLength(1);
    expect(b.chunks[0]!.k === "tool" && b.chunks[0]!.result?.is_error).toBe(true);
    expect(turns[3]!.finished).toBe(false);
    // The member's own words stay its own, in one run.
    expect(turns[0]!.chunks.filter((c) => c.k === "text").map((c) => (c.k === "text" ? c.text : ""))).toEqual([
      "Starting two reviewers.",
      "The #296 reviewer stopped; I restarted it.",
    ]);
  });

  test("the person's next message closes the blocks", () => {
    const rows = [
      msg("assistant", [say("one")], "claude/explore"),
      msg("user", [say("and now?")]),
      msg("assistant", [say("two")], "claude/explore"),
    ];
    expect(buildTurns(rows, (_r): _r is Message => true).filter(isTurn).map((t) => t.msgs.length)).toEqual([1, 1]);
  });
});

describe("buildTurns: subagents agent.spawn started", () => {
  test("the tree names their parent: their work is a block under its run", () => {
    const rows = [
      msg("user", [say("fix the limiter, with a reviewer")]),
      msg("assistant", [say("→ $codex-2\n\nReview the limiter.")], "claude-1"),
      msg("assistant", [call("x", "Bash", "go test ./...")], "codex-2"),
      msg("assistant", [say("Keying by address now.")], "claude-1"),
      msg("assistant", [result("x")]),
      msg("assistant", [say("→ $claude-1\n\nLooks right.")], "codex-2"),
      msg("assistant", [say("A peer's word.")], "codex-1"),
    ];
    const parents: Record<string, string> = { "codex-2": "claude-1" };
    const out = buildTurns(rows, (_r): _r is Message => true, undefined, (a) => parents[a] ?? null);
    expect(out.filter(isTurn).map((t) => `${t.agent}<${t.parent ?? ""}> ${t.msgs.length}`)).toEqual(["claude-1<> 2", "codex-2<claude-1> 2", "codex-1<> 1"]);
    // Without the tree's word it is an agent like any other.
    expect(buildTurns(rows, (_r): _r is Message => true).filter(isTurn).map((t) => t.parent)).toEqual([null, null, null, null, null]);
  });

  test("a turn whose parent becomes known is a new object", () => {
    const rows = [msg("assistant", [say("on it")], "codex-2")];
    const first = buildTurns(rows, (_r): _r is Message => true).filter(isTurn);
    const prev = new Map(first.map((t) => [t.id, t]));
    const next = buildTurns(rows, (_r): _r is Message => true, prev, () => "claude-1").filter(isTurn);
    expect(next[0]).not.toBe(first[0]);
    expect(next[0]!.parent).toBe("claude-1");
  });
});
