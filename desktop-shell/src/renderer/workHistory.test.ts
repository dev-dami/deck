import type { Message, Part } from "@web/protocol";
import { describe, expect, test } from "vitest";
import type { BoardTask } from "./crew";
import {
  ALL_WORK,
  buildHistory,
  excerpt,
  type HistoryFilter,
  type HistoryGraph,
  type HistoryThread,
  historyPath,
  inView,
  ITEM_H,
  mentions,
  NODE_H,
} from "./workHistory";

let clock = Date.UTC(2026, 9, 27, 9, 0, 0);
let n = 0;
const msg = (session: string, role: Message["role"], parts: Part[], agent?: string, finished = true): Message => {
  const at = new Date((clock += 1000)).toISOString();
  return { id: `m${n++}`, session_id: session, role, ...(agent === undefined ? {} : { agent }), created_at: at, updated_at: at, finished, parts };
};
const say = (t: string): Part => ({ type: "text", text: t });
const call = (id: string, name: string, summary: string): Part => ({ type: "tool_call", id, name, summary });
const result = (id: string, is_error = false): Part => ({ type: "tool_result", tool_call_id: id, name: "tool", output: "", is_error });

// A thread as Deck stores it: ada works, hands review to bob, who answers.
function busy(id = "s1", title = "Busy"): HistoryThread {
  const m = (role: Message["role"], parts: Part[], agent?: string, finished = true) => msg(id, role, parts, agent, finished);
  const messages = [
    m("user", [say("@ada fix the limiter")]),
    m("assistant", [say("Looking."), call(`${id}a`, "Read", "limit.go"), call(`${id}b`, "Bash", "go test")], "ada"),
    m("assistant", [result(`${id}a`)]),
    m("assistant", [result(`${id}b`, true)]),
    m("assistant", [call(`${id}c`, "Edit", "limit.go")], "ada"),
    m("assistant", [result(`${id}c`)]),
    m("assistant", [say("Fixed it. @bob please review.")], "ada"),
    m("assistant", [call(`${id}d`, "Bash", "git diff")], "bob"),
    m("assistant", [result(`${id}d`)]),
    m("assistant", [say("Looks right.")], "bob"),
  ];
  return {
    id,
    title,
    messages,
    roster: [
      { id: "ada", name: "ada", kind: "claude", busy: false },
      { id: "bob", name: "bob", kind: "codex", busy: false },
    ],
  };
}

const ids = (g: HistoryGraph, kind?: string) => g.nodes.filter((x) => kind === undefined || x.kind === kind).map((x) => x.id);
const edgeKinds = (g: HistoryGraph) => g.edges.map((e) => e.kind).sort();
const nodeOf = (g: HistoryGraph, id: string) => g.nodes.find((x) => x.id === id)!;

