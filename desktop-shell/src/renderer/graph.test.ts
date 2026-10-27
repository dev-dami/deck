import type { AgentLine, ChangedFile } from "@web/protocol";
import { describe, expect, test } from "vitest";
import type { AgentNode, Board, BoardTask, Crew, RunningTool } from "./crew";
import {
  buildGraph,
  chooseDirection,
  COLUMN_GAP,
  crossings,
  type Direction,
  edgePath,
  editTarget,
  fitView,
  FOLD,
  FOLD_AT,
  type GraphEdge,
  type GraphInput,
  type GraphNode,
  graphSummary,
  globMatch,
  isEmpty,
  LAYER_GAP,
  neighbourhood,
  orderLayers,
  ROW_GAP,
  SIBLING_GAP,
  SIZE,
  stepFocus,
  ZOOM_MAX,
  ZOOM_MIN,
  zoomAt,
} from "./graph";

const line = (id: string, busy = false): AgentLine => ({ id, name: id, kind: id.replace(/-\w+$/, ""), busy });

const node = (name: string, over: Partial<AgentNode> = {}): AgentNode => ({
  name,
  kind: name.replace(/-\w+$/, ""),
  model: "",
  effort: "",
  status: "idle",
  contextTokens: 0,
  contextWindow: 0,
  costUsd: 0,
  pending: 0,
  since: 0,
  depth: 0,
  parent: "",
  branch: "",
  leases: "",
  profile: "",
  role: "",
  color: "",
  ...over,
});

const task = (id: string, over: Partial<BoardTask> = {}): BoardTask => ({
  id,
  title: id,
  status: "pending",
  owner: "",
  deps: [],
  brief: "",
  result: "",
  error: "",
  attempts: 0,
  tries: 0,
  branch: "",
  updatedAt: 0,
  race: false,
  entry: false,
  stats: "",
  files: [],
  reviewer: "",
  reviews: 0,
  ...over,
});

const tool = (agent: string, name: string, summary: string, over: Partial<RunningTool> = {}): RunningTool => ({
  agent,
  kind: "claude",
  name,
  summary,
  detail: "",
  started: 0,
  subagent: false,
  command: false,
  ...over,
});

const file = (path: string, added = 1, removed = 0): ChangedFile => ({ path, added, removed });
const crew = (agents: AgentNode[], running: RunningTool[] = []): Crew => ({ agents, running });
const board = (tasks: BoardTask[]): Board => ({ orchestrate: true, tasks });

const graph = (over: Partial<GraphInput>, dir: Direction = "across") =>
  buildGraph({ roster: [], crew: null, board: null, files: null, ...over }, dir);
const byId = (nodes: GraphNode[], id: string) => {
  const n = nodes.find((x) => x.id === id);
  if (n === undefined) throw new Error(`no node ${id}`);
  return n;
};
const edgeIds = (edges: GraphEdge[]) => edges.map((e) => `${e.kind} ${e.from} ${e.to}`).sort();
const overlaps = (a: GraphNode, b: GraphNode) => a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;

// The room the screenshots show: two members, a subagent each, a race
// after a mapped limiter, and one agent editing a file now.
const rich: GraphInput = {
  roster: [line("claude-1", true), line("codex-1")],
  crew: crew(
    [
      node("claude-1", { status: "tool", model: "opus", contextTokens: 92_000, contextWindow: 200_000, costUsd: 1.84, leases: "pkg/remote/auth/** (exclusive)" }),
      node("codex-1", { status: "approval", model: "gpt-5-codex" }),
      node("claude-sub", { kind: "claude", status: "working", depth: 1, parent: "claude-1" }),
      node("codex-sub", { kind: "codex", status: "exited", depth: 1, parent: "codex-1" }),
    ],
    [tool("claude-1", "Edit", "pkg/remote/auth/limit.go"), tool("claude-sub", "Bash", "go test ./pkg/remote/auth", { command: true })],
  ),
  board: board([
    task("t1", { title: "Map the limiter", status: "done", owner: "claude-1", files: ["pkg/remote/auth/limit.go"] }),
    task("t2", { title: "Race the fix", status: "running", race: true, deps: ["t1"] }),
    task("t3", { status: "running", entry: true, owner: "claude-sub", files: ["pkg/remote/auth/limit.go", "pkg/remote/auth/limit_test.go"] }),
    task("t4", { status: "done", entry: true, owner: "codex-sub", files: ["pkg/remote/auth/limit.go"] }),
    task("t5", { title: "Document it", owner: "codex-1", deps: ["t1"], files: ["README.md"] }),
  ]),
  files: [file("pkg/remote/auth/limit.go", 3, 1), file("pkg/remote/auth/limit_test.go", 12), file("main.go", 2, 2)],
};

