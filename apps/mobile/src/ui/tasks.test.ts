import { describe, expect, test } from "bun:test";
import type { InboxItem } from "../../../../pkg/remote/web/src/protocol3";
import {
  defaultTab,
  diffLines,
  relativeTime,
  splitMessage,
  splitPath,
  tabCounts,
  taskRows,
} from "./tasks";

const NOW = Date.parse("2026-10-22T12:00:00Z");

function item(over: Partial<InboxItem>): InboxItem {
  return {
    pj: "prj_1",
    session_id: "ses_1",
    title: "Fix flaky test",
    state: "done",
    approvals: 0,
    last_activity: "2026-10-22T11:55:00Z",
    summary: "",
    unread: false,
    ...over,
  };
}

const projects = [{ id: "prj_1", name: "deck" }];

describe("task rows", () => {
  test("counts each tab and opens on what needs an answer", () => {
    const items = [
      item({ session_id: "a", state: "needs_you", approvals: 1 }),
      item({ session_id: "b", state: "running" }),
      item({ session_id: "c" }),
    ];
    expect(tabCounts(items)).toEqual({ needs: 1, running: 1, done: 1 });
    expect(defaultTab(items)).toBe("needs");
    expect(defaultTab(items.slice(1))).toBe("running");
    expect(defaultTab([])).toBe("done");
  });

  test("rows for a tab are newest first with a status badge", () => {
    const items = [
      item({
        session_id: "old",
        state: "needs_you",
        approvals: 1,
        last_activity: "2026-10-22T10:00:00Z",
      }),
      item({
        session_id: "new",
        state: "needs_you",
        approvals: 3,
        summary: "Wants to run go test",
      }),
      item({ session_id: "run", state: "running" }),
    ];
    const rows = taskRows(items, projects, "needs", NOW);
    expect(rows.map((r) => r.sessionId)).toEqual(["new", "old"]);
    expect(rows[0]?.badge).toEqual({ text: "3 approvals", tone: "warn" });
    expect(rows[0]?.subtitle).toBe("deck · Wants to run go test");
    expect(rows[1]?.badge).toEqual({ text: "Approve", tone: "warn" });
    expect(rows[1]?.subtitle).toBe("deck · 2h ago");
    expect(taskRows(items, projects, "running", NOW)[0]?.badge).toEqual({
      text: "Running",
      tone: "info",
    });
  });

  test("an untitled task in an unknown project still reads well", () => {
    const [row] = taskRows([item({ title: "  ", pj: "gone" })], projects, "done", NOW);
    expect(row?.title).toBe("Untitled task");
    expect(row?.subtitle).toBe("Project · 5m ago");
    expect(row?.badge).toBeNull();
  });
});

describe("relative time", () => {
  test("covers minutes, hours and days", () => {
    expect(relativeTime("2026-10-22T11:59:40Z", NOW)).toBe("just now");
    expect(relativeTime("2026-10-22T11:15:00Z", NOW)).toBe("45m ago");
    expect(relativeTime("2026-10-22T03:00:00Z", NOW)).toBe("9h ago");
    expect(relativeTime("2026-10-19T12:00:00Z", NOW)).toBe("3d ago");
    expect(relativeTime("not a date", NOW)).toBe("");
  });
});

describe("diff lines", () => {
  test("classifies hunks, additions, deletions and context", () => {
    const lines = diffLines(
      "--- a/x.go\n+++ b/x.go\n@@ -1,2 +1,2 @@\n func A() {\n-\told()\n+\tnew()\n }\n",
    );
    expect(lines).toEqual([
      { kind: "meta", text: "--- a/x.go" },
      { kind: "meta", text: "+++ b/x.go" },
      { kind: "hunk", text: "@@ -1,2 +1,2 @@" },
      { kind: "ctx", text: "func A() {" },
      { kind: "del", text: "    old()" },
      { kind: "add", text: "    new()" },
      { kind: "ctx", text: "}" },
    ]);
  });

  test("splits a path into its file and folder", () => {
    expect(splitPath("pkg/remote/server/rig_test.go")).toEqual({
      name: "rig_test.go",
      dir: "pkg/remote/server",
    });
    expect(splitPath("go.mod")).toEqual({ name: "go.mod", dir: "" });
  });
});

describe("split message", () => {
  test("keeps prose and folds tool work into steps", () => {
    const { prose, steps } = splitMessage([
      { type: "text", text: "Looking at the test." },
      { type: "tool", id: "t1", name: "Bash", output: "\nFAIL TestReconnect\nmore" },
      { type: "tool", id: "t2", name: "Edit", diff: "@@ -1 +1 @@", is_error: true },
      { type: "reasoning", text: "hidden" },
      { type: "text", text: "  " },
      { type: "text", text: "Fixed it." },
    ]);
    expect(prose).toEqual(["Looking at the test.", "Fixed it."]);
    expect(steps).toEqual([
      { key: "tool:t1", label: "Bash", detail: "FAIL TestReconnect", error: false },
      { key: "tool:t2", label: "Edit", detail: "@@ -1 +1 @@", error: true },
    ]);
  });
});
