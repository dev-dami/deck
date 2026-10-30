import { createPortal } from "react-dom";
import { type CSSProperties, Fragment, type ReactNode, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { cn } from "../lib/cn";
import { WIDE_PX } from "../ui";
import { Button } from "../ui/kit/button";
import { MenuItem, MenuSeparator, Popover } from "../ui/kit/menu";
import { segmentItem, segmentTrack } from "../ui/kit/segmented";
import { ColumnsPlusRight, CornersIn, CornersOut, Ellipsis, type IconType, Maximize2, Minimize2, Plus, RowsPlusBottom, X } from "../ui/icons";
import type { LayoutControl } from "./store";
import {
  activate,
  CONVERSATION,
  contains,
  dock,
  dockRoot,
  type Edge,
  isPanel,
  isTerminal,
  type Layout,
  type Node,
  type PaneId,
  panesOf,
  resizeSizes,
  setShare,
  setSizes,
  type Split,
  type Stack,
  shareOf,
  stackOf,
  stacksOf,
  type Zone,
} from "./tree";

// The room as a layout of panes (tree.ts), tmux-like: splits with dividers
// that drag (or take the arrow keys), stacks of tabs, tabs that drag onto
// another stack's edge to split it or onto its middle to join it, a zoom,
// and more than one terminal. The panes' content renders once, into a host
// element of its own that moves between the stacks (a portal), so moving a
// pane keeps its state; a pane the window hides keeps its place and its
// stack collapses. A split too narrow for its panes floats them over the
// one that holds the conversation, as the side panel floated in a narrow
// window.

// What the Workspace needs to know of a pane.
export interface PaneSpec {
  label: string;
  icon: IconType;
  // After the label: a count, a terminal's dev servers.
  extras?: ReactNode;
  // Rendered while hidden too (the conversation, terminals), so it keeps
  // its state; the panel's views mount only while they show, as before.
  keepAlive: boolean;
  // The tab's close: a terminal's shell ends; null when it has none.
  close: string | null;
}

export interface PaneOps {
  // A tab was opened (the Shell keeps the side panel's tab in step).
  activate: (p: PaneId) => void;
  // A pane's Close: a terminal ends, a view hides.
  close: (p: PaneId) => void;
  // A stack's own close: the side panel or the terminal hides.
  hide: (group: "panel" | "terminal") => void;
  // A new terminal, as a tab of a stack or beside a pane.
  newTerminal: (stackId: string) => void;
  split: (p: PaneId, edge: Edge) => void;
  zoom: (p: PaneId) => void;
  focus: (p: PaneId) => void;
}

const GAP = 8;
// Each pane's least width and height: the conversation keeps 30rem as it
// did beside the panel; a terminal shrinks the furthest.
const MIN: Record<"conversation" | "terminal" | "panel", { w: number; h: number }> = {
  conversation: { w: 480, h: 260 },
  terminal: { w: 220, h: 104 },
  panel: { w: 320, h: 160 },
};
const kindOf = (p: PaneId) => (p === CONVERSATION ? "conversation" : isTerminal(p) ? "terminal" : "panel");
const KEY_STEP = 16;
// The room's toolbar, which a float over the conversation keeps clear of.
const HEADER = 56;
const FLOAT_W = 480;

interface Ctx {
  layout: Layout;
  ctl: LayoutControl;
  vis: (p: PaneId) => boolean;
  spec: (p: PaneId) => PaneSpec;
  ops: PaneOps;
  float: boolean;
  focused: PaneId | null;
  ring: boolean;
  corner: string | null;
  slot: (p: PaneId) => (el: HTMLDivElement | null) => (() => void) | undefined;
  startDrag: (p: PaneId, e: React.PointerEvent) => void;
  mac: boolean;
  // Inside a part that floats over the conversation.
  floating?: boolean;
}

const shownNode = (n: Node, vis: (p: PaneId) => boolean): boolean => panesOf(n).some(vis);

function minOf(n: Node, vis: (p: PaneId) => boolean, axis: "w" | "h"): number {
  if (n.kind === "stack") return Math.max(0, ...n.panes.filter(vis).map((p) => MIN[kindOf(p)][axis]));
  const kids = n.children.filter((c) => shownNode(c, vis)).map((c) => minOf(c, vis, axis));
  if (kids.length === 0) return 0;
  const along = (n.dir === "row") === (axis === "w");
  return along ? kids.reduce((a, b) => a + b, 0) + GAP * (kids.length - 1) : Math.max(...kids);
}

function useSize(): [(el: HTMLDivElement | null) => (() => void) | undefined, { w: number; h: number }] {
  const [size, setSize] = useState({ w: 0, h: 0 });
  const ref = useCallback((el: HTMLDivElement | null) => {
    if (el === null) return;
    const set = () => {
      const r = el.getBoundingClientRect();
      setSize((s) => (s.w === r.width && s.h === r.height ? s : { w: r.width, h: r.height }));
    };
    set();
    const watch = new ResizeObserver(set);
    watch.observe(el);
    return () => watch.disconnect();
  }, []);
  return [ref, size];
}

function NodeView({ node, ctx, root = false }: { node: Node; ctx: Ctx; root?: boolean }) {
  return node.kind === "stack" ? <StackView node={node} ctx={ctx} /> : <SplitView node={node} ctx={ctx} root={root} />;
}

function SplitView({ node, ctx, root }: { node: Split; ctx: Ctx; root: boolean }) {
  const [measure, size] = useSize();
  // The sizes while a divider drags; the layout takes them when it drops.
  const [draft, setDraft] = useState<number[] | null>(null);
  const sizes = draft ?? node.sizes;
  const row = node.dir === "row";
  const total = row ? size.w : size.h;
  const shown = node.children.map((c) => shownNode(c, ctx.vis));
  const idx = shown.flatMap((s, i) => (s ? [i] : []));
  const mins = node.children.map((c) => minOf(c, ctx.vis, row ? "w" : "h"));
  const need = idx.reduce((a, i) => a + (mins[i] ?? 0), 0) + GAP * Math.max(0, idx.length - 1);
  // The child that stays in place when the others float: the one with the
  // conversation, else the largest.
  const withRoom = idx.find((i) => contains(node.children[i] as Node, CONVERSATION) && ctx.vis(CONVERSATION));
  const primary =
    withRoom ??
    idx.reduce((best, i) => ((sizes[i] ?? 0) > (sizes[best] ?? 0) ? i : best), idx[0] ?? 0);
  // The person floats the panel (Settings > Layout): what sits beside the
  // conversation at the top floats over it at any width.
  const floatAll = ctx.float && root && row && idx.some((i) => i !== primary);
  const overlay = idx.length > 1 && total > 0 && (total + 0.5 < need || floatAll);
  const shownSum = idx.reduce((a, i) => a + (sizes[i] ?? 0), 0) || 1;
  const avail = Math.max(1, total - GAP * Math.max(0, idx.length - 1));

  const style = (i: number): CSSProperties => {
    const min = mins[i] ?? 0;
    if (!overlay || i === primary) {
      // Grow factors that sum to one, so the shown children fill the split
      // (factors summing under one would leave the rest empty).
      return overlay ? { flex: "1 1 0px" } : { flex: `${(sizes[i] ?? 1) / shownSum} 1 0px`, [row ? "minWidth" : "minHeight"]: min };
    }
    // A float is as wide as its share would make it, and no narrower than
    // the side panel was when it floated (480px), leaving the conversation
    // 4rem.
    const want = Math.max(((sizes[i] ?? 0) / shownSum) * avail, row ? FLOAT_W : 0);
    const px = Math.round(Math.min(Math.max(want, min), Math.max(min, total - 64)));
    const start = i < primary;
    // Over the conversation, a float leaves its toolbar uncovered, so the
    // panes' buttons and the Layout menu stay in reach.
    const top = withRoom !== undefined ? HEADER : 12;
    // Floats on the same side share it, one after another across the
    // split, rather than lie on top of each other.
    const side = idx.filter((j) => j !== primary && j < primary === start);
    const k = side.length;
    const n = side.indexOf(i);
    const cross = row ? size.h - top - 12 : size.w - 24;
    const each = (cross - GAP * (k - 1)) / k;
    const at = (row ? top : 12) + n * (each + GAP);
    return row
      ? { position: "absolute", top: at, height: each, [start ? "left" : "right"]: 12, width: Math.min(px, total - 24), zIndex: 30 }
      : { position: "absolute", left: at, width: each, [start ? "top" : "bottom"]: start ? top : 12, height: Math.min(px, total - 24 - top), zIndex: 30 };
  };

  return (
    <div ref={measure} className={cn("split relative flex min-h-0 min-w-0 flex-1", row ? "flex-row" : "flex-col")} data-split={node.dir} data-overlay={overlay ? "" : undefined}>
      {node.children.map((c, i) => {
        const prev = idx.filter((j) => j < i).pop();
        const floating = overlay && i !== primary && shown[i] === true;
        return (
          <Fragment key={c.id}>
            {!overlay && shown[i] === true && prev !== undefined && (
              <Divider
                ctx={ctx}
                node={node}
                a={prev}
                b={i}
                row={row}
                perPx={shownSum / avail}
                mins={[mins[prev] ?? 0, mins[i] ?? 0]}
                sizes={sizes}
                setDraft={setDraft}
              />
            )}
            <div
              className={cn("split-child relative flex min-h-0 min-w-0", floating && "pane-float")}
              style={style(i)}
              hidden={shown[i] !== true}
              data-float={floating ? "" : undefined}
            >
              <NodeView node={c} ctx={floating ? { ...ctx, floating: true } : ctx} />
            </div>
          </Fragment>
        );
      })}
    </div>
  );
}

// The first visible pane's name, for a divider's label.
function nameOf(n: Node, ctx: Ctx): string {
  const p = panesOf(n).find(ctx.vis);
  return p === undefined ? "" : ctx.spec(p).label;
}

// The border between two children of a split: drag it, or focus it and use
// the arrow keys; a double-click (or Enter) shares the space evenly again.
function Divider({
  ctx,
  node,
  a,
  b,
  row,
  perPx,
  mins,
  sizes,
  setDraft,
}: {
  ctx: Ctx;
  node: Split;
  a: number;
  b: number;
  row: boolean;
  // Share units per pixel.
  perPx: number;
  mins: [number, number];
  sizes: number[];
  setDraft: (s: number[] | null) => void;
}) {
  const move = (from: number[], px: number) => resizeSizes(from, a, b, px * perPx, mins[0] * perPx, mins[1] * perPx);
  const commit = (next: number[]) => ctx.ctl.edit((l) => setSizes(l, node.id, next));
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    if (e.button !== 0) return;
    e.preventDefault();
    const el = e.currentTarget;
    el.setPointerCapture(e.pointerId);
    const start = row ? e.clientX : e.clientY;
    const from = [...sizes];
    let last = from;
    document.documentElement.dataset["resizing"] = row ? "col" : "row";
    const onMove = (ev: PointerEvent) => {
      last = move(from, (row ? ev.clientX : ev.clientY) - start);
      setDraft(last);
    };
    const onUp = () => {
      el.removeEventListener("pointermove", onMove);
      el.removeEventListener("pointerup", onUp);
      el.removeEventListener("pointercancel", onUp);
      delete document.documentElement.dataset["resizing"];
      setDraft(null);
      if (last !== from) commit(last);
    };
    el.addEventListener("pointermove", onMove);
    el.addEventListener("pointerup", onUp);
    el.addEventListener("pointercancel", onUp);
  };
  const even = () => {
    const pair = (sizes[a] ?? 1) + (sizes[b] ?? 1);
    const next = [...sizes];
    next[a] = pair / 2;
    next[b] = pair / 2;
    commit(next);
  };
  const share = Math.round((100 * (sizes[a] ?? 0)) / ((sizes[a] ?? 0) + (sizes[b] ?? 0) || 1));
  const less = row ? "ArrowLeft" : "ArrowUp";
  const more = row ? "ArrowRight" : "ArrowDown";
  return (
    <div
      role="separator"
      aria-orientation={row ? "vertical" : "horizontal"}
      aria-label={`Resize ${nameOf(node.children[a] as Node, ctx)} and ${nameOf(node.children[b] as Node, ctx)}`}
      aria-valuemin={0}
      aria-valuemax={100}
      aria-valuenow={share}
      tabIndex={0}
      title="Drag to resize; double-click to share evenly"
      className={cn(
        "pane-divider no-drag group/div relative z-20 flex shrink-0 justify-center outline-none",
        row ? "w-2 cursor-col-resize flex-row" : "h-2 cursor-row-resize flex-col",
      )}
      onPointerDown={onPointerDown}
      onDoubleClick={even}
      onKeyDown={(e) => {
        if (e.key === "Enter") {
          e.preventDefault();
          even();
          return;
        }
        if (e.key !== less && e.key !== more) return;
        e.preventDefault();
        commit(move(sizes, e.key === more ? KEY_STEP : -KEY_STEP));
      }}
    >
      <span
        aria-hidden
        className={cn(
          "self-center rounded-full bg-primary opacity-0 transition-opacity duration-150 group-hover/div:opacity-60 group-focus-visible/div:opacity-100 group-active/div:opacity-100",
          row ? "h-10 w-0.5" : "h-0.5 w-10",
        )}
      />
    </div>
  );
}