describe("history", () => {
  test("a thread is a band of lanes: you, then each agent", () => {
    const t = busy();
    const g = buildHistory([t]);
    expect(g.bands.map((b) => b.title)).toEqual(["Busy", "Files"]);
    expect(g.lanes.map((l) => l.label)).toEqual(["you", "ada", "bob", "files"]);
    expect(g.lanes.find((l) => l.label === "bob")?.agentKind).toBe("codex");
    // The prompt, ada's run of three calls (its result messages folded in),
    // ada's answer, bob's run and bob's answer.
    expect(g.nodes.filter((x) => x.kind !== "file").map((x) => x.kind)).toEqual(["prompt", "actions", "reply", "actions", "reply"]);
    const run = g.nodes.find((x) => x.kind === "actions" && x.agent === "ada")!;
    expect(run.actions).toMatchObject({ count: 3, failed: 1, running: false });
    expect(run.title).toBe("3 actions");
    // The narration before the calls goes; the run jumps to where it starts.
    expect(g.nodes.some((x) => x.title === "Looking.")).toBe(false);
    expect(run.mid).toBe(t.messages[1]!.id);
  });

  test("edges: who asked whom, the handoff for review, and the files", () => {
    const g = buildHistory([busy()]);
    expect(edgeKinds(g)).toEqual(["asks", "reviews", "touches"]);
    const asks = g.edges.find((e) => e.kind === "asks")!;
    expect(nodeOf(g, asks.from).kind).toBe("prompt");
    expect(nodeOf(g, asks.to).agent).toBe("ada");
    const review = g.edges.find((e) => e.kind === "reviews")!;
    expect(nodeOf(g, review.from).agent).toBe("ada");
    expect(nodeOf(g, review.to).agent).toBe("bob");
    // limit.go was read and edited by one run: one edge, the edit.
    expect(ids(g, "file")).toEqual(["f:limit.go"]);
    expect(g.edges.filter((e) => e.to === "f:limit.go").map((e) => e.kind)).toEqual(["touches"]);
  });

  test("columns follow time in each thread, and every edge but a file's runs forward", () => {
    const g = buildHistory([busy("s1"), busy("s2")]);
    for (const t of ["s1", "s2"]) {
      const work = g.nodes.filter((x) => x.thread === t).sort((a, b) => a.at - b.at);
      expect(work[0]!.col).toBe(0);
      for (let i = 1; i < work.length; i++) expect(work[i]!.col).toBeGreaterThanOrEqual(work[i - 1]!.col);
    }
    for (const e of g.edges) {
      if (e.kind === "touches" || e.kind === "reads") continue;
      // A prompt sits over the reply it asks for; the rest move on.
      if (e.kind === "asks") expect(nodeOf(g, e.to).col).toBeGreaterThanOrEqual(nodeOf(g, e.from).col);
      else expect(nodeOf(g, e.to).col).toBeGreaterThan(nodeOf(g, e.from).col);
    }
    // No two nodes of one lane overlap.
    for (const l of g.lanes) {
      const xs = g.nodes.filter((x) => x.lane === l.id).map((x) => x.col);
      expect(new Set(xs).size).toBe(xs.length);
    }
  });

  test("work side by side in two lanes shares a column", () => {
    const a = msg("p", "assistant", [say("ada starts")], "ada");
    const b = msg("p", "assistant", [say("bob starts")], "bob");
    const g = buildHistory([{ id: "p", title: "P", messages: [msg("p", "user", [say("go")]), a, b], roster: [] }]);
    const cols = g.nodes.filter((x) => x.kind === "reply").map((x) => x.col);
    expect(cols[0]).toBe(cols[1]);
  });

  test("threads band in the order their work began; files share one band", () => {
    const later = busy("s2", "Later");
    const first = busy("s1", "First");
    const g = buildHistory([later, first]);
    expect(g.bands.map((b) => b.title)).toEqual(["Later", "First", "Files"]);
    // Both touched limit.go: one node, an edge from each thread's run.
    expect(g.edges.filter((e) => e.to === "f:limit.go")).toHaveLength(2);
    expect(g.threads.map((t) => t.id)).toEqual(["s2", "s1"]);
  });

  test("filters: by agent, thread, time and kind", () => {
    const t1 = busy("s1");
    const t2 = busy("s2");
    const f = (over: Partial<HistoryFilter>) => buildHistory([t1, t2], { ...ALL_WORK, ...over });
    const onlyBob = f({ agents: ["bob"] });
    expect(new Set(onlyBob.nodes.filter((x) => x.agent !== null && x.kind !== "file").map((x) => x.agent))).toEqual(new Set(["bob"]));
    // The prompts stay, so a filtered lane still shows what started it.
    expect(onlyBob.nodes.some((x) => x.kind === "prompt")).toBe(true);
    expect(f({ threads: ["s2"] }).bands.map((b) => b.thread)).toEqual(["s2", ""]);
    const mid = toTime(t2.messages[0]!);
    expect(f({ since: mid }).nodes.every((x) => x.kind === "file" || x.at >= mid)).toBe(true);
    const noFiles = f({ kinds: ["messages", "actions", "tasks"] });
    expect(ids(noFiles, "file")).toEqual([]);
    expect(noFiles.lanes.some((l) => l.id === "files")).toBe(false);
    const noRuns = f({ kinds: ["messages"] });
    expect(ids(noRuns, "actions")).toEqual([]);
    // Every edge has both its ends.
    for (const g of [onlyBob, noFiles, noRuns]) for (const e of g.edges) expect(ids(g)).toEqual(expect.arrayContaining([e.from, e.to]));
    // The filters' choices list everything, filtered or not.
    expect(onlyBob.agents.map((a) => a.name)).toEqual(["ada", "bob"]);
  });

  test("an opened run grows to list its calls", () => {
    const t = busy();
    const g = buildHistory([t]);
    const run = g.nodes.find((x) => x.kind === "actions" && x.agent === "ada")!;
    const open = buildHistory([t], ALL_WORK, new Set([run.id]));
    const grown = open.nodes.find((x) => x.id === run.id)!;
    expect(grown.open).toBe(true);
    expect(grown.h).toBe(NODE_H + 6 + 3 * ITEM_H);
    // Its lane grows with it.
    expect(open.lanes.find((l) => l.label === "ada")!.h).toBe(grown.h);
  });

  test("a big history folds each agent's work between prompts into a cluster that opens", () => {
    const t = busy();
    const g = buildHistory([t], ALL_WORK, new Set(), 3);
    expect(g.clustered).toBe(true);
    const ada = g.nodes.find((x) => x.kind === "cluster" && x.agent === "ada")!;
    expect(ada.members).toBe(2);
    expect(ada.meta).toBe("1 reply · 3 actions · 1 failed");
    // The edges move to the cluster.
    expect(g.edges.some((e) => e.from === ada.id && e.kind === "reviews")).toBe(true);
    expect(g.edges.some((e) => e.from === ada.id && e.to === "f:limit.go")).toBe(true);
    const opened = buildHistory([t], ALL_WORK, new Set([ada.id]), 3);
    expect(opened.nodes.some((x) => x.id === ada.id)).toBe(false);
    expect(opened.nodes.filter((x) => x.agent === "ada" && x.kind !== "cluster")).toHaveLength(2);
  });

  test("tasks join their owner's and reviewer's work", () => {
    const t = busy();
    const task = {
      id: "t1",
      title: "Key failures by address",
      status: "done",
      owner: "ada",
      reviewer: "bob",
      files: ["limit_test.go"],
      updatedAt: Math.floor(clock / 1000),
    } as BoardTask;
    const g = buildHistory([{ ...t, tasks: [task] }]);
    const node = g.nodes.find((x) => x.kind === "task")!;
    expect(node.lane).toBe("s1/tasks");
    expect(g.lanes.map((l) => l.label)).toContain("tasks");
    expect(g.edges.filter((e) => e.to === node.id).map((e) => `${e.kind}:${e.agent}`).sort()).toEqual(["owns:ada", "reviews:bob"]);
    expect(g.edges.some((e) => e.from === node.id && e.to === "f:limit_test.go")).toBe(true);
  });

  test("a turn at work marks its run as running", () => {
    const m = [msg("r", "user", [say("go")]), msg("r", "assistant", [call("x", "Bash", "make")], "ada", false)];
    const g = buildHistory([{ id: "r", title: "R", messages: m, roster: [] }]);
    expect(g.nodes.find((x) => x.kind === "actions")?.actions?.running).toBe(true);
  });

  test("mentions and excerpts", () => {
    const known = new Set(["ada", "bob", "codex-1"]);
    expect(mentions("@ada and $Codex-1, then @nobody. cc @bob.", known)).toEqual(["ada", "codex-1", "bob"]);
    expect(mentions("mail me at x@ada.dev", known)).toEqual([]);
    expect(excerpt("## Plan\n\n**First** do `x`")).toBe("Plan");
    expect(excerpt("x".repeat(100), 10)).toBe(`${"x".repeat(9)}…`);
  });

  test("edge paths: across when ahead, down otherwise", () => {
    expect(historyPath({ x: 0, y: 0, w: 10, h: 10 }, { x: 50, y: 0, w: 10, h: 10 })).toMatch(/^M10 5 C/);
    expect(historyPath({ x: 0, y: 0, w: 10, h: 10 }, { x: 0, y: 40, w: 10, h: 10 })).toMatch(/^M5 10 C.* 5 40$/);
  });

  test("a big graph draws only what is in sight", () => {
    const g = buildHistory([busy("s1"), busy("s2"), busy("s3")]);
    const first = g.nodes.find((x) => x.kind === "prompt")!;
    const seen = { x: first.x, y: first.y, w: 10, h: 10 };
    const v = inView(g, seen, 0);
    expect(v.nodes.map((x) => x.id)).toContain(first.id);
    expect(v.nodes.length).toBeLessThan(g.nodes.length);
    expect(inView(g, null).nodes).toBe(g.nodes);
  });

  test("an empty project draws nothing", () => {
    const g = buildHistory([]);
    expect(g.nodes).toEqual([]);
    expect(g.bounds).toEqual({ x: 0, y: 0, w: 0, h: 0 });
  });
});

function toTime(m: Message): number {
  return Date.parse(m.created_at);
}
