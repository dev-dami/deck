import { PANELS, type Panel } from "../ui";

// The room's layout as a tree, tmux-like: splits of rows and columns whose
// leaves are stacks, each a set of panes shown as tabs. A pane is the
// conversation, a terminal, or one of the side panel's views. The tree says
// where each pane goes; whether it shows is the window's to say (the
// terminal's toggle, the side panel's), so a toggle never reshapes it.
// Every function here is pure: the Workspace renders a tree, the store
// keeps one, and the tests drive them directly.

export type PaneId = string;
export const CONVERSATION = "conversation";

export interface Stack {
  kind: "stack";
  id: string;
  panes: PaneId[];
  active: PaneId;
}

export interface Split {
  kind: "split";
  id: string;
  // A row lays its children left to right, a column top to bottom.
  dir: "row" | "col";
  children: Node[];
  // Each child's share; only their ratios count.
  sizes: number[];
}

export type Node = Stack | Split;

export interface Layout {
  v: 1;
  root: Node;
  // Panes the person hid from the Layout menu; they keep their place.
  hidden: PaneId[];
  // The pane that fills the room while zoomed.
  zoom: PaneId | null;
}

export type Edge = "left" | "right" | "top" | "bottom";
export type Zone = Edge | "center";

const PANEL_IDS: readonly string[] = PANELS.map((p) => p.panel);
const TERM = /^term:[a-z0-9]{1,24}$/;

export const isTerminal = (p: PaneId): boolean => TERM.test(p);
export const isPanel = (p: PaneId): p is Panel => PANEL_IDS.includes(p);
export const knownPane = (p: unknown): p is PaneId => typeof p === "string" && (p === CONVERSATION || isPanel(p) || isTerminal(p));

let seq = 0;
// A node's id: unique in the window, which is all a key or a drop target needs.
export function nodeId(): string {
  seq += 1;
  return `n${seq.toString(36)}${Math.floor(Math.random() * 1296).toString(36)}`;
}

// A fresh terminal pane's id.
export function terminalId(): PaneId {
  seq += 1;
  return `term:${Date.now().toString(36)}${seq.toString(36)}`;
}

export const stack = (panes: PaneId[], active?: PaneId): Stack => ({ kind: "stack", id: nodeId(), panes, active: active ?? panes[0] ?? "" });
export const split = (dir: "row" | "col", children: Node[], sizes?: number[]): Split => ({
  kind: "split",
  id: nodeId(),
  dir,
  children,
  sizes: sizes ?? children.map(() => 1),
});

// ── Reading ──

export function stacksOf(n: Node): Stack[] {
  return n.kind === "stack" ? [n] : n.children.flatMap(stacksOf);
}

// Every pane, in reading order (left to right, top to bottom).
export function panesOf(n: Node): PaneId[] {
  return stacksOf(n).flatMap((s) => s.panes);
}

export function terminalsOf(n: Node): PaneId[] {
  return panesOf(n).filter(isTerminal);
}

export function stackOf(n: Node, pane: PaneId): Stack | null {
  return stacksOf(n).find((s) => s.panes.includes(pane)) ?? null;
}

export function findNode(n: Node, id: string): Node | null {
  if (n.id === id) return n;
  if (n.kind === "stack") return null;
  for (const c of n.children) {
    const f = findNode(c, id);
    if (f !== null) return f;
  }
  return null;
}

export function contains(n: Node, pane: PaneId): boolean {
  return n.kind === "stack" ? n.panes.includes(pane) : n.children.some((c) => contains(c, pane));
}

// "Terminal 2": terminals are numbered by where they sit, as tmux numbers
// its panes, so closing one renumbers those after it.
export function terminalNumber(n: Node, pane: PaneId): number {
  return terminalsOf(n).indexOf(pane) + 1;
}

// ── Shaping ──