type StackKind = "room" | "panel" | "terminal" | "mixed";
const stackKind = (s: Stack): StackKind =>
  s.panes.includes(CONVERSATION) ? "room" : s.panes.every(isPanel) ? "panel" : s.panes.every(isTerminal) ? "terminal" : "mixed";

function StackView({ node, ctx }: { node: Stack; ctx: Ctx }) {
  const tabs = node.panes.filter(ctx.vis);
  const active = tabs.includes(node.active) ? node.active : (tabs[0] ?? null);
  const kind = stackKind(node);
  const [measure, size] = useSize();
  const floating = ctx.floating === true;
  const focused = ctx.focused !== null && node.panes.includes(ctx.focused);
  const lone = tabs.length === 1 && active === CONVERSATION;
  const props = {
    ref: measure,
    className: cn(
      // Floating, it fills the part that floats (and reads as positioned
      // over the room, as the side panel did).
      "stack sheet flex min-h-0 min-w-0 flex-1 flex-col overflow-hidden rounded-(--sheet-radius)",
      floating ? "absolute inset-0" : "relative",
      kind === "room" && "room-sheet",
      kind === "panel" && "panel",
      kind === "terminal" && "terminal-drawer",
    ),
    "data-stack-id": node.id,
    "data-stack": kind,
    "data-float": floating ? "" : undefined,
    "data-focused": focused && ctx.ring ? "" : undefined,
    "data-corner": ctx.corner === node.id ? "" : undefined,
    "data-yolo-region": kind === "panel" ? "" : undefined,
    hidden: tabs.length === 0,
  };
  const body = (
    <>
      {!lone && active !== null && <StackHead node={node} tabs={tabs} active={active} kind={kind} ctx={ctx} width={size.w} />}
      {lone && <Grip ctx={ctx} />}
      <div className="stack-body relative flex min-h-0 flex-1 flex-col">
        {node.panes.map((p) => (
          <div key={p} ref={ctx.slot(p)} className="pane-slot flex min-h-0 min-w-0 flex-1 flex-col" hidden={p !== active} data-slot={p} />
        ))}
      </div>
    </>
  );
  if (kind === "panel") {
    return (
      <aside {...props} aria-label="Side panel">
        {body}
      </aside>
    );
  }
  if (kind === "terminal") {
    return (
      <section {...props} aria-label="Terminal">
        {body}
      </section>
    );
  }
  if (kind === "mixed") {
    return (
      <section {...props} aria-label="Panes">
        {body}
      </section>
    );
  }
  return <div {...props}>{body}</div>;
}

