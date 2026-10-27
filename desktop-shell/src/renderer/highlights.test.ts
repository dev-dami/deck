import { describe, expect, test } from "vitest";
import type { AgentSummary, HighlightItem } from "../shared/bridge";
import { closedItems, groupHighlights, highlightCounts, pinTitle, stripItem, summaryRows } from "./highlights";

const item = (id: string, over: Partial<HighlightItem> = {}): HighlightItem => ({
  id,
  session_id: "s1",
  agent: "claude",
  key: id,
  kind: "finding",
  title: id,
  detail: "",
  source: null,
  state: "open",
  created_at: 1,
  updated_at: 1,
  unread: false,
  ...over,
});

describe("groupHighlights", () => {
  test("orders groups Needs you, Blockers, Key findings and drops empty ones", () => {
    const groups = groupHighlights([item("f"), item("n", { kind: "needs_you" })]);
    expect(groups.map((g) => g.label)).toEqual(["Needs you", "Key findings"]);
  });

  test("shows only open items, unread first then newest", () => {
    const [g] = groupHighlights([
      item("old", { updated_at: 1 }),
      item("new", { updated_at: 5 }),
      item("fresh", { updated_at: 2, unread: true }),
      item("done", { state: "resolved", updated_at: 9 }),
    ]);
    expect(g?.items.map((i) => i.id)).toEqual(["fresh", "new", "old"]);
  });
});

describe("highlightCounts", () => {
  test("counts unresolved items, not unread ones", () => {
    expect(highlightCounts([item("a"), item("b", { unread: true }), item("c", { state: "dismissed" })])).toEqual({ open: 2, unread: true });
  });

  test("an unread item that is resolved does not light the dot", () => {
    expect(highlightCounts([item("a", { state: "resolved", unread: true })])).toEqual({ open: 0, unread: false });
  });

  test("a seen blocker still counts as open", () => {
    expect(highlightCounts([item("b", { kind: "blocker", unread: false })]).open).toBe(1);
  });
});

describe("stripItem", () => {
  test("prefers Needs you over Blockers", () => {
    expect(stripItem([item("b", { kind: "blocker" }), item("n", { kind: "needs_you" })])?.id).toBe("n");
  });

  test("never shows a finding or a closed item", () => {
    expect(stripItem([item("f"), item("r", { kind: "blocker", state: "resolved" })])).toBeNull();
  });

  test("takes one item, the newest of the kind", () => {
    expect(stripItem([item("a", { kind: "blocker", updated_at: 1 }), item("b", { kind: "blocker", updated_at: 2 })])?.id).toBe("b");
  });
});

describe("closedItems", () => {
  test("lists resolved and dismissed items newest first", () => {
    const out = closedItems([item("a", { state: "resolved", updated_at: 1 }), item("o"), item("b", { state: "dismissed", updated_at: 3 })]);
    expect(out.map((i) => i.id)).toEqual(["b", "a"]);
  });
});

describe("pinTitle", () => {
  test("takes the first real line without markdown marks", () => {
    expect(pinTitle("\n\n## The limiter leaks\nmore")).toBe("The limiter leaks");
    expect(pinTitle("- **Found** it")).toBe("Found it");
  });

  test("clips to the title limit and names an empty message", () => {
    expect(pinTitle("x".repeat(300))).toHaveLength(160);
    expect(pinTitle("   ")).toBe("Pinned message");
  });
});

describe("summaryRows", () => {
  const sum = (agent: string, text: string, updated_at: number, task_id = ""): AgentSummary => ({
    session_id: "s1",
    agent,
    text,
    source: task_id === "" ? null : { message_id: "", task_id, turn_id: "" },
    updated_at,
  });

  test("keeps each agent's latest line, newest first", () => {
    const rows = summaryRows([sum("claude", "old", 1), sum("claude", "new", 4), sum("codex", "proto done", 3, "t2")]);
    expect(rows).toEqual([
      { agent: "claude", text: "new", at: 4 },
      { agent: "codex", text: "proto done", at: 3 },
    ]);
  });

  test("clips long lines and skips empty ones", () => {
    const rows = summaryRows([sum("claude", "x".repeat(400), 1), sum("codex", "  ", 2)]);
    expect(rows).toHaveLength(1);
    expect(rows[0]?.text.length).toBe(280);
    expect(rows[0]?.text.endsWith("…")).toBe(true);
  });
});
