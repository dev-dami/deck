import type { AgentLine, Message } from "@web/protocol";
import { describe, expect, test } from "vitest";
import type { AgentNode } from "./crew";
import { buildLanes, filterLane, fitLanes, type Lane, laneOf, leaseCount, lineCount, pinnedLane, shortId } from "./lanes";

const line = (id: string, busy = false): AgentLine => ({ id, name: id, kind: id.replace(/-\d+$/, ""), busy });

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
  ...over,
});

const msg = (role: Message["role"], text: string, agent?: string): Message => ({
  id: `${role}-${text}`,
  session_id: "s",
  role,
  ...(agent === undefined ? {} : { agent }),
  created_at: "",
  updated_at: "",
  finished: true,
  parts: [{ type: "text", text }],
});

describe("buildLanes", () => {
  test("the person first, then the roster merged with the tree, then followers", () => {
    const lanes = buildLanes({
      roster: [line("claude-1", true), line("codex-2")],
      agents: [
        node("claude-1", { status: "tool", model: "opus", contextTokens: 50, contextWindow: 200, leases: "a/**, b.go (exclusive); c/**" }),
        node("codex-sub", { kind: "codex", status: "working", depth: 1 }),
        node("old-sub", { status: "exited", depth: 1 }),
      ],
      shares: { vps: "editor", ana: "observer" },
      waiting: ["codex-2"],
    });
    expect(lanes.map((l) => l.id)).toEqual(["you", "agent:claude-1", "agent:codex-2", "agent:codex-sub", "mate:ana", "mate:vps"]);
    const claude = lanes[1]!;
    expect(claude).toMatchObject({ status: "working", model: "opus", contextPct: 25, leases: 3, agentKind: "claude" });
    // An open approval wins over the roster's busy flag.
    expect(lanes[2]).toMatchObject({ status: "waiting", contextPct: null, leases: 0 });
    expect(lanes[3]).toMatchObject({ depth: 1, agentKind: "codex" });
    expect(lanes[5]).toMatchObject({ kind: "mate", name: "vps", role: "editor" });
  });

  test("without the tree, the roster's busy flag says working or idle", () => {
    const lanes = buildLanes({ roster: [line("claude-1", true), line("codex-1")], agents: null, shares: {}, waiting: [] });
    expect(lanes.map((l) => l.status)).toEqual(["idle", "working", "idle"]);
  });

  test("an error in the tree shows on the lane", () => {
    const lanes = buildLanes({ roster: [line("claude-1")], agents: [node("claude-1", { status: "error" })], shares: {}, waiting: [] });
    expect(lanes[1]?.status).toBe("error");
  });
});

describe("lane fold", () => {
  const agents = (statuses: Lane["status"][]): Lane[] =>
    buildLanes({
      roster: statuses.map((_, i) => line(`claude-${i + 1}`)),
      agents: statuses.map((s, i) => node(`claude-${i + 1}`, { status: s === "waiting" ? "approval" : s })),
      shares: { vps: "observer" },
      waiting: [],
    });
  // Every chip 100px wide, 2px apart, a "+N" of 30px.
  const fit = (lanes: Lane[], width: number) => fitLanes(lanes, () => 100, width, { gap: 2, lines: 2, more: 30 });

  test("chips wrap as flex-wrap lays them out", () => {
    expect(lineCount([], 300, 2)).toBe(0);
    expect(lineCount([100, 100], 202, 2)).toBe(1);
    expect(lineCount([100, 100], 201, 2)).toBe(2);
    expect(lineCount([100, 100, 100, 100, 100], 310, 2)).toBe(2);
    // A chip wider than the row takes a line of its own.
    expect(lineCount([400, 10], 300, 2)).toBe(2);
  });

  test("lanes that fit in two lines all show", () => {
    const { shown, folded } = fit(agents(["idle", "idle", "idle", "idle"]), 310);
    expect(shown).toHaveLength(6);
    expect(folded).toHaveLength(0);
  });

  test("past two lines, teammates fold first, then idle agents from the end", () => {
    // you, eight agents, vps: ten chips; three fit on a line of 310px.
    const lanes = agents(["idle", "idle", "idle", "working", "idle", "idle", "waiting", "idle"]);
    const { shown, folded } = fit(lanes, 310);
    expect(folded.map((l) => l.name)).toEqual(["claude-3", "claude-5", "claude-6", "claude-8", "vps"]);
    expect(shown.map((l) => l.name)).toEqual(["you", "claude-1", "claude-2", "claude-4", "claude-7"]);
  });

  test("the person and the agents that work, wait or failed never fold", () => {
    const lanes = agents(["working", "error", "waiting", "working", "working", "working"]);
    const { shown, folded } = fit(lanes, 210);
    expect(shown.every(pinnedLane)).toBe(true);
    expect(folded.map((l) => l.name)).toEqual(["vps"]);
  });

  test("a row not measured yet shows every lane", () => {
    expect(fit(agents(["idle", "idle", "idle"]), 0).folded).toEqual([]);
  });
});

describe("lane filter", () => {
  const messages = [msg("user", "fix it"), msg("assistant", "on it", "claude-1"), msg("user", "[vps] and the tests"), msg("system", "joined")];

  test("each message belongs to its lane", () => {
    expect(messages.map(laneOf)).toEqual(["you", "agent:claude-1", "mate:vps", null]);
  });

  test("a lane shows its messages alone; none shows all", () => {
    expect(filterLane(messages, null)).toBe(messages);
    expect(filterLane(messages, "agent:claude-1").map((m) => m.id)).toEqual(["assistant-on it"]);
    expect(filterLane(messages, "mate:vps")).toHaveLength(1);
  });
});

test("lease counts and short ids", () => {
  expect(leaseCount("")).toBe(0);
  expect(leaseCount("internal/** (exclusive)")).toBe(1);
  expect(leaseCount("a, b; c (exclusive)")).toBe(3);
});

test("a short id is the last three base36 characters of the id", () => {
  expect(shortId("ses_a7f3e2")).toBe("3e2");
  expect(shortId("ses_01HZ7A3F")).toBe("a3f");
  expect(shortId("ses_main")).toBe("ain");
  // Separators do not count; an id of fewer characters is all of them.
  expect(shortId("a_b-c")).toBe("abc");
  expect(shortId("x9")).toBe("x9");
  expect(shortId("___")).toBe("___");
});