// The conversation's handle while it has a stack to itself: a short bar on
// the sheet's top edge, as a window's, that drags it to another place.
function Grip({ ctx }: { ctx: Ctx }) {
  return (
    <button
      type="button"
      aria-label="Move the conversation"
      title="Drag to move the conversation"
      // A pointer's handle; the keys arrange the panes from the Layout menu.
      tabIndex={-1}
      className="pane-grip no-drag group/grip absolute top-0 left-1/2 z-30 flex h-2.5 w-16 -translate-x-1/2 cursor-grab touch-none items-start justify-center outline-none"
      onPointerDown={(e) => ctx.startDrag(CONVERSATION, e)}
    >
      <span
        aria-hidden
        className="mt-[3px] h-1 w-9 rounded-full bg-hairline-strong opacity-0 transition-opacity group-hover/grip:opacity-100 group-focus-visible/grip:bg-primary group-focus-visible/grip:opacity-100"
      />
    </button>
  );
}

function StackHead({ node, tabs, active, kind, ctx, width }: { node: Stack; tabs: PaneId[]; active: PaneId; kind: StackKind; ctx: Ctx; width: number }) {
  const term = kind === "terminal";
  const tab = (p: PaneId) => {
    const s = ctx.spec(p);
    const on = p === active;
    const Icon = s.icon;
    const select = () => {
      ctx.ctl.edit((l) => activate(l, p));
      ctx.ops.activate(p);
    };
    const handle = {
      role: "tab" as const,
      "aria-selected": on,
      "data-pane": p,
      onPointerDown: (e: React.PointerEvent) => ctx.startDrag(p, e),
      onClick: select,
    };
    if (term) {
      return (
        <div
          key={p}
          className={cn(
            "group flex h-7 shrink-0 items-center gap-1.5 rounded-full pr-1 pl-3 text-xs transition-colors",
            on ? "bg-selected font-medium text-foreground" : "text-muted-foreground hover:bg-hover hover:text-foreground",
          )}
        >
          <button type="button" {...handle} className="flex cursor-pointer touch-none items-center gap-1.5 whitespace-nowrap outline-none focus-visible:underline">
            <Icon className="size-3.5" />
            {s.label}
          </button>
          {s.extras}
          {s.close !== null && (
            <button
              type="button"
              aria-label={s.close}
              className="grid size-5 cursor-pointer place-items-center rounded-full text-muted-foreground opacity-0 outline-none hover:bg-hover hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-focus-within:opacity-100 group-hover:opacity-100"
              onClick={() => ctx.ops.close(p)}
            >
              <X className="size-3" />
            </button>
          )}
        </div>
      );
    }
    return (
      <button key={p} type="button" {...handle} aria-label={s.label} title={s.label} className={cn(segmentItem({ on }), "touch-none px-2.5")}>
        <Icon className="size-3.5" weight={on ? "fill" : "regular"} />
        {/* Many tabs in a narrow stack: only the open one keeps its name. */}
        <span className={on || tabs.length < 5 ? undefined : "hidden @[36rem]/head:inline"}>{s.label}</span>
        {s.extras}
      </button>
    );
  };
  const menu = <PaneMenu pane={active} ctx={ctx} />;
  if (term) {
    return (
      <header className="stack-head flex h-10 shrink-0 items-center gap-1 border-hairline border-b px-2">
        <div className="flex min-w-0 items-center gap-1 overflow-x-auto [scrollbar-width:none]" role="tablist" aria-label="Terminals">
          {tabs.map(tab)}
          <Button variant="ghost-muted" size="icon-sm" aria-label="New terminal" title="New terminal" onClick={() => ctx.ops.newTerminal(node.id)}>
            <Plus />
          </Button>
        </div>
        <div className="ml-auto flex shrink-0 items-center gap-0.5">
          {menu}
          <Button variant="ghost-muted" size="icon-sm" aria-label="Hide the terminal" title="Hide the terminal" onClick={() => ctx.ops.hide("terminal")}>
            <X />
          </Button>
        </div>
      </header>
    );
  }
  const wide = width >= WIDE_PX;
  return (
    <header className={cn("stack-head drag @container/head flex h-12 shrink-0 items-center gap-2 px-2.5", kind === "panel" && "panel-head")}>
      <h2 className="sr-only">{ctx.spec(active).label}</h2>
      <div className={cn(segmentTrack(), "min-w-0 overflow-x-auto [scrollbar-width:none]")} role="tablist" aria-label={kind === "panel" ? "Room" : "Panes"}>
        {tabs.map(tab)}
      </div>
      <div className="liquid-clear ml-auto flex shrink-0 items-center gap-0.5 rounded-full p-0.5">
        {menu}
        {kind === "panel" && (
          <Button
            variant="ghost-muted"
            size="icon-xs"
            aria-label={wide ? "Narrow the panel" : "Widen the panel"}
            title={wide ? "Narrow the panel" : "Widen the panel"}
            onClick={() =>
              ctx.ctl.edit((l) => {
                // Narrow: back to about 480px, at most Default's share.
                const share = shareOf(l, active);
                const room = share > 0 ? width / share : width;
                return setShare(l, active, wide ? Math.min(0.44, 480 / room) : Math.max(0.6, share));
              })
            }
          >
            {wide ? <Minimize2 /> : <Maximize2 />}
          </Button>
        )}
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label={kind === "panel" ? "Close the panel" : `Close ${ctx.spec(active).label}`}
          title={kind === "panel" ? "Close the panel" : `Close ${ctx.spec(active).label}`}
          onClick={() => (kind === "panel" ? ctx.ops.hide("panel") : ctx.ops.close(active))}
        >
          <X />
        </Button>
      </div>
    </header>
  );
}

