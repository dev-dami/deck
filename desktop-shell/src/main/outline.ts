// The page as an agent sees it: a compact text outline built from Chromium's
// accessibility tree. Each element an agent can act on gets a short ref
// (e12) that stays the same for the element across snapshots of one
// document, and the outline says nothing about the rest of the DOM. This
// file is pure: it turns the nodes of Accessibility.getFullAXTree into text.
//
//   [t2] Example Domain  https://example.com/
//   main
//     h1 "Example Domain"
//     text "This domain is for use in illustrative examples."
//     link [e1] "More information..." -> https://www.iana.org/domains/example
//   form "Sign in"
//     textbox [e2] "Email" = "ada@example.com" required
//     textbox [e3] "Password" = (hidden)
//     checkbox [e4] "Remember me" unchecked
//     button [e5] "Sign in" disabled
//
// One element per line, two spaces of indent for each container (landmark,
// form, dialog, list, table) it sits in. A line is the role, the ref when it
// can be acted on, the accessible name in quotes, "= value" for a field, and
// its states. Text is merged into one line per run.

export interface AxValue {
  value?: unknown;
}

export interface AxProperty {
  name: string;
  value: AxValue;
}

// One node of Accessibility.getFullAXTree, the fields the outline reads.
export interface AxNode {
  nodeId: string;
  ignored?: boolean;
  role?: AxValue;
  name?: AxValue;
  value?: AxValue;
  properties?: AxProperty[];
  childIds?: string[];
  parentId?: string;
  backendDOMNodeId?: number;
}

export const OUTLINE_LIMITS = {
  /** Lines a snapshot returns unless the agent asks for more. */
  lines: 250,
  name: 120,
  text: 240,
  href: 120,
  value: 120,
  /** How deep the walk goes; deeper elements are left out. */
  depth: 120,
} as const;

// Refs for the elements of one document: the same element keeps its ref
// from one snapshot to the next, and a new element gets the next number.
export class RefTable {
  private next = 1;
  private readonly byNode = new Map<number, string>();
  private readonly byRef = new Map<string, number>();

  ref(node: number): string {
    let r = this.byNode.get(node);
    if (r === undefined) {
      r = `e${this.next++}`;
      this.byNode.set(node, r);
      this.byRef.set(r, node);
    }
    return r;
  }

  node(ref: string): number | undefined {
    return this.byRef.get(ref);
  }

  get size(): number {
    return this.byRef.size;
  }
}

export interface OutlineOptions {
  refs: RefTable;
  /** The page's address, to shorten links to it. */
  url: string;
  /** Only the elements that have refs, one per line with no indent. */
  interactive?: boolean;
  /** Most lines to return. */
  max?: number;
  /** Backend ids of fields whose value must not be shown (passwords). */
  secret?: ReadonlySet<number>;
}

export interface Outline {
  lines: string[];
  /** Elements with refs in the outline, shown or not. */
  actionable: number;
  /** Lines left out past max. */
  omitted: number;
}

// Roles the agent can act on: they get a ref and no children are listed.
const ACTIONABLE = new Set([
  "button",
  "link",
  "textbox",
  "searchbox",
  "combobox",
  "checkbox",
  "radio",
  "switch",
  "slider",
  "spinbutton",
  "menuitem",
  "menuitemcheckbox",
  "menuitemradio",
  "tab",
  "option",
  "treeitem",
  "DisclosureTriangle",
  "PopUpButton",
  "ListBoxOption",
  "TextField",
  "ComboBox",
  "ColorWell",
  "Pdf",
]);

// Roles whose value the outline shows; an editable one shows even when empty.
const FIELDS = new Set(["textbox", "searchbox", "combobox", "spinbutton", "slider", "PopUpButton", "TextField", "ComboBox"]);
const EDITABLE = new Set(["textbox", "searchbox", "TextField"]);

// Roles that open a level of structure.
const CONTAINERS = new Set([
  "main",
  "navigation",
  "banner",
  "contentinfo",
  "complementary",
  "form",
  "search",
  "region",
  "dialog",
  "alertdialog",
  "alert",
  "status",
  "list",
  "listbox",
  "menu",
  "menubar",
  "tablist",
  "toolbar",
  "tree",
  "table",
  "grid",
  "row",
  "article",
  "figure",
  "group",
  "radiogroup",
  "tabpanel",
  "Iframe",
]);

