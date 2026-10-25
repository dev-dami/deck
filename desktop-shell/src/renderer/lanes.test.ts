import type { AgentLine, Message } from "@web/protocol";
import { describe, expect, test } from "vitest";
import type { AgentNode } from "./crew";
import { buildLanes, collapseIdle, filterLane, type Lane, laneOf, leaseCount, shortId } from "./lanes";

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

describe("collapseIdle", () => {
  const agents = (statuses: Lane["status"][]): Lane[] =>
    buildLanes({
      roster: statuses.map((_, i) => line(`claude-${i + 1}`)),
      agents: statuses.map((s, i) => node(`claude-${i + 1}`, { status: s === "waiting" ? "approval" : s })),
      shares: { vps: "observer" },
      waiting: [],
    });

  test("six agents or fewer all show", () => {
    const { shown, idle } = collapseIdle(agents(["idle", "idle", "idle", "idle", "idle", "idle"]));
    expect(shown).toHaveLength(8);
    expect(idle).toHaveLength(0);
  });

  test("past six, idle agents fold and busy ones stay", () => {
    const { shown, idle } = collapseIdle(agents(["idle", "idle", "idle", "idle", "idle", "idle", "idle", "working", "idle"]));
    expect(idle.map((l) => l.name)).toEqual(["claude-7", "claude-9"]);
    expect(shown.map((l) => l.id)).toContain("agent:claude-8");
    // The person and the followers always show.
    expect(shown[0]?.id).toBe("you");
    expect(shown.at(-1)?.id).toBe("mate:vps");
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
  expect(shortId("ses_a7f3e2")).toBe("a7f");
  expect(shortId("main")).toBe("mai");
});