// A pane's menu: split it with a new terminal, zoom it, close it.
function PaneMenu({ pane, ctx }: { pane: PaneId; ctx: Ctx }) {
  const [open, setOpen] = useState(false);
  const run = (f: () => void) => () => {
    setOpen(false);
    f();
  };
  const zoomed = ctx.layout.zoom !== null;
  const m = ctx.mac ? "⌘" : "Ctrl+";
  const s = ctx.spec(pane);
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      align="end"
      label="Pane"
      className="w-60"
      trigger={
        <Button variant="ghost-muted" size="icon-xs" aria-label="Pane options" title="Pane options" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <Ellipsis weight="bold" />
        </Button>
      }
    >
      <MenuItem onClick={run(() => ctx.ops.split(pane, "right"))}>
        <ColumnsPlusRight /> <span className="flex-1">Split right</span>
        <Hint>{m}⇧\</Hint>
      </MenuItem>
      <MenuItem onClick={run(() => ctx.ops.split(pane, "bottom"))}>
        <RowsPlusBottom /> <span className="flex-1">Split down</span>
        <Hint>{m}⇧-</Hint>
      </MenuItem>
      <MenuItem onClick={run(() => ctx.ops.zoom(pane))}>
        {zoomed ? <CornersIn /> : <CornersOut />} <span className="flex-1">{zoomed ? "Unzoom" : "Zoom"}</span>
        <Hint>{m}⇧M</Hint>
      </MenuItem>
      {pane !== CONVERSATION && (
        <>
          <MenuSeparator />
          <MenuItem onClick={run(() => ctx.ops.close(pane))}>
            <X /> {s.close ?? `Hide ${s.label}`}
          </MenuItem>
        </>
      )}
    </Popover>
  );
}

