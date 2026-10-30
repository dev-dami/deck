import { describe, expect, test } from "vitest";
import { PANELS } from "../ui";
import { parseSaved } from "./store";
import {
  activate,
  applyPreset,
  CONVERSATION,
  complete,
  dock,
  dockRoot,
  type Layout,
  type Node,
  normalize,
  panesOf,
  preset,
  removePane,
  resizeSizes,
  setHidden,
  setShare,
  setSizes,
  shareOf,
  splitPane,
  stack,
  stackOf,
  split,
  terminalNumber,
  terminalsOf,
  toggleZoom,
  validate,
} from "./tree";

// The tree as nested arrays, for readable expectations: a stack is its
// panes joined by "|", a split is [dir, ...children].
type Shape = string | [string, ...Shape[]];
function shape(n: Node): Shape {
  return n.kind === "stack" ? n.panes.join("|") : [n.dir, ...n.children.map(shape)];
}

const PANELS_STACK = PANELS.map((p) => p.panel).join("|");
const stackId = (l: Layout, pane: string) => stackOf(l.root, pane)?.id ?? "";

describe("presets", () => {
  test("Default: the conversation over the terminal, the panel on the side Settings says", () => {
    expect(shape(preset("default", "right").root)).toEqual(["row", ["col", CONVERSATION, "term:1"], PANELS_STACK]);
    expect(shape(preset("default", "left").root)).toEqual(["row", PANELS_STACK, ["col", CONVERSATION, "term:1"]]);
  });

  test("Terminal below runs across the room; Terminal right sits beside the conversation", () => {
    expect(shape(preset("terminal-below", "right").root)).toEqual(["col", ["row", CONVERSATION, PANELS_STACK], "term:1"]);
    expect(shape(preset("terminal-right", "right").root)).toEqual(["row", CONVERSATION, "term:1", PANELS_STACK]);
  });

  test("Review puts the agents under the diff", () => {
    const rest = PANELS.map((p) => p.panel).filter((p) => p !== "agents").join("|");
    expect(shape(preset("review", "right").root)).toEqual(["row", ["col", CONVERSATION, "term:1"], ["col", rest, "agents"]]);
  });

  test("a preset keeps the terminals there are, so their shells live on", () => {
    let l = preset("default", "right");
    l = splitPane(l, "term:1", "right", "term:b");
    const next = applyPreset(l, "terminal-below", "right");
    expect(terminalsOf(next.root)).toEqual(["term:1", "term:b"]);
    expect(stackOf(next.root, "term:b")?.panes).toEqual(["term:1", "term:b"]);
  });

  test("Focus keeps the arrangement and leaves zoom", () => {
    const l = toggleZoom(preset("default", "right"), "term:1");
    const f = applyPreset(l, "focus", "right");
    expect(f.root).toBe(l.root);
    expect(f.zoom).toBeNull();
  });
});

describe("docking", () => {
  test("the terminal docked under the conversation, then to its right", () => {
    let l = preset("focus", "right");
    l = { ...l, root: split("row", [stack([CONVERSATION]), stack(["term:1"]), stack(PANELS.map((p) => p.panel))]) };
    l = dock(l, "term:1", stackId(l, CONVERSATION), "bottom");
    expect(shape(l.root)).toEqual(["row", ["col", CONVERSATION, "term:1"], PANELS_STACK]);
    l = dock(l, "term:1", stackId(l, CONVERSATION), "right");
    // The column of one closes up into the row.
    expect(shape(l.root)).toEqual(["row", CONVERSATION, "term:1", PANELS_STACK]);
  });

  test("an edge in a split of the same direction adds a sibling with half the target's share", () => {
    let l: Layout = { v: 1, root: split("row", [stack([CONVERSATION]), stack(["changes"])], [0.6, 0.4]), hidden: [], zoom: null };
    l = dock(l, "agents", stackId(l, "changes"), "left");
    expect(shape(l.root)).toEqual(["row", CONVERSATION, "agents", "changes"]);
    expect((l.root as { sizes: number[] }).sizes).toEqual([0.6, 0.2, 0.2]);
  });

  test("the centre joins the stack as its open tab", () => {
    let l = preset("default", "right");
    l = dock(l, "term:1", stackId(l, CONVERSATION), "center");
    expect(stackOf(l.root, CONVERSATION)).toMatchObject({ panes: [CONVERSATION, "term:1"], active: "term:1" });
    expect(shape(l.root)).toEqual(["row", `${CONVERSATION}|term:1`, PANELS_STACK]);
  });

  test("a tab dragged to its own stack's edge splits it; the only tab does nothing", () => {
    let l = preset("default", "right");
    const same = dock(l, "term:1", stackId(l, "term:1"), "right");
    expect(same).toBe(l);
    l = dock(l, "agents", stackId(l, "agents"), "bottom");
    const rest = PANELS.map((p) => p.panel).filter((p) => p !== "agents").join("|");
    expect(shape(l.root)).toEqual(["row", ["col", CONVERSATION, "term:1"], ["col", rest, "agents"]]);
  });

  test("the conversation moves like any pane and stays in the tree", () => {
    let l = preset("default", "right");
    l = dock(l, CONVERSATION, stackId(l, "changes"), "right");
    expect(shape(l.root)).toEqual(["row", "term:1", PANELS_STACK, CONVERSATION]);
    expect(panesOf(l.root)).toContain(CONVERSATION);
  });

  test("an unknown target changes nothing", () => {
    const l = preset("default", "right");
    expect(dock(l, "term:1", "nope", "left")).toBe(l);
  });

  test("the room's own edge docks across its full width", () => {
    let l = preset("default", "right");
    l = dockRoot(l, "term:1", "bottom");
    expect(shape(l.root)).toEqual(["col", ["row", CONVERSATION, PANELS_STACK], "term:1"]);
  });

  test("a lone conversation cannot be docked on the room's edge", () => {
    const l: Layout = { v: 1, root: stack([CONVERSATION]), hidden: [], zoom: null };
    expect(dockRoot(l, CONVERSATION, "left")).toBe(l);
  });
});