// Puts a tree back in its plain form: no empty stacks, no split of one
// child, no split inside a split of the same direction (its children join
// the parent's, their shares scaled to the one they had).
export function normalize(n: Node): Node | null {
  if (n.kind === "stack") {
    if (n.panes.length === 0) return null;
    return n.panes.includes(n.active) ? n : { ...n, active: n.panes[0] as PaneId };
  }
  const children: Node[] = [];
  const sizes: number[] = [];
  n.children.forEach((c, i) => {
    const k = normalize(c);
    if (k === null) return;
    const size = n.sizes[i] ?? 1;
    if (k.kind === "split" && k.dir === n.dir) {
      const total = sum(k.sizes);
      k.children.forEach((cc, j) => {
        children.push(cc);
        sizes.push((size * (k.sizes[j] ?? 1)) / total);
      });
    } else {
      children.push(k);
      sizes.push(size);
    }
  });
  if (children.length === 0) return null;
  if (children.length === 1) return children[0] as Node;
  const same = children.length === n.children.length && children.every((c, i) => c === n.children[i]) && sizes.every((s, i) => s === n.sizes[i]);
  return same ? n : { ...n, children, sizes };
}

// The tree with one node put in place of another (by id).
function replace(n: Node, id: string, f: (n: Node) => Node): Node {
  if (n.id === id) return f(n);
  if (n.kind === "stack") return n;
  let changed = false;
  const children = n.children.map((c) => {
    const k = replace(c, id, f);
    if (k !== c) changed = true;
    return k;
  });
  return changed ? { ...n, children } : n;
}

function withRoot(l: Layout, root: Node | null): Layout {
  if (root === null) return l;
  const r = normalize(root) ?? l.root;
  const panes = panesOf(r);
  const zoom = l.zoom !== null && panes.includes(l.zoom) ? l.zoom : null;
  return { ...l, root: r, zoom };
}

// Takes a pane out of its stack. A stack left empty goes, and the tree
// closes up around it.
export function removePane(l: Layout, pane: PaneId): Layout {
  const s = stackOf(l.root, pane);
  if (s === null) return l;
  if (pane === CONVERSATION) return l; // The conversation always has a place.
  const root = replace(l.root, s.id, (n) => {
    const st = n as Stack;
    const panes = st.panes.filter((p) => p !== pane);
    return { ...st, panes, active: st.active === pane ? (panes[st.panes.indexOf(pane) - 1] ?? panes[0] ?? "") : st.active };
  });
  // The conversation never leaves, so the root never empties.
  const next = withRoot(l, root);
  return { ...next, hidden: next.hidden.filter((p) => p !== pane) };
}

const axis = (e: Edge): "row" | "col" => (e === "left" || e === "right" ? "row" : "col");
const before = (e: Edge): boolean => e === "left" || e === "top";

// Puts a node beside the node with id target, on its edge. In a split of
// that direction the new node becomes a sibling, taking half the target's
// share; otherwise the target turns into a split of the two.
function besideNode(root: Node, target: string, add: Node, edge: Edge): Node {
  const dir = axis(edge);
  const parent = parentOf(root, target);
  if (parent !== null && parent.dir === dir) {
    const i = parent.children.findIndex((c) => c.id === target);
    const half = (parent.sizes[i] ?? 1) / 2;
    const children = [...parent.children];
    const sizes = [...parent.sizes];
    sizes[i] = half;
    children.splice(before(edge) ? i : i + 1, 0, add);
    sizes.splice(before(edge) ? i : i + 1, 0, half);
    return replace(root, parent.id, () => ({ ...parent, children, sizes }));
  }
  return replace(root, target, (n) => split(dir, before(edge) ? [add, n] : [n, add]));
}

function parentOf(n: Node, id: string): Split | null {
  if (n.kind === "stack") return null;
  for (const c of n.children) {
    if (c.id === id) return n;
    const p = parentOf(c, id);
    if (p !== null) return p;
  }
  return null;
}