describe("globMatch", () => {
  test("** spans folders, * and ? stay in one", () => {
    expect(globMatch("pkg/remote/**", "pkg/remote/auth/limit.go")).toBe(true);
    expect(globMatch("pkg/remote/**", "pkg/rpc/a.go")).toBe(false);
    expect(globMatch("**/*.go", "main.go")).toBe(true);
    expect(globMatch("**/*.go", "a/b/c.go")).toBe(true);
    expect(globMatch("pkg/*.go", "pkg/a/b.go")).toBe(false);
    expect(globMatch("pkg/?.go", "pkg/a.go")).toBe(true);
    expect(globMatch("a.b", "aXb")).toBe(false);
  });

  test("a plain path is itself, or a folder of what is under it", () => {
    expect(globMatch("docs/a.md", "docs/a.md")).toBe(true);
    expect(globMatch("docs", "docs/a.md")).toBe(true);
    expect(globMatch("docs/", "docs/a.md")).toBe(true);
    expect(globMatch("doc", "docs/a.md")).toBe(false);
  });
});

describe("editTarget", () => {
  const known = ["pkg/remote/auth/limit.go", "README.md"];
  test("an edit names a known file, whole or relative", () => {
    expect(editTarget(tool("a", "Edit", "pkg/remote/auth/limit.go"), known)).toBe("pkg/remote/auth/limit.go");
    expect(editTarget(tool("a", "apply_patch", "/home/me/deck/README.md"), known)).toBe("README.md");
    expect(editTarget(tool("a", "Write", "auth/limit.go"), known)).toBe("pkg/remote/auth/limit.go");
  });

  test("a new file is the path it names", () => {
    expect(editTarget(tool("a", "Write", "./docs/new.md"), known)).toBe("docs/new.md");
  });

  test("a command, a read, a subagent or prose edits nothing", () => {
    expect(editTarget(tool("a", "Bash", "go test ./..."), known)).toBeNull();
    expect(editTarget(tool("a", "Read", "README.md"), known)).toBeNull();
    expect(editTarget(tool("a", "Edit", "README.md", { subagent: true }), known)).toBeNull();
    expect(editTarget(tool("a", "Edit", "the limiter"), known)).toBeNull();
  });
});

