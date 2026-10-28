import { describe, expect, test } from "vitest";
import type { BrowserTab, HighlightItem } from "../shared/bridge";
import { EMPTY_LIVE, LiveStore } from "./live";
import { toEvent } from "./rpcMap";

const item = (id: string, over: Partial<HighlightItem> = {}): HighlightItem => ({
  id,
  session_id: "s1",
  agent: "claude",
  key: id,
  kind: "blocker",
  title: id,
  detail: "",
  source: null,
  state: "open",
  created_at: 1,
  updated_at: 1,
  unread: true,
  ...over,
});

const tab = (id: string, over: Partial<BrowserTab> = {}): BrowserTab => ({
  id,
  session_id: "s1",
  owner: "claude",
  url: "https://example.com",
  title: "",
  loading: false,
  can_back: false,
  can_forward: false,
  user_in_control: false,
  snapshot_version: "1",
  activity: "",
  asleep: false,
  ...over,
});

describe("LiveStore", () => {
  test("an unknown room is empty", () => {
    expect(new LiveStore().get("nope")).toBe(EMPTY_LIVE);
  });

  test("a delta replaces its item by id, so a replay changes nothing", () => {
    const s = new LiveStore();
    s.loadHighlights("s1", { items: [item("a")], summaries: [] });
    s.apply({ k: "highlight", item: item("a", { state: "resolved" }) });
    s.apply({ k: "highlight", item: item("a", { state: "resolved" }) });
    expect(s.get("s1").items).toHaveLength(1);
    expect(s.get("s1").items[0]?.state).toBe("resolved");
  });

  test("a new item is added and a summary replaces that agent's line", () => {
    const s = new LiveStore();
    s.apply({ k: "highlight", item: item("a") });
    s.apply({ k: "summary", summary: { session_id: "s1", agent: "codex", text: "one", source: null, updated_at: 1 } });
    s.apply({ k: "summary", summary: { session_id: "s1", agent: "codex", text: "two", source: null, updated_at: 2 } });
    expect(s.get("s1").items).toHaveLength(1);
    expect(s.get("s1").summaries.map((x) => x.text)).toEqual(["two"]);
  });

  test("a browser state with no tabs clears the room's tabs", () => {
    const s = new LiveStore();
    s.apply({ k: "browser", state: { session_id: "s1", available: true, tabs: [] } });
    expect(s.get("s1")).toMatchObject({ tabs: [], browserAvailable: true });
  });

  test("drop forgets a room and notifies", () => {
    const s = new LiveStore();
    let n = 0;
    s.subscribe(() => n++);
    s.apply({ k: "highlight", item: item("a") });
    s.drop("s1");
    expect(s.get("s1")).toBe(EMPTY_LIVE);
    expect(n).toBe(2);
  });
});

