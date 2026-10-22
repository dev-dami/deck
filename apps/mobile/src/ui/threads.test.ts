import { describe, expect, test } from "bun:test";
import type { InboxItem, ProjectRef } from "../../../../pkg/remote/web/src/protocol3";
import { groupByProject, SETTLE_AFTER_MS, threadStatus } from "./threads";

const NOW = Date.parse("2026-10-22T12:00:00Z");
const ago = (ms: number): string => new Date(NOW - ms).toISOString();

function item(id: string, over: Partial<InboxItem> = {}): InboxItem {
  return {
    pj: "p1",
    session_id: id,
    title: id,
    state: "done",
    approvals: 0,
    last_activity: ago(1000),
    summary: "",
    unread: false,
    ...over,
  };
}

const projects: ProjectRef[] = [
  { id: "p1", name: "api", online: true },
  { id: "p2", name: "web", online: true },
  { id: "p3", name: "docs", online: false },
];

describe("threadStatus", () => {
  test("an open approval wins over everything", () => {
    expect(threadStatus(item("a", { state: "running", approvals: 1 }))).toBe("approval");
    expect(threadStatus(item("a", { state: "needs_you" }))).toBe("approval");
  });

  test("working, then unread, then nothing", () => {
    expect(threadStatus(item("a", { state: "running" }))).toBe("working");
    expect(threadStatus(item("a", { unread: true }))).toBe("done");
    expect(threadStatus(item("a"))).toBeNull();
  });
});

describe("groupByProject", () => {
  test("puts each thread under its project, newest first", () => {
    const groups = groupByProject(
      [item("old", { last_activity: ago(5000) }), item("new", { last_activity: ago(10) })],
      projects,
      NOW,
    );
    const api = groups.find((g) => g.id === "p1");
    expect(api?.live.map((i) => i.session_id)).toEqual(["new", "old"]);
  });

  test("folds quiet finished threads away but keeps ones that need attention", () => {
    const quiet = ago(SETTLE_AFTER_MS + 1000);
    const groups = groupByProject(
      [
        item("stale", { last_activity: quiet }),
        item("unread", { last_activity: quiet, unread: true }),
        item("waiting", { last_activity: quiet, state: "needs_you", approvals: 2 }),
        item("working", { last_activity: quiet, state: "running" }),
      ],
      projects,
      NOW,
    );
    const api = groups.find((g) => g.id === "p1");
    expect(api?.settled.map((i) => i.session_id)).toEqual(["stale"]);
    expect(api?.live.map((i) => i.session_id).sort()).toEqual(["unread", "waiting", "working"]);
    expect(api?.needsYou).toBe(1);
  });

  test("orders projects: needs you, then latest activity, then empty ones", () => {
    const groups = groupByProject(
      [
        item("a", { pj: "p1", last_activity: ago(100) }),
        item("b", { pj: "p2", last_activity: ago(9000), state: "needs_you", approvals: 1 }),
      ],
      projects,
      NOW,
    );
    expect(groups.map((g) => g.id)).toEqual(["p2", "p1", "p3"]);
  });

  test("keeps threads of a project the computer no longer lists", () => {
    const groups = groupByProject([item("x", { pj: "gone" })], projects, NOW);
    const orphan = groups.find((g) => g.id === "gone");
    expect(orphan?.name).toBe("Project");
    expect(orphan?.live).toHaveLength(1);
  });
});