describe("buildGraph: nodes", () => {
  test("a named member's card carries its role, from the roster or the tree", () => {
    const g = graph({
      roster: [{ ...line("charles"), kind: "claude", role: "reviewer", profile: "charles" }],
      crew: crew([node("charles", { kind: "claude" }), node("ada", { kind: "codex", depth: 1, parent: "charles", role: "tests" })]),
    });
    expect(byId(g.nodes, "agent:charles")).toMatchObject({ agentKind: "claude", role: "reviewer" });
    expect(byId(g.nodes, "agent:ada")).toMatchObject({ agentKind: "codex", role: "tests" });
  });

  test("only you is empty", () => {
    const g = graph({});
    expect(g.nodes.map((n) => n.id)).toEqual(["you"]);
    expect(isEmpty(g)).toBe(true);
    expect(isEmpty(graph({ roster: [line("claude-1")] }))).toBe(false);
  });

  test("the roster merges with the tree; subagents come from the tree", () => {
    const g = graph(rich);
    const agents = g.nodes.filter((n) => n.kind === "agent");
    expect(agents.map((a) => a.id).sort()).toEqual(["agent:claude-1", "agent:claude-sub", "agent:codex-1", "agent:codex-sub"]);
    const claude = byId(g.nodes, "agent:claude-1");
    expect(claude).toMatchObject({ status: "working", model: "opus", contextPct: 46, costUsd: 1.84, depth: 0, parent: null });
    expect(claude.kind === "agent" && claude.leases).toEqual(["pkg/remote/auth/**"]);
    expect(byId(g.nodes, "agent:codex-1")).toMatchObject({ status: "waiting" });
    expect(byId(g.nodes, "agent:claude-sub")).toMatchObject({ depth: 1, parent: "agent:claude-1", agentKind: "claude" });
    // A subagent that exited stays on the map of the work it did.
    expect(byId(g.nodes, "agent:codex-sub")).toMatchObject({ status: "exited" });
  });

  test("a card that runs a tool is taller, to show it", () => {
    const g = graph(rich);
    expect(byId(g.nodes, "agent:claude-1").h).toBe(SIZE.agentTool.h);
    expect(byId(g.nodes, "agent:codex-1").h).toBe(SIZE.agent.h);
  });

  test("without a tree, the roster says who works", () => {
    const g = graph({ roster: [line("claude-1", true), line("codex-1")] });
    expect(byId(g.nodes, "agent:claude-1")).toMatchObject({ status: "working" });
    expect(byId(g.nodes, "agent:codex-1")).toMatchObject({ status: "idle" });
  });

  test("a loop of parents puts its agents at the top", () => {
    const g = graph({ crew: crew([node("a-1", { parent: "b-1" }), node("b-1", { parent: "a-1" }), node("c-1", { parent: "a-1" })]) });
    expect(byId(g.nodes, "agent:a-1")).toMatchObject({ parent: null, depth: 0 });
    expect(byId(g.nodes, "agent:b-1")).toMatchObject({ parent: null, depth: 0 });
    expect(byId(g.nodes, "agent:c-1")).toMatchObject({ parent: "agent:a-1", depth: 1 });
  });

  test("a parent the room does not know makes a member", () => {
    const g = graph({ crew: crew([node("a-1", { parent: "ghost", depth: 1 })]) });
    expect(byId(g.nodes, "agent:a-1")).toMatchObject({ parent: null, depth: 0 });
  });

  test("files: the changed ones, those tasks name, and leases", () => {
    const g = graph(rich);
    const files = g.nodes.flatMap((n) => (n.kind === "file" ? [n] : []));
    expect(files.map((f) => f.path).sort()).toEqual(["README.md", "main.go", "pkg/remote/auth/limit.go", "pkg/remote/auth/limit_test.go"]);
    const limit = byId(g.nodes, "file:pkg/remote/auth/limit.go");
    expect(limit).toMatchObject({ changed: true, added: 3, removed: 1, holders: ["claude-1"] });
    expect(byId(g.nodes, "file:README.md")).toMatchObject({ changed: false, holders: [] });
  });

  test("a lease that matches no file shows as its pattern", () => {
    const g = graph({ roster: [line("claude-1")], crew: crew([node("claude-1", { leases: "pkg/rpc/** (exclusive); docs/a.md" })]) });
    expect(byId(g.nodes, "file:pkg/rpc/**")).toMatchObject({ glob: true, holders: ["claude-1"] });
    expect(byId(g.nodes, "file:docs/a.md")).toMatchObject({ glob: false, holders: ["claude-1"] });
  });

  test("live leases win over the tree's", () => {
    const g = graph({
      roster: [line("claude-1")],
      crew: crew([node("claude-1", { leases: "old/**" })]),
      leases: [{ agent: "claude-1", paths: ["new.go"], exclusive: true }],
    });
    expect(g.nodes.some((n) => n.id === "file:old/**")).toBe(false);
    expect(byId(g.nodes, "file:new.go")).toMatchObject({ holders: ["claude-1"] });
  });

  test("many changed files no one is linked to fold into one node", () => {
    const files = Array.from({ length: FOLD_AT + 1 }, (_, i) => file(`f${i}.go`, i, 1));
    const g = graph({ roster: [line("claude-1"), line("codex-1")], files });
    expect(g.nodes.some((n) => n.kind === "file")).toBe(false);
    expect(byId(g.nodes, FOLD)).toMatchObject({ paths: files.map((f) => f.path), added: 10, removed: 5 });
    // A few stay as they are.
    const few = graph({ roster: [line("claude-1"), line("codex-1")], files: files.slice(0, FOLD_AT) });
    expect(few.nodes.filter((n) => n.kind === "file")).toHaveLength(FOLD_AT);
  });
});

