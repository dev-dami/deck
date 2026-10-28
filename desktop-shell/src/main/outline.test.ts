import { describe, expect, test } from "vitest";
import { type AxNode, buildOutline, OUTLINE_LIMITS, RefTable, renderOutline } from "./outline";

// A tiny way to write an accessibility tree: [role, name, props, ...children].
type Spec = [role: string, name: string, props: Record<string, unknown> | null, ...kids: Spec[]];

function tree(root: Spec): AxNode[] {
  const nodes: AxNode[] = [];
  let n = 0;
  const add = (s: Spec, parent?: string): string => {
    const id = String(++n);
    const [role, name, props, ...kids] = s;
    const node: AxNode = {
      nodeId: id,
      role: { value: role },
      name: { value: name },
      backendDOMNodeId: 100 + n,
      childIds: [],
      ...(parent === undefined ? {} : { parentId: parent }),
    };
    const { ignored, value, ...rest } = props ?? {};
    if (ignored === true) node.ignored = true;
    if (value !== undefined) node.value = { value };
    node.properties = Object.entries(rest).map(([k, v]) => ({ name: k, value: { value: v } }));
    nodes.push(node);
    node.childIds = kids.map((k) => add(k, id));
    return id;
  };
  add(root);
  return nodes;
}

const URL_ = "https://example.com/start";
const run = (root: Spec, extra: Partial<Parameters<typeof buildOutline>[1]> = {}) =>
  buildOutline(tree(root), { refs: new RefTable(), url: URL_, ...extra });

const PAGE: Spec = [
  "RootWebArea",
  "Sign in",
  null,
  ["banner", "", null, ["link", "Home", { url: "https://example.com/" }, ["StaticText", "Home", null]]],
  [
    "main",
    "",
    null,
    ["heading", "Welcome back", { level: 1 }, ["StaticText", "Welcome back", null]],
    ["paragraph", "", null, ["StaticText", "Please sign in ", null], ["StaticText", "to continue.", null]],
    [
      "form",
      "Sign in",
      null,
      ["textbox", "Email", { required: true, value: "ada@example.com" }],
      ["textbox", "Password", { value: "••••••" }],
      ["checkbox", "Remember me", { checked: "false" }],
      ["button", "Sign in", { disabled: true }, ["StaticText", "Sign in", null]],
      ["combobox", "Country", { value: "United Kingdom", expanded: false }],
    ],
    ["link", "Forgot password?", { url: "https://example.com/start#reset" }],
    ["link", "Terms", { url: "https://other.org/terms" }],
  ],
  ["contentinfo", "", null, ["StaticText", "(c) Example", null]],
];