// Docks a pane on a stack: its centre joins the stack as a tab, an edge
// splits the stack there. The pane leaves wherever it was; a pane the tree
// does not hold yet (a new terminal) is added. Dropping the only pane of a
// stack on that stack changes nothing.
export function dock(l: Layout, pane: PaneId, target: string, zone: Zone): Layout {
  const from = stackOf(l.root, pane);
  const to = findNode(l.root, target);
  if (to === null || to.kind !== "stack") return l;
  if (from !== null && from.id === to.id && (zone === "center" || from.panes.length === 1)) {
    return zone === "center" ? activate(l, pane) : l;
  }
  // The conversation may move, but never out of the tree: lift it first.
  const lifted = from === null ? l : liftPane(l, pane);
  if (findNode(lifted.root, target) === null || (from !== null && contains(lifted.root, pane))) return l;
  const root =
    zone === "center"
      ? replace(lifted.root, target, (n) => ({ ...(n as Stack), panes: [...(n as Stack).panes, pane], active: pane }))
      : besideNode(lifted.root, target, stack([pane]), zone);
  return withRoot(lifted, root);
}

// Docks a pane along an edge of the whole room, across its full width or
// height.
export function dockRoot(l: Layout, pane: PaneId, edge: Edge): Layout {
  const held = stackOf(l.root, pane) !== null;
  const lifted = held ? liftPane(l, pane) : l;
  if (held && contains(lifted.root, pane)) return l;
  const add = stack([pane]);
  const r = lifted.root;
  const dir = axis(edge);
  const root =
    r.kind === "split" && r.dir === dir
      ? {
          ...r,
          children: before(edge) ? [add, ...r.children] : [...r.children, add],
          sizes: before(edge) ? [sum(r.sizes) / 3, ...r.sizes] : [...r.sizes, sum(r.sizes) / 3],
        }
      : split(dir, before(edge) ? [add, r] : [r, add], before(edge) ? [1, 2] : [2, 1]);
  return withRoot(lifted, root);
}

// Like removePane, but the conversation may go too, as it is about to be
// docked again.
function liftPane(l: Layout, pane: PaneId): Layout {
  const s = stackOf(l.root, pane);
  if (s === null) return l;
  const root = replace(l.root, s.id, (n) => {
    const st = n as Stack;
    const panes = st.panes.filter((p) => p !== pane);
    return { ...st, panes, active: st.active === pane ? (panes[0] ?? "") : st.active };
  });
  const r = normalize(root);
  // A lone pane lifted leaves nothing; it goes back where it was.
  return r === null ? l : { ...l, root: r };
}

// A new terminal beside a pane (tmux's split-window): right or below.
export function splitPane(l: Layout, beside: PaneId, edge: Edge, term: PaneId = terminalId()): Layout {
  const s = stackOf(l.root, beside);
  if (s === null) return l;
  return dock(l, term, s.id, edge);
}

// Makes a pane its stack's open tab.
export function activate(l: Layout, pane: PaneId): Layout {
  const s = stackOf(l.root, pane);
  if (s === null || s.active === pane) return l;
  return { ...l, root: replace(l.root, s.id, (n) => ({ ...(n as Stack), active: pane })) };
}

export function toggleZoom(l: Layout, pane: PaneId): Layout {
  if (l.zoom === pane) return { ...l, zoom: null };
  return contains(l.root, pane) ? { ...l, zoom: pane } : l;
}

export function setHidden(l: Layout, pane: PaneId, hidden: boolean): Layout {
  const has = l.hidden.includes(pane);
  if (has === hidden || pane === CONVERSATION) return l;
  return { ...l, hidden: hidden ? [...l.hidden, pane] : l.hidden.filter((p) => p !== pane), zoom: hidden && l.zoom === pane ? null : l.zoom };
}

export function setSizes(l: Layout, splitId: string, sizes: number[]): Layout {
  const n = findNode(l.root, splitId);
  if (n === null || n.kind !== "split" || sizes.length !== n.children.length || !sizes.every((s) => Number.isFinite(s) && s > 0)) return l;
  return { ...l, root: replace(l.root, splitId, () => ({ ...n, sizes })) };
}

// Moves the border between children a and b of a split by delta, in the
// split's own share units, keeping each at least its minimum share.
export function resizeSizes(sizes: readonly number[], a: number, b: number, delta: number, minA: number, minB: number): number[] {
  const sa = sizes[a];
  const sb = sizes[b];
  if (sa === undefined || sb === undefined) return [...sizes];
  const pair = sa + sb;
  const lo = Math.min(minA, pair / 2);
  const hi = pair - Math.min(minB, pair / 2);
  const na = Math.min(hi, Math.max(lo, sa + delta));
  const out = [...sizes];
  out[a] = na;
  out[b] = pair - na;
  return out;
}