function Hint({ children }: { children: ReactNode }) {
  return <span className="text-2xs text-faint tabular-nums">{children}</span>;
}

// ── Drag to dock ──

type Target = { kind: "stack"; id: string; zone: Zone } | { kind: "root"; edge: Edge };

interface Drag {
  pane: PaneId;
  x: number;
  y: number;
  target: Target | null;
}

// Within this share of a stack's side, a drop splits it there; further in,
// it joins the stack.
const EDGE_SHARE = 0.28;
// Within this many pixels of the room's own edge, a drop runs along it.
const ROOT_EDGE = 12;

function targetAt(root: HTMLElement, x: number, y: number): Target | null {
  const box = root.getBoundingClientRect();
  if (x < box.left || x > box.right || y < box.top || y > box.bottom) return null;
  const near: Array<[Edge, number]> = [
    ["left", x - box.left],
    ["right", box.right - x],
    ["top", y - box.top],
    ["bottom", box.bottom - y],
  ];
  const edge = near.sort((p, q) => p[1] - q[1])[0];
  if (edge !== undefined && edge[1] < ROOT_EDGE) return { kind: "root", edge: edge[0] };
  const el = document.elementFromPoint(x, y)?.closest<HTMLElement>("[data-stack-id]");
  if (el === null || el === undefined || !root.contains(el)) return null;
  const r = el.getBoundingClientRect();
  const fx = (x - r.left) / r.width;
  const fy = (y - r.top) / r.height;
  const sides: Array<[Edge, number]> = [
    ["left", fx],
    ["right", 1 - fx],
    ["top", fy],
    ["bottom", 1 - fy],
  ];
  const side = sides.sort((p, q) => p[1] - q[1])[0];
  const id = el.dataset["stackId"] ?? "";
  return side !== undefined && side[1] < EDGE_SHARE ? { kind: "stack", id, zone: side[0] } : { kind: "stack", id, zone: "center" };
}