describe("multiplexing", () => {
  test("splitting a terminal right and down, as tmux does", () => {
    let l = preset("terminal-right", "right");
    l = splitPane(l, "term:1", "bottom", "term:b");
    l = splitPane(l, "term:b", "right", "term:c");
    expect(shape(l.root)).toEqual(["row", CONVERSATION, ["col", "term:1", ["row", "term:b", "term:c"]], PANELS_STACK]);
    expect(terminalNumber(l.root, "term:c")).toBe(3);
  });

  test("closing a pane closes up the tree and renumbers the terminals", () => {
    let l = preset("default", "right");
    l = splitPane(l, "term:1", "right", "term:b");
    l = removePane(l, "term:1");
    expect(shape(l.root)).toEqual(["row", ["col", CONVERSATION, "term:b"], PANELS_STACK]);
    expect(terminalNumber(l.root, "term:b")).toBe(1);
  });

  test("the conversation cannot be closed", () => {
    const l = preset("default", "right");
    expect(removePane(l, CONVERSATION)).toBe(l);
  });

  test("closing a tab opens the one before it", () => {
    let l = preset("default", "right");
    l = activate(l, "tasks");
    l = removePane(l, "tasks");
    expect(stackOf(l.root, "changes")?.active).toBe("graph");
  });

  test("zoom toggles, and goes with its pane", () => {
    let l = preset("default", "right");
    l = toggleZoom(l, "term:1");
    expect(l.zoom).toBe("term:1");
    expect(toggleZoom(l, "term:1").zoom).toBeNull();
    expect(removePane(l, "term:1").zoom).toBeNull();
    expect(toggleZoom(l, "term:zz").zoom).toBe("term:1");
  });

  test("a hidden pane keeps its place; the conversation never hides", () => {
    let l = preset("default", "right");
    l = setHidden(l, "graph", true);
    expect(l.hidden).toEqual(["graph"]);
    expect(setHidden(l, CONVERSATION, true)).toBe(l);
    expect(setHidden(l, "graph", false).hidden).toEqual([]);
  });
});

describe("resizing", () => {
  test("a border moves within both children's minimums", () => {
    expect(resizeSizes([0.5, 0.5], 0, 1, 0.2, 0.1, 0.1)).toEqual([0.7, 0.30000000000000004]);
    expect(resizeSizes([0.5, 0.5], 0, 1, 0.9, 0.1, 0.25)).toEqual([0.75, 0.25]);
    expect(resizeSizes([0.5, 0.5], 0, 1, -0.9, 0.2, 0.1)).toEqual([0.2, 0.8]);
    // Minimums too big for the pair split it evenly at worst.
    expect(resizeSizes([0.3, 0.3], 0, 1, 0.1, 0.5, 0.5)).toEqual([0.3, 0.3]);
  });

  test("setSizes refuses sizes that do not fit the split", () => {
    const l = preset("default", "right");
    expect(setSizes(l, l.root.id, [1])).toBe(l);
    expect(setSizes(l, l.root.id, [1, Number.NaN])).toBe(l);
    expect((setSizes(l, l.root.id, [3, 1]).root as { sizes: number[] }).sizes).toEqual([3, 1]);
  });

  test("Widen gives the panel's side a share of the room", () => {
    let l = preset("default", "right");
    expect(shareOf(l, "changes")).toBeCloseTo(0.44);
    l = setShare(l, "changes", 0.6);
    expect(shareOf(l, "changes")).toBeCloseTo(0.6);
    expect(shareOf(l, CONVERSATION)).toBeCloseTo(0.4);
  });
});