// Containers shown only when they have a name.
const NAMED_ONLY = new Set(["region", "group", "article", "figure", "row", "list", "table", "grid", "radiogroup", "tabpanel", "status"]);

const SHORT_ROLE: Record<string, string> = {
  DisclosureTriangle: "disclosure",
  PopUpButton: "select",
  ListBoxOption: "option",
  TextField: "textbox",
  ComboBox: "combobox",
  ColorWell: "color",
  contentinfo: "footer",
  banner: "header",
  Iframe: "iframe",
  img: "image",
};

const str = (v: AxValue | undefined): string => (typeof v?.value === "string" ? v.value : v?.value === undefined || v.value === null ? "" : String(v.value));

function clean(s: string, max: number): string {
  const t = s.replace(/\s+/g, " ").trim();
  return t.length > max ? `${t.slice(0, max - 1)}…` : t;
}

function prop(n: AxNode, name: string): unknown {
  return n.properties?.find((p) => p.name === name)?.value.value;
}

function shortHref(href: string, base: string): string {
  let out = href;
  try {
    const u = new URL(href);
    const b = new URL(base);
    if (u.origin === b.origin) out = u.pathname + u.search + u.hash;
    else out = u.href;
    if (out === b.pathname + b.search) out = "(this page)";
  } catch {
    // Not a URL; shown as it is.
  }
  return out.length > OUTLINE_LIMITS.href ? `${out.slice(0, OUTLINE_LIMITS.href - 1)}…` : out;
}

// States of an element, in a fixed order.
function states(n: AxNode, role: string): string[] {
  const out: string[] = [];
  const checked = prop(n, "checked");
  if (["checkbox", "radio", "switch", "menuitemcheckbox", "menuitemradio"].includes(role)) {
    out.push(checked === "mixed" ? "mixed" : checked === true || checked === "true" ? "checked" : "unchecked");
  }
  const pressed = prop(n, "pressed");
  if (pressed === true || pressed === "true") out.push("pressed");
  else if (pressed === "mixed") out.push("mixed");
  const expanded = prop(n, "expanded");
  if (expanded === true || expanded === "true") out.push("expanded");
  else if (expanded === false || expanded === "false") out.push("collapsed");
  if (prop(n, "selected") === true) out.push("selected");
  if (prop(n, "disabled") === true) out.push("disabled");
  if (prop(n, "required") === true) out.push("required");
  if (prop(n, "readonly") === true) out.push("readonly");
  const invalid = prop(n, "invalid");
  if (invalid !== undefined && invalid !== false && invalid !== "false") out.push("invalid");
  if (prop(n, "focused") === true) out.push("focused");
  return out;
}

// The words under a node, as one string.
function textUnder(n: AxNode, byId: ReadonlyMap<string, AxNode>, depth: number): string {
  if (depth > 6) return "";
  const own = str(n.role) === "StaticText" ? clean(str(n.name), OUTLINE_LIMITS.text) : "";
  const kids = (n.childIds ?? []).map((id) => byId.get(id)).filter((k): k is AxNode => k !== undefined);
  return [own, ...kids.map((k) => textUnder(k, byId, depth + 1))].filter((t) => t !== "").join(" ");
}