// Gives the top-level part of the room that holds a pane a share of the
// room (the side panel's Widen), the others keeping their ratios.
export function setShare(l: Layout, pane: PaneId, share: number): Layout {
  const r = l.root;
  if (r.kind !== "split") return l;
  const i = r.children.findIndex((c) => contains(c, pane));
  if (i < 0) return l;
  const rest = sum(r.sizes) - (r.sizes[i] ?? 0);
  if (rest <= 0) return l;
  const s = Math.min(0.9, Math.max(0.1, share));
  const sizes = r.sizes.map((x, j) => (j === i ? s : ((1 - s) * x) / rest));
  return { ...l, root: { ...r, sizes } };
}

// The share of the room's top level that holds a pane.
export function shareOf(l: Layout, pane: PaneId): number {
  const r = l.root;
  if (r.kind !== "split") return 1;
  const i = r.children.findIndex((c) => contains(c, pane));
  return i < 0 ? 0 : (r.sizes[i] ?? 0) / sum(r.sizes);
}

// Puts every panel the tree is missing (a view added since the layout was
// saved) with the others: in the stack that holds Changes, else in a stack
// of their own on the right.
export function complete(l: Layout): Layout {
  const have = new Set(panesOf(l.root));
  const missing = PANEL_IDS.filter((p) => !have.has(p));
  if (missing.length === 0) return l;
  const home = stackOf(l.root, "changes") ?? stacksOf(l.root).find((s) => s.panes.every(isPanel)) ?? null;
  const root =
    home === null
      ? split("row", [l.root, stack(missing)], [0.62, 0.38])
      : replace(l.root, home.id, (n) => ({ ...(n as Stack), panes: [...(n as Stack).panes, ...missing] }));
  return withRoot(l, root);
}

// ── Presets ──

export type Preset = "default" | "terminal-below" | "terminal-right" | "focus" | "review";

export const PRESETS: ReadonlyArray<{ id: Preset; label: string; hint: string }> = [
  { id: "default", label: "Default", hint: "The conversation, the terminal under it, the panel beside" },
  { id: "terminal-below", label: "Terminal below", hint: "The terminal across the bottom" },
  { id: "terminal-right", label: "Terminal right", hint: "The terminal beside the conversation" },
  { id: "focus", label: "Focus", hint: "The conversation alone" },
  { id: "review", label: "Review", hint: "The conversation, the diff and the agents" },
];

// What a preset shows besides its arrangement: the terminal, and a panel.
export interface PresetShow {
  terminal: boolean | null;
  panel: Panel | null | undefined;
}

// A preset's tree. side is where the side panel sits (Settings > Layout);
// terms are the terminal panes there are, which keep their shells.
export function preset(id: Preset, side: "left" | "right", terms: readonly PaneId[] = []): Layout {
  const t = terms.length > 0 ? [...terms] : ["term:1"];
  const panels = (except: readonly string[] = []) => stack(PANEL_IDS.filter((p) => !except.includes(p)));
  const beside = (main: Node, panel: Node, mainShare: number): Node =>
    side === "right" ? split("row", [main, panel], [mainShare, 1 - mainShare]) : split("row", [panel, main], [1 - mainShare, mainShare]);
  const conv = () => stack([CONVERSATION]);
  let root: Node;
  switch (id) {
    case "terminal-below":
      root = split("col", [beside(conv(), panels(), 0.56), stack(t)], [0.68, 0.32]);
      break;
    case "terminal-right":
      root = split("row", [conv(), stack(t), panels()], [0.46, 0.3, 0.24]);
      if (side === "left") root = split("row", [panels(), conv(), stack(t)], [0.24, 0.46, 0.3]);
      break;
    case "review":
      root = beside(split("col", [conv(), stack(t)], [0.7, 0.3]), split("col", [panels(["agents"]), stack(["agents"])], [0.62, 0.38]), 0.5);
      break;
    default:
      // The side panel about as wide as it was at a fixed 480px.
      root = beside(split("col", [conv(), stack(t)], [0.7, 0.3]), panels(), 0.56);
  }
  return { v: 1, root: normalize(root) ?? root, hidden: [], zoom: null };
}