describe("buildGraph: edges", () => {
  test("leads, spawned, owns, after, touches, holds and edits", () => {
    const g = graph(rich);
    expect(edgeIds(g.edges)).toEqual(
      [
        "leads you agent:claude-1",
        "leads you agent:codex-1",
        "spawned agent:claude-1 agent:claude-sub",
        "spawned agent:codex-1 agent:codex-sub",
        "owns agent:claude-1 task:t1",
        "owns agent:claude-sub task:t3",
        "owns agent:codex-sub task:t4",
        "owns agent:codex-1 task:t5",
        "after task:t1 task:t2",
        // A race's entries come after the race.
        "after task:t2 task:t3",
        "after task:t2 task:t4",
        "after task:t1 task:t5",
        "touches task:t1 file:pkg/remote/auth/limit.go",
        "touches task:t3 file:pkg/remote/auth/limit.go",
        "touches task:t3 file:pkg/remote/auth/limit_test.go",
        "touches task:t4 file:pkg/remote/auth/limit.go",
        "touches task:t5 file:README.md",
        // Editing it now wins over holding it.
        "edits agent:claude-1 file:pkg/remote/auth/limit.go",
        "holds agent:claude-1 file:pkg/remote/auth/limit_test.go",
      ].sort(),
    );
    const live = g.edges.filter((e) => e.live);
    expect(live.map((e) => e.id)).toEqual(["edits:agent:claude-1>file:pkg/remote/auth/limit.go"]);
    // An agent's edges take its hue; you and the tasks are neutral.
    expect(g.edges.find((e) => e.kind === "spawned")?.tint).toBe("agent:claude-1");
    expect(g.edges.find((e) => e.kind === "leads")?.tint).toBeNull();
    expect(g.edges.find((e) => e.kind === "after")?.tint).toBeNull();
  });

  test("main.go, changed by no one known, stays loose among several agents", () => {
    const g = graph(rich);
    expect(g.edges.some((e) => e.to === "file:main.go")).toBe(false);
  });

  test("with one agent in the room, the changes are its", () => {
    const g = graph({ roster: [line("claude-1")], files: [file("a.go"), file("b.go")] });
    expect(edgeIds(g.edges)).toEqual(["edits agent:claude-1 file:a.go", "edits agent:claude-1 file:b.go", "leads you agent:claude-1"]);
    expect(g.edges.every((e) => !e.live)).toBe(true);
  });

  test("a file an agent edits now joins the graph", () => {
    const g = graph({ roster: [line("claude-1")], crew: crew([node("claude-1", { status: "tool" })], [tool("claude-1", "Write", "docs/new.md")]) });
    expect(byId(g.nodes, "file:docs/new.md")).toMatchObject({ changed: false });
    expect(g.edges.find((e) => e.kind === "edits")).toMatchObject({ live: true, from: "agent:claude-1", to: "file:docs/new.md" });
  });

  test("deps on tasks the board lacks, and on a task itself, draw nothing", () => {
    const g = graph({ board: board([task("t1", { deps: ["t1", "gone"] })]) });
    expect(g.edges).toEqual([]);
  });

  test("an owner the room does not know draws no edge", () => {
    const g = graph({ board: board([task("t1", { owner: "ghost" })]) });
    expect(g.edges).toEqual([]);
  });
});