describe("buildOutline", () => {
  test("lists the page as indented lines with refs on what can be acted on", () => {
    const refs = new RefTable();
    const out = buildOutline(tree(PAGE), { refs, url: URL_, secret: new Set([113]) });
    expect(out.lines).toEqual([
      "header",
      '  link [e1] "Home" -> /',
      "main",
      '  h1 "Welcome back"',
      '  text "Please sign in to continue."',
      '  form "Sign in"',
      '    textbox [e2] "Email" = "ada@example.com" required',
      // The password is the 13th node of the tree (backend id 113).
      '    textbox [e3] "Password" = (hidden)',
      '    checkbox [e4] "Remember me" unchecked',
      '    button [e5] "Sign in" disabled',
      '    combobox [e6] "Country" = "United Kingdom" collapsed',
      '  link [e7] "Forgot password?" -> /start#reset',
      '  link [e8] "Terms" -> https://other.org/terms',
      "footer",
      '  text "(c) Example"',
    ]);
    expect(out.actionable).toBe(8);
    expect(out.omitted).toBe(0);
  });

  test("a heading's own words and a button's label are not listed twice", () => {
    const out = run(["RootWebArea", "", null, ["heading", "Hi", { level: 2 }, ["StaticText", "Hi", null]], ["button", "Go", null, ["StaticText", "Go", null]]]);
    expect(out.lines).toEqual(['h2 "Hi"', 'button [e1] "Go"']);
  });

  test("a label's words are not listed again beside its field, but a field inside it is", () => {
    const out = run([
      "RootWebArea",
      "",
      null,
      ["LabelText", "", null, ["StaticText", "Email", null]],
      ["textbox", "Email", null],
      ["LabelText", "", null, ["checkbox", "Remember me", { checked: "false" }], ["StaticText", "Remember me", null]],
      ["LabelText", "Phone", null, ["StaticText", "Phone", null]],
      ["StaticText", "Note", null],
    ]);
    expect(out.lines).toEqual(['textbox [e1] "Email" = ""', 'checkbox [e2] "Remember me" unchecked', 'text "Note"']);
  });

  test("a link inside a heading keeps its ref", () => {
    const out = run(["RootWebArea", "", null, ["heading", "Docs home", { level: 3 }, ["link", "Docs home", { url: "/d" }, ["StaticText", "Docs home", null]]]]);
    expect(out.lines).toEqual(['h3 "Docs home"', '  link [e1] "Docs home" -> /d']);
  });

  test("ignored nodes and plain wrappers vanish, their content stays", () => {
    const out = run([
      "RootWebArea",
      "",
      null,
      ["generic", "", null, ["generic", "", { ignored: true }, ["button", "Deep", null]], ["none", "", null, ["StaticText", "Kept", null]]],
    ]);
    expect(out.lines).toEqual(['button [e1] "Deep"', 'text "Kept"']);
  });

  test("unnamed groups, lists and regions add no level; named ones do", () => {
    const out = run([
      "RootWebArea",
      "",
      null,
      ["list", "", null, ["listitem", "", null, ["link", "A", { url: "/a" }]]],
      ["list", "Steps", null, ["listitem", "", null, ["link", "B", { url: "/b" }]]],
      ["region", "", null, ["button", "C", null]],
      ["dialog", "Cookies", null, ["button", "Accept", null]],
    ]);
    expect(out.lines).toEqual(['link [e1] "A" -> /a', 'list "Steps"', '  link [e2] "B" -> /b', 'button [e3] "C"', 'dialog "Cookies"', '  button [e4] "Accept"']);
  });

  test("refs are stable across snapshots of one document and new for new elements", () => {
    const refs = new RefTable();
    const before: Spec = ["RootWebArea", "", null, ["button", "A", null], ["button", "B", null]];
    buildOutline(tree(before), { refs, url: URL_ });
    // A new button appears first; the old ones keep their refs.
    const nodes = tree(["RootWebArea", "", null, ["button", "New", null], ["button", "A", null], ["button", "B", null]]);
    // Same backend ids for A and B as in the first tree (102, 103).
    nodes[1]!.backendDOMNodeId = 999;
    nodes[2]!.backendDOMNodeId = 102;
    nodes[3]!.backendDOMNodeId = 103;
    const out = buildOutline(nodes, { refs, url: URL_ });
    expect(out.lines).toEqual(['button [e3] "New"', 'button [e1] "A"', 'button [e2] "B"']);
    expect(refs.node("e3")).toBe(999);
    expect(refs.node("e9")).toBeUndefined();
    expect(refs.size).toBe(3);
  });

  test("interactive mode lists only what has a ref, flat", () => {
    const out = run(PAGE, { interactive: true });
    expect(out.lines).toHaveLength(8);
    expect(out.lines[0]).toBe('link [e1] "Home" -> /');
    expect(out.lines.every((l) => /\[e\d+\]/.test(l) && !l.startsWith(" "))).toBe(true);
  });

  test("past max the outline stops and counts what it left out", () => {
    const buttons: Spec[] = Array.from({ length: 30 }, (_, i): Spec => ["button", `B${i}`, null]);
    const out = run(["RootWebArea", "", null, ...buttons], { max: 10 });
    expect(out.lines).toHaveLength(10);
    expect(out.omitted).toBe(20);
    expect(out.actionable).toBe(30);
    const text = renderOutline(out, { tab: "t1", title: "Many", url: URL_ }, false);
    expect(text).toContain("[... 20 more lines;");
  });

  test("long names and text are cut, and quotes are escaped", () => {
    const long = "x".repeat(500);
    const out = run(["RootWebArea", "", null, ["button", `say "hi"\nnow ${long}`, null], ["StaticText", long, null]]);
    expect(out.lines[0]).toMatch(/^button \[e1\] "say \\"hi\\" now x+…"$/);
    expect(out.lines[0]!.length).toBeLessThan(OUTLINE_LIMITS.name + 30);
    expect(out.lines[1]!.length).toBeLessThan(OUTLINE_LIMITS.text + 10);
  });

  test("an empty or oddly shaped tree gives nothing, not an error", () => {
    expect(buildOutline([], { refs: new RefTable(), url: URL_ }).lines).toEqual([]);
    const loop: AxNode[] = [{ nodeId: "1", role: { value: "RootWebArea" }, childIds: ["1", "9"] }];
    expect(buildOutline(loop, { refs: new RefTable(), url: URL_ }).lines).toEqual([]);
  });
});

describe("renderOutline", () => {
  test("has a header with the tab, title and address", () => {
    const text = renderOutline(run(["RootWebArea", "", null, ["button", "Go", null]]), { tab: "t4", title: "Shop", url: URL_, loading: true }, false);
    expect(text).toBe(`[t4] Shop  ${URL_}  (still loading)\nbutton [e1] "Go"`);
    expect(renderOutline(run(["RootWebArea", "", null]), { tab: "t1", title: "", url: URL_ }, false)).toContain("(untitled)");
    expect(renderOutline(run(["RootWebArea", "", null]), { tab: "t1", title: "", url: URL_ }, true)).toContain("nothing to act on");
  });
});