describe("LiveStore races", () => {
  test("a snapshot read before a delta does not overwrite it", () => {
    const s = new LiveStore();
    s.apply({ k: "highlight", item: item("a", { state: "resolved", updated_at: 5 }) });
    s.loadHighlights("s1", { items: [item("a", { state: "open", updated_at: 3 }), item("b", { updated_at: 2 })], summaries: [] });
    expect(s.get("s1").items.find((i) => i.id === "a")?.state).toBe("resolved");
    expect(s.get("s1").items.map((i) => i.id).sort()).toEqual(["a", "b"]);
  });

  test("an older delta does not overwrite a newer item", () => {
    const s = new LiveStore();
    s.apply({ k: "highlight", item: item("a", { state: "resolved", updated_at: 5 }) });
    s.apply({ k: "highlight", item: item("a", { state: "open", updated_at: 4 }) });
    expect(s.get("s1").items[0]?.state).toBe("resolved");
  });

  test("a newer summary wins whichever way it arrives", () => {
    const s = new LiveStore();
    s.apply({ k: "summary", summary: { session_id: "s1", agent: "codex", text: "new", source: null, updated_at: 9 } });
    s.loadHighlights("s1", { items: [], summaries: [{ session_id: "s1", agent: "codex", text: "old", source: null, updated_at: 2 }] });
    expect(s.get("s1").summaries.map((x) => x.text)).toEqual(["new"]);
  });

  test("a tab snapshot is dropped when a delta came in while it was fetched", () => {
    const s = new LiveStore();
    const stamp = s.browserStamp("s1");
    s.apply({ k: "browser", state: { session_id: "s1", available: true, tabs: [tab("t2")] } });
    s.loadBrowser("s1", [tab("t1")], true, stamp);
    expect(s.get("s1").tabs.map((t) => t.id)).toEqual(["t2"]);
    s.loadBrowser("s1", [tab("t3")], true, s.browserStamp("s1"));
    expect(s.get("s1").tabs.map((t) => t.id)).toEqual(["t3"]);
  });

  test("a stale snapshot at the same time keeps the source the Deck filled in", () => {
    const s = new LiveStore();
    const src = { message_id: "m1", task_id: "", turn_id: "t1" };
    s.apply({ k: "highlight", item: item("a", { updated_at: 4, source: src }) });
    s.loadHighlights("s1", { items: [item("a", { updated_at: 4, source: { message_id: "", task_id: "", turn_id: "t1" } })], summaries: [] });
    expect(s.get("s1").items[0]?.source?.message_id).toBe("m1");
    s.apply({ k: "summary", summary: { session_id: "s1", agent: "codex", text: "x", source: src, updated_at: 4 } });
    s.loadHighlights("s1", { items: [], summaries: [{ session_id: "s1", agent: "codex", text: "x", source: null, updated_at: 4 }] });
    expect(s.get("s1").summaries[0]?.source?.message_id).toBe("m1");
  });

  test("a source that arrives later at the same time fills the item in", () => {
    const s = new LiveStore();
    s.apply({ k: "highlight", item: item("a", { updated_at: 4 }) });
    s.apply({ k: "highlight", item: item("a", { updated_at: 4, source: { message_id: "m1", task_id: "", turn_id: "" } }) });
    expect(s.get("s1").items[0]?.source?.message_id).toBe("m1");
  });

  test("a command's answer cannot undo a close or takeover that came first", () => {
    const s = new LiveStore();
    s.applyTab(tab("t1"));
    const stamp = s.browserStamp("s1");
    s.apply({ k: "browser", state: { session_id: "s1", available: true, tabs: [] } });
    s.applyTab(tab("t1"), stamp);
    expect(s.get("s1").tabs).toEqual([]);
    s.applyTab(tab("t2", { user_in_control: true }), s.browserStamp("s1"));
    expect(s.get("s1").tabs.map((t) => t.id)).toEqual(["t2"]);
  });

  test("an action's answer updates the item and the tab at once", () => {
    const s = new LiveStore();
    s.applyItem(item("a", { updated_at: 1 }));
    s.applyItem(item("a", { state: "dismissed", updated_at: 2 }));
    s.applyTab(tab("t1"));
    s.applyTab(tab("t1", { url: "https://x.test" }));
    expect(s.get("s1").items[0]?.state).toBe("dismissed");
    expect(s.get("s1").tabs).toHaveLength(1);
    expect(s.get("s1").tabs[0]?.url).toBe("https://x.test");
  });
});

describe("toEvent live kinds", () => {
  test("maps a highlight delta", () => {
    const e = toEvent({ kind: "highlight_changed", seq: 7, highlight_changed: { id: "h1", session_id: "s1", agent: "codex", kind: "blocker", state: "open", title: "t", unread: true } });
    expect(e).toMatchObject({ k: "live", seq: 7, live: { k: "highlight", item: { id: "h1", kind: "blocker", unread: true } } });
  });

  test("drops an unknown kind or state", () => {
    expect(toEvent({ kind: "highlight_changed", seq: 1, highlight_changed: { id: "h1", kind: "meh", state: "open" } })).toBeNull();
    expect(toEvent({ kind: "highlight_changed", seq: 1, highlight_changed: { id: "h1", kind: "blocker", state: "gone" } })).toBeNull();
  });

  test("a browser delta needs its room", () => {
    expect(toEvent({ kind: "browser_changed", seq: 2, browser_changed: { available: true, tabs: [] } })).toBeNull();
    expect(toEvent({ kind: "browser_changed", seq: 2, browser_changed: { session_id: "s1", available: true, tabs: [] } })).toMatchObject({ k: "live", live: { k: "browser" } });
  });
});