describe("normalize", () => {
  test("drops empty stacks, unwraps single children and flattens same-direction splits", () => {
    const n = split("row", [stack([]), split("row", [stack(["a"]), stack(["b"])], [1, 3]), split("col", [stack(["c"])])], [1, 2, 2]);
    const out = normalize(n);
    expect(out === null ? null : shape(out)).toEqual(["row", "a", "b", "c"]);
    expect((out as { sizes: number[] }).sizes).toEqual([0.5, 1.5, 2]);
  });
});

describe("validate", () => {
  const good = () => JSON.parse(JSON.stringify(preset("default", "right"))) as Record<string, unknown>;

  test("a saved layout comes back as it was", () => {
    const l = preset("terminal-below", "left");
    const back = validate(JSON.parse(JSON.stringify(l)));
    expect(back?.root).toEqual(l.root);
  });

  test("garbage is refused", () => {
    for (const v of [null, 3, "x", [], {}, { v: 2, root: good()["root"] }, { v: 1 }, { v: 1, root: { kind: "tree" } }, { v: 1, root: { kind: "split", dir: "diagonal", children: [] } }]) {
      expect(validate(v)).toBeNull();
    }
  });

  test("a layout without the conversation, or with a pane twice, is refused", () => {
    expect(validate({ v: 1, root: { kind: "stack", panes: ["changes"], active: "changes" } })).toBeNull();
    expect(validate({ v: 1, root: { kind: "split", dir: "row", children: [{ kind: "stack", panes: [CONVERSATION] }, { kind: "stack", panes: [CONVERSATION] }] } })).toBeNull();
  });

  test("a tree too deep is refused rather than walked", () => {
    let n: unknown = { kind: "stack", panes: [CONVERSATION] };
    for (let i = 0; i < 40; i++) n = { kind: "split", dir: i % 2 ? "row" : "col", children: [n], sizes: [1] };
    expect(validate({ v: 1, root: n })).toBeNull();
  });

  test("what can be mended is: unknown panes, missing panels, bad sizes, clashing ids, a stale zoom", () => {
    const l = validate({
      v: 1,
      root: {
        kind: "split",
        id: "same",
        dir: "row",
        sizes: [1, "wide"],
        children: [
          { kind: "stack", id: "same", panes: [CONVERSATION, "laser-view"], active: "laser-view" },
          { kind: "stack", id: "x", panes: ["changes", "term:BAD!", "term:2"] },
        ],
      },
      hidden: ["graph", "nope", CONVERSATION],
      zoom: "term:9",
    });
    expect(l).not.toBeNull();
    if (l === null) return;
    expect(shape(l.root)).toEqual(["row", CONVERSATION, ["changes", "term:2", ...PANELS.map((p) => p.panel).filter((p) => p !== "changes")].join("|")]);
    expect((l.root as { sizes: number[] }).sizes).toEqual([1, 1]);
    expect(stackOf(l.root, CONVERSATION)?.active).toBe(CONVERSATION);
    const ids = [l.root.id, ...(l.root as { children: Node[] }).children.map((c) => c.id)];
    expect(new Set(ids).size).toBe(3);
    expect(l.hidden).toEqual(["graph"]);
    expect(l.zoom).toBeNull();
  });

  test("complete adds the panels an older layout lacks", () => {
    const l: Layout = { v: 1, root: stack([CONVERSATION]), hidden: [], zoom: null };
    expect(shape(complete(l).root)).toEqual(["row", CONVERSATION, PANELS_STACK]);
  });

  test("the saved file: corrupt JSON or entries fall back to Default", () => {
    expect(parseSaved(null)).toEqual({ global: null, projects: {} });
    expect(parseSaved("{not json")).toEqual({ global: null, projects: {} });
    expect(parseSaved(JSON.stringify({ global: { v: 1, root: 5 }, projects: { a: good(), b: { v: 9 } } }))).toMatchObject({
      global: null,
      projects: { a: { v: 1 } },
    });
    expect(Object.keys(parseSaved(JSON.stringify({ projects: { a: good(), b: { v: 9 } } })).projects)).toEqual(["a"]);
  });
});