/** Turns the accessibility tree into the outline an agent reads. */
export function buildOutline(nodes: readonly AxNode[], o: OutlineOptions): Outline {
  const byId = new Map<string, AxNode>();
  for (const n of nodes) byId.set(n.nodeId, n);
  const root = nodes.find((n) => str(n.role) === "RootWebArea") ?? nodes.find((n) => n.parentId === undefined);
  const max = o.max ?? OUTLINE_LIMITS.lines;
  const lines: string[] = [];
  let omitted = 0;
  let actionable = 0;
  // A run of text is one line: where it is in lines, and its words so far.
  let run: { at: number; indent: number; text: string } | null = null;

  const push = (indent: number, text: string) => {
    run = null;
    if (lines.length >= max) omitted++;
    else lines.push(`${o.interactive ? "" : "  ".repeat(indent)}${text}`);
  };
  const pushText = (indent: number, text: string) => {
    if (o.interactive) return;
    if (run !== null && run.indent === indent && lines.length === run.at + 1 && run.text.length < OUTLINE_LIMITS.text) {
      run.text = clean(`${run.text} ${text}`, OUTLINE_LIMITS.text);
      lines[run.at] = `${"  ".repeat(indent)}text ${JSON.stringify(run.text)}`;
      return;
    }
    const t = clean(text, OUTLINE_LIMITS.text);
    if (lines.length >= max) {
      omitted++;
      run = null;
      return;
    }
    lines.push(`${"  ".repeat(indent)}text ${JSON.stringify(t)}`);
    run = { at: lines.length - 1, indent, text: t };
  };

  // covered is text the parent line already says (a heading's own words).
  const walk = (n: AxNode, indent: number, covered: string, depth: number): void => {
    if (depth > OUTLINE_LIMITS.depth) return;
    const role = str(n.role);
    const kids = (n.childIds ?? []).map((id) => byId.get(id)).filter((k): k is AxNode => k !== undefined);
    const descend = (at: number, cover: string) => {
      for (const k of kids) walk(k, at, cover, depth + 1);
    };
    if (n.ignored === true) {
      descend(indent, covered);
      return;
    }
    const name = clean(str(n.name), OUTLINE_LIMITS.name);
    if (role === "StaticText" || role === "text") {
      const t = clean(str(n.name), OUTLINE_LIMITS.text);
      if (t !== "" && !(covered !== "" && covered.includes(t))) pushText(indent, t);
      return;
    }
    if (ACTIONABLE.has(role)) {
      const rname = SHORT_ROLE[role] ?? role;
      const id = n.backendDOMNodeId;
      let line = rname;
      if (id !== undefined) {
        line += ` [${o.refs.ref(id)}]`;
        actionable++;
      }
      if (name !== "") line += ` ${JSON.stringify(name)}`;
      if (FIELDS.has(role)) {
        const secret = id !== undefined && o.secret?.has(id) === true;
        const v = str(n.value);
        if (secret) line += v === "" ? ' = ""' : " = (hidden)";
        else if (v !== "" || EDITABLE.has(role)) line += ` = ${JSON.stringify(clean(v, OUTLINE_LIMITS.value))}`;
      }
      if (role === "link") {
        const href = prop(n, "url");
        if (typeof href === "string" && href !== "") line += ` -> ${shortHref(href, o.url)}`;
      }
      const st = states(n, role);
      if (st.length > 0) line += ` ${st.join(" ")}`;
      if (id !== undefined || !o.interactive) push(indent, line);
      // A listbox or menu lists its options; a field or button says all it has.
      return;
    }
    if (role === "heading") {
      const level = prop(n, "level");
      if (!o.interactive && name !== "") push(indent, `h${typeof level === "number" ? level : 2} ${JSON.stringify(name)}`);
      descend(indent + 1, name);
      return;
    }
    if (role === "image" || role === "img") {
      if (!o.interactive && name !== "") push(indent, `image ${JSON.stringify(name)}`);
      descend(indent, name);
      return;
    }
    if (CONTAINERS.has(role)) {
      const show = !(NAMED_ONLY.has(role) && name === "");
      if (show && !o.interactive) {
        push(indent, `${SHORT_ROLE[role] ?? role}${name === "" ? "" : ` ${JSON.stringify(name)}`}${states(n, role).map((s) => ` ${s}`).join("")}`);
      }
      descend(show ? indent + 1 : indent, covered);
      return;
    }
    // A label's words are the name of what it labels: only a control inside
    // it is listed.
    if (role === "LabelText" || role === "label") {
      descend(indent, name !== "" ? name : textUnder(n, byId, 0));
      return;
    }
    // Everything else (generic, paragraph, cells, ...) is only structure:
    // what is inside shows at the same level.
    descend(indent, covered);
  };

  if (root !== undefined) walk(root, 0, "", 0);
  return { lines, actionable, omitted };
}

/** The outline as the text an agent reads, with its header and footer. */
export function renderOutline(o: Outline, head: { tab: string; title: string; url: string; loading?: boolean }, interactive: boolean): string {
  const out = [`[${head.tab}] ${clean(head.title, OUTLINE_LIMITS.name) || "(untitled)"}  ${head.url}${head.loading === true ? "  (still loading)" : ""}`];
  out.push(...o.lines);
  if (o.lines.length === 0) out.push(interactive ? "(nothing to act on here)" : "(the page shows no content yet)");
  if (o.omitted > 0) {
    out.push(`[... ${o.omitted} more lines; use browser.snapshot {interactive: true} or {max: ${Math.min(OUTLINE_LIMITS.lines * 4, o.lines.length + o.omitted)}}, or scroll ...]`);
  }
  return out.join("\n");
}