describe("layout", () => {
  test("left to right: you, agents by depth, tasks by level, files", () => {
    const g = graph(rich);
    const layer = (id: string) => byId(g.nodes, id).layer;
    expect(layer("you")).toBe(0);
    expect(layer("agent:claude-1")).toBe(1);
    expect(layer("agent:codex-1")).toBe(1);
    expect(layer("agent:claude-sub")).toBe(2);
    expect(layer("task:t1")).toBe(3);
    expect(layer("task:t2")).toBe(4);
    expect(layer("task:t5")).toBe(4);
    expect(layer("task:t3")).toBe(5);
    expect(layer("file:main.go")).toBe(6);
    expect(g.layers.map((c) => c.label)).toEqual(["", "Agents", "Subagents", "Tasks", "", "", "Files"]);
    // Each column starts after the widest node of the one before.
    for (let i = 1; i < g.layers.length; i++) {
      const prev = g.layers[i - 1]!;
      expect(g.layers[i]!.at).toBe(prev.at + prev.size + COLUMN_GAP);
    }
    expect(g.dir).toBe("across");
  });

  test("down: the same layers from top to bottom, each a row", () => {
    const g = graph(rich, "down");
    expect(g.dir).toBe("down");
    expect(byId(g.nodes, "agent:claude-sub").layer).toBe(2);
    for (let i = 1; i < g.layers.length; i++) {
      const prev = g.layers[i - 1]!;
      expect(g.layers[i]!.at).toBe(prev.at + prev.size + LAYER_GAP);
    }
    // A row's cards stand side by side, SIBLING_GAP apart, none overlapping.
    for (const a of g.nodes) for (const b of g.nodes) if (a !== b) expect(overlaps(a, b), `${a.id} / ${b.id}`).toBe(false);
    for (const c of g.layers) {
      const row = g.nodes.filter((n) => n.layer === c.layer).sort((a, b) => a.x - b.x);
      for (let i = 1; i < row.length; i++) expect(row[i]!.x - (row[i - 1]!.x + row[i - 1]!.w)).toBeGreaterThanOrEqual(SIBLING_GAP - 1);
      // Each card sits in the middle of its row's height.
      for (const n of row) expect(n.y + n.h / 2).toBeCloseTo(c.at + c.size / 2, 0);
    }
  });

  test("no empty column: without tasks, the files follow the agents", () => {
    const g = graph({ roster: [line("claude-1")], files: [file("a.go")] });
    expect(byId(g.nodes, "file:a.go").layer).toBe(2);
  });

  test("a loop of deps still lays out", () => {
    const g = graph({ board: board([task("a", { deps: ["b"] }), task("b", { deps: ["a"] })]) });
    expect(g.nodes.filter((n) => n.kind === "task")).toHaveLength(2);
  });

  test("no two nodes overlap, and each column keeps ROW_GAP", () => {
    const g = graph(rich);
    for (const a of g.nodes) for (const b of g.nodes) if (a !== b) expect(overlaps(a, b), `${a.id} / ${b.id}`).toBe(false);
    for (const c of g.layers) {
      const col = g.nodes.filter((n) => n.layer === c.layer).sort((a, b) => a.y - b.y);
      for (let i = 1; i < col.length; i++) expect(col[i]!.y - (col[i - 1]!.y + col[i - 1]!.h)).toBeGreaterThanOrEqual(ROW_GAP - 1);
    }
  });

  test("the graph is centred on y = 0, in whole units", () => {
    const g = graph(rich);
    expect(Math.abs(g.bounds.y + g.bounds.h / 2)).toBeLessThanOrEqual(1);
    expect(g.nodes.every((n) => Number.isInteger(n.y))).toBe(true);
  });

  test("a lone chain lies level", () => {
    const g = graph({ roster: [line("claude-1")], crew: crew([node("claude-1")]), board: board([task("t1", { owner: "claude-1" })]) });
    const mid = (id: string) => byId(g.nodes, id).y + byId(g.nodes, id).h / 2;
    expect(Math.abs(mid("you") - mid("agent:claude-1"))).toBeLessThanOrEqual(1);
    expect(Math.abs(mid("agent:claude-1") - mid("task:t1"))).toBeLessThanOrEqual(1);
  });

  test("the same input lays out the same", () => {
    expect(graph(rich)).toEqual(graph(rich));
  });

  test("a long edge runs in a lane through each layer it crosses, clear of the cards", () => {
    const g = graph(rich);
    const edit = g.edges.find((e) => e.kind === "edits")!;
    const [from, to] = [byId(g.nodes, edit.from), byId(g.nodes, edit.to)];
    expect(edit.via).toHaveLength(to.layer - from.layer - 1);
    edit.via!.forEach((v, i) => {
      const c = g.layers[from.layer + i + 1]!;
      // Through the whole of its column, at one height.
      expect([v.x, v.w, v.h]).toEqual([c.at, c.size, 0]);
      for (const n of g.nodes) if (n.layer === c.layer) expect(v.y > n.y && v.y < n.y + n.h, `${n.id} at ${v.y}`).toBe(false);
    });
    // A short edge has none.
    expect(g.edges.find((e) => e.kind === "leads")!.via).toBeUndefined();
    // Lanes widen the bounds when they pass outside the cards.
    for (const e of g.edges) for (const v of e.via ?? []) expect(v.y >= g.bounds.y && v.y <= g.bounds.y + g.bounds.h).toBe(true);
  });

  test("edges of one kind from one node share their lanes until they part", () => {
    const g = graph({
      roster: [line("claude-1")],
      crew: crew([node("claude-1", { leases: "src/**" })]),
      board: board([task("t1")]),
      files: [file("src/a.go"), file("src/b.go")],
    });
    const holds = g.edges.filter((e) => e.kind === "holds");
    expect(holds).toHaveLength(2);
    expect(holds[0]!.via).toEqual(holds[1]!.via);
    expect(holds[0]!.via).toHaveLength(1);
  });

  test("the rich room draws without a crossing", () => {
    const g = graph(rich);
    const layers: GraphNode[][] = g.layers.map((c) => g.nodes.filter((n) => n.layer === c.layer).sort((a, b) => a.y - b.y));
    expect(crossings(layers, g.edges)).toBe(0);
  });
});