const ZONE_LABEL: Record<Zone, string> = { left: "Split left", right: "Split right", top: "Split up", bottom: "Split down", center: "Add as a tab" };
const ROOT_LABEL: Record<Edge, string> = { left: "Along the left", right: "Along the right", top: "Across the top", bottom: "Across the bottom" };

// Where the drop would go, in the room's coordinates.
function zoneRect(root: HTMLElement, t: Target): { style: CSSProperties; label: string } | null {
  const box = root.getBoundingClientRect();
  if (t.kind === "root") {
    const third = (n: number) => Math.round(n / 3);
    const s: CSSProperties =
      t.edge === "left"
        ? { left: 0, top: 0, bottom: 0, width: third(box.width) }
        : t.edge === "right"
          ? { right: 0, top: 0, bottom: 0, width: third(box.width) }
          : t.edge === "top"
            ? { left: 0, right: 0, top: 0, height: third(box.height) }
            : { left: 0, right: 0, bottom: 0, height: third(box.height) };
    return { style: s, label: ROOT_LABEL[t.edge] };
  }
  const el = root.querySelector<HTMLElement>(`[data-stack-id="${CSS.escape(t.id)}"]`);
  if (el === null) return null;
  const r = el.getBoundingClientRect();
  let left = r.left - box.left;
  let top = r.top - box.top;
  let width = r.width;
  let height = r.height;
  if (t.zone === "left" || t.zone === "right") {
    width = r.width / 2;
    if (t.zone === "right") left += width;
  } else if (t.zone === "top" || t.zone === "bottom") {
    height = r.height / 2;
    if (t.zone === "bottom") top += height;
  }
  return { style: { left, top, width, height }, label: ZONE_LABEL[t.zone] };
}