export function presetShows(id: Preset): PresetShow {
  switch (id) {
    case "terminal-below":
    case "terminal-right":
      return { terminal: true, panel: undefined };
    case "focus":
      return { terminal: false, panel: null };
    case "review":
      return { terminal: false, panel: "changes" };
    default:
      return { terminal: null, panel: undefined };
  }
}

// Focus keeps the arrangement and hides the rest; the others are trees.
export function applyPreset(l: Layout, id: Preset, side: "left" | "right"): Layout {
  if (id === "focus") return { ...l, zoom: null };
  return preset(id, side, terminalsOf(l.root));
}

// ── Saving ──

const MAX_DEPTH = 12;
const MAX_CHILDREN = 16;
const ID = /^[\w-]{1,40}$/;

// A saved layout, checked: anything that is not a tree of known panes,
// each once, with the conversation in it, is refused (null) and the window
// falls back to Default. What can be mended is: unknown panes (a view a
// later version added) drop out, missing panels join the others, bad
// shares even out, ids that clash are renewed.
export function validate(raw: unknown): Layout | null {
  if (typeof raw !== "object" || raw === null) return null;
  const o = raw as Record<string, unknown>;
  if (o["v"] !== 1) return null;
  const seen = new Set<PaneId>();
  const ids = new Set<string>();
  let bad = false;
  const freshId = (v: unknown): string => {
    const id = typeof v === "string" && ID.test(v) && !ids.has(v) ? v : nodeId();
    ids.add(id);
    return id;
  };
  const walk = (v: unknown, depth: number): Node | null => {
    if (bad || depth > MAX_DEPTH || typeof v !== "object" || v === null) {
      bad = true;
      return null;
    }
    const n = v as Record<string, unknown>;
    if (n["kind"] === "stack") {
      if (!Array.isArray(n["panes"])) {
        bad = true;
        return null;
      }
      const panes: PaneId[] = [];
      for (const p of n["panes"] as unknown[]) {
        if (!knownPane(p)) continue;
        if (seen.has(p)) {
          bad = true;
          return null;
        }
        seen.add(p);
        panes.push(p);
      }
      if (panes.length === 0) return null;
      const active = typeof n["active"] === "string" && panes.includes(n["active"]) ? n["active"] : (panes[0] as PaneId);
      return { kind: "stack", id: freshId(n["id"]), panes, active };
    }
    if (n["kind"] === "split") {
      const dir = n["dir"];
      const kids = n["children"];
      if ((dir !== "row" && dir !== "col") || !Array.isArray(kids) || kids.length > MAX_CHILDREN) {
        bad = true;
        return null;
      }
      const rawSizes = Array.isArray(n["sizes"]) ? (n["sizes"] as unknown[]) : [];
      const okSizes = rawSizes.length === kids.length && rawSizes.every((s) => typeof s === "number" && Number.isFinite(s) && s > 0);
      const children: Node[] = [];
      const sizes: number[] = [];
      kids.forEach((k, i) => {
        const c = walk(k, depth + 1);
        if (c === null) return;
        children.push(c);
        sizes.push(okSizes ? (rawSizes[i] as number) : 1);
      });
      if (bad) return null;
      return { kind: "split", id: freshId(n["id"]), dir, children, sizes };
    }
    bad = true;
    return null;
  };
  const root = walk(o["root"], 0);
  if (bad || root === null || !seen.has(CONVERSATION)) return null;
  const tree = normalize(root);
  if (tree === null) return null;
  const whole = complete({ v: 1, root: tree, hidden: [], zoom: null });
  const has = new Set(panesOf(whole.root));
  const hidden = Array.isArray(o["hidden"]) ? [...new Set((o["hidden"] as unknown[]).filter((p): p is PaneId => knownPane(p) && p !== CONVERSATION && has.has(p)))] : [];
  const zoom = knownPane(o["zoom"]) && has.has(o["zoom"]) ? o["zoom"] : null;
  return { ...whole, hidden, zoom };
}

function sum(xs: readonly number[]): number {
  return xs.reduce((a, b) => a + b, 0) || 1;
}