describe("orderLayers", () => {
  const n = (id: string, layer: number): GraphNode => ({ kind: "you", id, layer, x: 0, y: 0, w: 10, h: 10 });
  const e = (from: string, to: string): GraphEdge => ({ id: `${from}>${to}`, from, to, kind: "after", live: false, tint: null });

  test("barycentres undo a crossing", () => {
    const layers = [
      [n("a", 0), n("b", 0)],
      [n("x", 1), n("y", 1)],
    ];
    const edges = [e("a", "y"), e("b", "x")];
    // A crossing between two cards counts 4.
    expect(crossings(layers, edges)).toBe(4);
    const out = orderLayers(layers, edges);
    expect(crossings(out, edges)).toBe(0);
    // The first layer keeps its order; the second follows it.
    expect(out.map((l) => l.map((x) => x.id))).toEqual([
      ["a", "b"],
      ["y", "x"],
    ]);
  });

  test("three layers untangle", () => {
    const layers = [
      [n("a", 0), n("b", 0), n("c", 0)],
      [n("p", 1), n("q", 1), n("r", 1)],
      [n("u", 2), n("v", 2), n("w", 2)],
    ];
    const edges = [e("a", "r"), e("b", "q"), e("c", "p"), e("p", "w"), e("q", "v"), e("r", "u")];
    expect(crossings(layers, edges)).toBe(24);
    expect(crossings(orderLayers(layers, edges), edges)).toBe(0);
  });

  test("a crossing of lanes counts less than one of cards", () => {
    const via = (id: string, layer: number): GraphNode => ({ ...n(id, layer), kind: "via" }) as unknown as GraphNode;
    const layers = [
      [n("a", 0), via("b", 0)],
      [n("x", 1), via("y", 1)],
    ];
    expect(crossings(layers, [e("a", "y"), e("b", "x")])).toBe(1);
  });

  test("nodes without neighbours keep their place", () => {
    const layers = [[n("a", 0)], [n("x", 1), n("y", 1), n("z", 1)]];
    expect(orderLayers(layers, []).map((l) => l.map((x) => x.id))).toEqual([["a"], ["x", "y", "z"]]);
  });
});

describe("edgePath", () => {
  test("from the right side of one to the left side of the other", () => {
    expect(edgePath({ x: 0, y: 0, w: 100, h: 40 }, { x: 200, y: 60, w: 100, h: 20 })).toBe("M100 20 C150 20 150 70 200 70");
  });

  test("down, from the bottom of one to the top of the other", () => {
    expect(edgePath({ x: 0, y: 0, w: 100, h: 40 }, { x: 60, y: 140, w: 100, h: 20 }, "down")).toBe("M50 40 C50 90 110 90 110 140");
  });

  test("through a lane: an S to it, a straight run along it, an S out", () => {
    const lane = { x: 150, y: 80, w: 100, h: 0 };
    expect(edgePath({ x: 0, y: 0, w: 100, h: 40 }, { x: 300, y: 60, w: 100, h: 20 }, "across", [lane])).toBe(
      "M100 20 C132 20 118 80 150 80 L250 80 C282 80 268 70 300 70",
    );
  });

  test("short gaps keep a reach of 32", () => {
    expect(edgePath({ x: 0, y: 0, w: 10, h: 10 }, { x: 20, y: 0, w: 10, h: 10 })).toBe("M10 5 C42 5 -12 5 20 5");
  });
});