// ── The room ──

export function Workspace({
  ctl,
  visible,
  spec,
  render,
  ops,
  float,
  focused,
  ring,
  onFocused,
  mac,
}: {
  ctl: LayoutControl;
  // Whether the window shows a pane now: the terminal's toggle, the side
  // panel's, the views the Deck serves, the ones hidden from the menu.
  visible: (p: PaneId) => boolean;
  spec: (p: PaneId) => PaneSpec;
  // A pane's content; shown says whether it is on screen now.
  render: (p: PaneId, shown: boolean) => ReactNode;
  ops: PaneOps;
  float: boolean;
  focused: PaneId | null;
  // Mark the focused pane (after the keys moved between panes).
  ring: boolean;
  // The person clicked into a pane, or the focus went into it.
  onFocused: (p: PaneId, pointer: boolean) => void;
  mac: boolean;
}) {
  const { layout } = ctl;
  const rootRef = useRef<HTMLDivElement>(null);
  // Zoomed, only the zoomed pane's stack shows.
  const zoomStack = layout.zoom !== null && visible(layout.zoom) ? stackOf(layout.root, layout.zoom) : null;
  const vis = useCallback((p: PaneId) => visible(p) && (zoomStack === null || zoomStack.panes.includes(p)), [visible, zoomStack]);

  // Each pane's content renders into a host of its own that the stacks
  // take in turn, so it keeps its state wherever the pane goes.
  const hosts = useRef(new Map<PaneId, HTMLDivElement>());
  const hostOf = (p: PaneId): HTMLDivElement => {
    let h = hosts.current.get(p);
    if (h === undefined) {
      h = document.createElement("div");
      h.className = "pane-host flex min-h-0 min-w-0 flex-1 flex-col";
      h.dataset["paneHost"] = p;
      hosts.current.set(p, h);
    }
    return h;
  };
  const slots = useRef(new Map<PaneId, (el: HTMLDivElement | null) => (() => void) | undefined>());
  const slot = (p: PaneId) => {
    let f = slots.current.get(p);
    if (f === undefined) {
      f = (el: HTMLDivElement | null) => {
        if (el === null) return;
        const h = hostOf(p);
        el.appendChild(h);
        return () => {
          if (h.parentElement === el) h.remove();
        };
      };
      slots.current.set(p, f);
    }
    return f;
  };

  // The pane the person works in is the one they last clicked or tabbed
  // into. The hosts sit in the stacks' DOM, so their events reach here.
  const latestFocus = useRef(onFocused);
  latestFocus.current = onFocused;
  useEffect(() => {
    const root = rootRef.current;
    if (root === null) return;
    const on = (e: Event) => {
      const host = (e.target as Element | null)?.closest?.<HTMLElement>("[data-pane-host]");
      const slot = (e.target as Element | null)?.closest?.<HTMLElement>("[data-stack-id]");
      const p = host?.dataset["paneHost"] ?? slot?.querySelector<HTMLElement>(".pane-slot:not([hidden])")?.dataset["slot"];
      if (p !== undefined) latestFocus.current(p, e.type === "pointerdown");
    };
    root.addEventListener("pointerdown", on, true);
    root.addEventListener("focusin", on);
    return () => {
      root.removeEventListener("pointerdown", on, true);
      root.removeEventListener("focusin", on);
    };
  }, []);

  const [drag, setDrag] = useState<Drag | null>(null);
  const startDrag = useCallback(
    (pane: PaneId, e: React.PointerEvent) => {
      if (e.button !== 0) return;
      const root = rootRef.current;
      if (root === null) return;
      const x0 = e.clientX;
      const y0 = e.clientY;
      let on = false;
      let target: Target | null = null;
      const move = (ev: PointerEvent) => {
        if (!on && Math.hypot(ev.clientX - x0, ev.clientY - y0) < 5) return;
        if (!on) {
          on = true;
          document.documentElement.dataset["dragging"] = "";
        }
        ev.preventDefault();
        target = targetAt(root, ev.clientX, ev.clientY);
        setDrag({ pane, x: ev.clientX, y: ev.clientY, target });
      };
      const end = (drop: boolean) => {
        window.removeEventListener("pointermove", move);
        window.removeEventListener("pointerup", up);
        window.removeEventListener("keydown", key, true);
        if (!on) return;
        delete document.documentElement.dataset["dragging"];
        setDrag(null);
        const t = target;
        if (!drop || t === null) return;
        ctl.edit((l) => (t.kind === "root" ? dockRoot(l, pane, t.edge) : dock(l, pane, t.id, t.zone)));
        ops.focus(pane);
      };
      const up = () => end(true);
      const key = (ev: KeyboardEvent) => {
        if (ev.key !== "Escape") return;
        ev.preventDefault();
        ev.stopPropagation();
        end(false);
      };
      window.addEventListener("pointermove", move);
      window.addEventListener("pointerup", up);
      window.addEventListener("keydown", key, true);
    },
    [ctl, ops],
  );
  // The top-left stack clears a Mac's traffic lights when nothing else does.
  const corner = stacksOf(layout.root).find((s) => s.panes.some(vis))?.id ?? null;
  const ctx: Ctx = { layout, ctl, vis, spec, ops, float, focused, ring, corner, slot, startDrag, mac };
  const all = panesOf(layout.root);
  const shownPanes = new Set(stacksOf(layout.root).flatMap((s) => {
    const tabs = s.panes.filter(vis);
    const active = tabs.includes(s.active) ? s.active : tabs[0];
    return active === undefined ? [] : [active];
  }));
  const zone = drag?.target != null && rootRef.current !== null ? zoneRect(rootRef.current, drag.target) : null;

  // Hosts of panes the layout no longer has go.
  useLayoutEffect(() => {
    const keep = new Set(all);
    for (const [p, h] of hosts.current) {
      if (keep.has(p)) continue;
      h.remove();
      hosts.current.delete(p);
      slots.current.delete(p);
    }
  });

  return (
    <div
      ref={rootRef}
      className="workspace relative flex min-h-0 min-w-0 flex-1"
      data-zoomed={zoomStack !== null ? "" : undefined}
      data-dragging={drag !== null ? "" : undefined}
    >
      <NodeView node={layout.root} ctx={ctx} root />
      {drag !== null && (
        <>
          {zone !== null && (
            <div aria-hidden className="drop-zone pointer-events-none absolute z-50 grid place-items-center rounded-(--sheet-radius) border-2 border-primary/70 border-dashed bg-primary/10 text-primary text-xs transition-[left,top,width,height] duration-100" style={zone.style}>
              <span className="material rounded-full px-2.5 py-1 font-medium">{zone.label}</span>
            </div>
          )}
          <div
            aria-hidden
            className="drag-ghost material pointer-events-none fixed z-[60] flex items-center gap-1.5 rounded-full px-2.5 py-1 text-foreground text-xs"
            style={{ left: drag.x + 12, top: drag.y + 10 }}
          >
            {(() => {
              const s = spec(drag.pane);
              const Icon = s.icon;
              return (
                <>
                  <Icon className="size-3.5" /> {s.label}
                </>
              );
            })()}
          </div>
        </>
      )}
      {/* After the tree, so each host is in place before its content mounts. */}
      {all.map((p) => {
        const shown = shownPanes.has(p);
        return createPortal(shown || spec(p).keepAlive ? render(p, shown) : null, hostOf(p), p);
      })}
    </div>
  );
}

// The panes on screen, in reading order, for the keys that move between them.
export function shownPanes(l: Layout, visible: (p: PaneId) => boolean): PaneId[] {
  const zoomStack = l.zoom !== null && visible(l.zoom) ? stackOf(l.root, l.zoom) : null;
  return stacksOf(l.root).flatMap((s) => {
    if (zoomStack !== null && s.id !== zoomStack.id) return [];
    const tabs = s.panes.filter(visible);
    const active = tabs.includes(s.active) ? s.active : tabs[0];
    return active === undefined ? [] : [active];
  });
}