describe("neighbourhood", () => {
  test("the node and every node one edge away", () => {
    const g = graph(rich);
    expect([...neighbourhood(g.edges, "agent:claude-sub")].sort()).toEqual(["agent:claude-1", "agent:claude-sub", "task:t3"]);
  });
});

describe("view", () => {
  const pad = { top: 20, right: 20, bottom: 20, left: 20 };

  test("fit centres the graph and never zooms past 100%", () => {
    expect(fitView({ x: 0, y: -50, w: 200, h: 100 }, 1000, 600, pad)).toEqual({ k: 1, x: 400, y: 300 });
  });

  test("fit shrinks a large graph into the padding", () => {
    const v = fitView({ x: 0, y: 0, w: 1920, h: 560 }, 1000, 600, pad);
    expect(v.k).toBe(0.5);
    expect(v.x).toBe(20);
    expect(v.y).toBe(160);
  });

  test("fit stops at the least zoom", () => {
    expect(fitView({ x: 0, y: 0, w: 10_000, h: 100 }, 1000, 600, pad).k).toBe(ZOOM_MIN);
  });

  test("zoom keeps the point under the pointer", () => {
    const v = { x: 100, y: 50, k: 1 };
    const z = zoomAt(v, 2, 300, 250);
    // The graph point under (300, 250) was (200, 200); it still is.
    expect((300 - z.x) / z.k).toBe(200);
    expect((250 - z.y) / z.k).toBe(200);
    expect(z.k).toBe(2);
  });

  test("zoom stays between the least and the most", () => {
    expect(zoomAt({ x: 0, y: 0, k: 1 }, 9, 0, 0).k).toBe(ZOOM_MAX);
    expect(zoomAt({ x: 0, y: 0, k: 1 }, 0.01, 0, 0).k).toBe(ZOOM_MIN);
  });
});

describe("stepFocus", () => {
  const g = graph(rich);

  test("up and down along a column, stopping at its ends", () => {
    const col = g.nodes.filter((n) => n.layer === 1).sort((a, b) => a.y - b.y);
    expect(stepFocus(g.nodes, col[0]!.id, "ArrowDown")).toBe(col[1]!.id);
    expect(stepFocus(g.nodes, col[0]!.id, "ArrowUp")).toBe(col[0]!.id);
  });

  test("left and right to the nearest node in the next column", () => {
    expect(stepFocus(g.nodes, "you", "ArrowRight")).toMatch(/^agent:/);
    expect(stepFocus(g.nodes, "agent:claude-sub", "ArrowLeft")).toBe("agent:claude-1");
    expect(stepFocus(g.nodes, "you", "ArrowLeft")).toBe("you");
  });

  test("down: left and right along a row, up and down between rows", () => {
    const d = graph(rich, "down");
    const row = d.nodes.filter((n) => n.layer === 1).sort((a, b) => a.x - b.x);
    expect(stepFocus(d.nodes, row[0]!.id, "ArrowRight", "down")).toBe(row[1]!.id);
    expect(stepFocus(d.nodes, "you", "ArrowDown", "down")).toMatch(/^agent:/);
    expect(stepFocus(d.nodes, "agent:claude-sub", "ArrowUp", "down")).toBe("agent:claude-1");
  });

  test("another key is not a move; an unknown node starts at the first", () => {
    expect(stepFocus(g.nodes, "you", "Enter")).toBeNull();
    expect(stepFocus(g.nodes, "gone", "ArrowDown")).toBe(g.nodes[0]!.id);
  });
});

describe("chooseDirection", () => {
  const wide = { x: 0, y: 0, w: 1200, h: 300 };
  const tall = { x: 0, y: 0, w: 600, h: 700 };
  test("across in a wide canvas, down in a tall narrow one", () => {
    expect(chooseDirection(wide, tall, 1000, 700)).toBe("across");
    expect(chooseDirection(wide, tall, 480, 800)).toBe("down");
  });

  test("across when down shows it barely larger", () => {
    expect(chooseDirection({ x: 0, y: 0, w: 1000, h: 100 }, { x: 0, y: 0, w: 900, h: 100 }, 500, 800)).toBe("across");
  });
});

describe("graphSummary", () => {
  test("counts the agents at work, the tasks done and the files", () => {
    expect(graphSummary(graph(rich))).toEqual({ agents: 4, working: 2, tasks: 5, done: 2, files: 4 });
  });
});
