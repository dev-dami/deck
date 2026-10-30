import type { AgentLine, ChangedFile, Session, Theme } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { kindMark } from "@web/theme";
import {
  ArrowDownToLine,
  ChatCircle,
  FileDiff,
  GitBranch,
  Graph,
  type IconType,
  PanelLeft,
  PanelRight,
  SquareTerminal,
  Trash2,
  Undo2, CornerDownRight } from "./icons";
import { type ReactNode, useMemo, useState } from "react";
import { agentColour } from "../agents";
import { tintOf } from "../teamGate";
import { type DeckClient, serves } from "../client";
import type { Team } from "../crew";
import { shortId } from "../lanes";
import { cn } from "../lib/cn";
import type { ProjectApi } from "../project";
import { type Git, GitActions } from "./Git";
import { isMac } from "./keys";
import { Button } from "./kit/button";
import { Confirm } from "./kit/dialog";
import { MenuItem, Popover } from "./kit/menu";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { PortChip } from "./PreviewPanel";
import { ROOM_VIEWS, type RoomView } from "./roomView";
import { ShareStack } from "./Team";
import { actionToast } from "./Toasts";
import { useOpenPorts } from "./usePorts";

const count = (n: number, one: string) => `${n} ${n === 1 ? one : `${one}s`}`;

// How long the apply toast offers Undo.
const APPLY_UNDO_MS = 10_000;

// The rooms whose last apply in this window can still be undone, by client.
const applied = new WeakMap<DeckClient, Set<string>>();
const appliedOf = (c: DeckClient): Set<string> => {
  let s = applied.get(c);
  if (s === undefined) applied.set(c, (s = new Set()));
  return s;
};

// The room's branch beside its title, and what can be done with it: apply
// its changes to the shared tree or discard it when the room is isolated,
// or move the room onto a branch of its own. Each item needs its call.
function BranchMenu({
  c,
  open,
  branch,
  files,
  onChanged,
  onInfo,
}: {
  c: DeckClient;
  open: Session;
  branch: string;
  files: ChangedFile[] | null;
  onChanged: () => void;
  onInfo: (t: string) => void;
}) {
  const [menu, setMenu] = useState(false);
  const [discard, setDiscard] = useState(false);
  const isolated = branch !== "";
  const items = isolated ? ["ApplyIsolated", "UnisolateSession"] : ["IsolateSession"];
  const undoable = isolated && serves(c, "UndoApply") && appliedOf(c).has(open.id);
  if (!items.some((m) => serves(c, m))) return null;
  const run = (f: () => Promise<void>) => () => {
    setMenu(false);
    void f().then(onChanged);
  };
  const changed = files?.length ?? 0;
  const undoApply = async () => {
    const back = await c.undoApply?.(open.id);
    if (back === null || back === undefined) return;
    appliedOf(c).delete(open.id);
    onInfo(`Undid the apply: ${count(back.length, "file")} as before.`);
  };
  return (
    <>
      <Popover
        open={menu}
        onClose={() => setMenu(false)}
        label="Branch"
        className="w-60"
        trigger={
          <button
            type="button"
            className="room-branch flex h-5 min-w-0 max-w-48 shrink cursor-pointer items-center gap-1 rounded-full px-1.5 font-mono text-2xs text-faint outline-none transition-colors hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected"
            aria-label={isolated ? `Branch ${branch}` : "Branch"}
            aria-expanded={menu}
            title={isolated ? branch : "This thread works in the shared tree"}
            onClick={() => setMenu((m) => !m)}
          >
            <GitBranch className="size-3 shrink-0" />
            {isolated && <span className="truncate">{branch}</span>}
          </button>
        }
      >
        {isolated ? (
          <>
            <MenuItem
              disabled={!serves(c, "ApplyIsolated")}
              onClick={run(async () => {
                const files = await c.applyIsolated?.(open.id);
                if (files === null || files === undefined) return;
                if (files.length === 0) return onInfo("Nothing to apply.");
                const text = `Applied ${count(files.length, "file")} to your tree: ${files.join(", ")}`;
                if (!serves(c, "UndoApply")) return onInfo(text);
                appliedOf(c).add(open.id);
                actionToast(c.store, text, { label: "Undo", ms: APPLY_UNDO_MS, run: () => void undoApply().then(onChanged) });
              })}
            >
              <ArrowDownToLine /> Apply to my tree
            </MenuItem>
            {undoable && (
              <MenuItem onClick={run(undoApply)}>
                <Undo2 /> Undo last apply
              </MenuItem>
            )}
            <MenuItem
              className="text-destructive-foreground"
              disabled={!serves(c, "UnisolateSession")}
              onClick={() => {
                setMenu(false);
                setDiscard(true);
              }}
            >
              <Trash2 /> Discard branch
            </MenuItem>
          </>
        ) : (
          <MenuItem
            disabled={open.busy}
            title={open.busy ? "Stop the agents first" : undefined}
            onClick={run(async () => {
              if (await c.isolateSession?.(open.id)) onInfo("The thread works on a branch of its own now.");
            })}
          >
            <GitBranch /> Move to its own branch
          </MenuItem>
        )}
      </Popover>
      {discard && (
        <Confirm
          title={`Discard ${branch}?`}
          description={`Deck removes the branch ${branch} and its worktree, with ${count(changed, "changed file")} you did not apply. The thread goes back to the shared tree.`}
          action="Discard branch"
          onClose={() => setDiscard(false)}
          onConfirm={() => {
            void c.unisolate?.(open.id).then((ok) => {
              if (ok) {
                appliedOf(c).delete(open.id);
                onInfo(`Discarded ${branch}.`);
              }
              onChanged();
            });
          }}
        />
      )}
    </>
  );
}

// A capsule of clear glass that groups the toolbar's trailing controls.
// The glass is a layer behind them, not their parent: a backdrop filter
// holds fixed descendants (the commit dialog) and nested glass (the menus)
// to the element that carries it. The controls are positioned, so they
// paint over that layer.
function Capsule({ className, children }: { className?: string; children: ReactNode }) {
  return (
    <div className={cn("relative flex shrink-0 items-center gap-0.5 rounded-full p-0.5", className)}>
      <span aria-hidden className="liquid-clear absolute inset-0 rounded-full" />
      {children}
    </div>
  );
}

const VIEW_ICONS: Record<RoomView, IconType> = { conversation: ChatCircle, graph: Graph };

// Conversation or Graph, centred in the toolbar. The arrows move between
// the two, as in any tab list.
function ViewSwitcher({ view, onView }: { view: RoomView; onView: (v: RoomView) => void }) {
  return (
    <div
      // A narrow room (beside wide panes) gives its toolbar the middle;
      // ⌘/Ctrl+Shift+G still turns the view.
      className={cn(segmentTrack(), "room-views hidden @[32rem]/top:inline-flex")}
      role="tablist"
      aria-label="View"
      onKeyDown={(e) => {
        if (e.key !== "ArrowLeft" && e.key !== "ArrowRight") return;
        e.preventDefault();
        const i = ROOM_VIEWS.findIndex((v) => v.view === view);
        const next = ROOM_VIEWS[(i + (e.key === "ArrowRight" ? 1 : ROOM_VIEWS.length - 1)) % ROOM_VIEWS.length];
        if (next === undefined) return;
        onView(next.view);
        e.currentTarget.querySelector<HTMLElement>(`[data-view="${next.view}"]`)?.focus();
      }}
    >
      {ROOM_VIEWS.map((v) => {
        const Icon = VIEW_ICONS[v.view];
        const on = v.view === view;
        return (
          <button
            key={v.view}
            type="button"
            role="tab"
            data-view={v.view}
            aria-selected={on}
            aria-label={v.label}
            title={v.label}
            tabIndex={on ? 0 : -1}
            className={segmentItem({ on })}
            onClick={() => onView(v.view)}
          >
            <Icon weight={on ? "fill" : "regular"} className="size-3.5" />
            <span className="sr-only @2xl/top:not-sr-only">{v.label}</span>
          </button>
        );
      })}
    </div>
  );
}

// The room's toolbar, 48px on the sheet with no material of its own: the
// title over its project at the leading edge, the view switcher in the
// centre, and the controls at the trailing edge in up to three capsules of
// clear glass (git, sharing, the panels). The centre column stays centred
// while both sides fit, and gives way to them when they do not.
export function RoomHeader({
  c,
  branch,
  onChanged,
  sidebar,
  onShowSidebar,
  project,
  parent = null,
  onOpenParent,
  open,
  loaded,
  roster,
  theme,
  onRename,
  api,
  git,
  team,
  shares,
  onShare,
  shareAsk,
  files,
  terminal,
  terminalKey,
  onTerminal,
  diff,
  panelOpen,
  onDiff,
  onPanel,
  onError,
  onInfo,
  onPreview,
  layout,
  view,
  onView,
}: {
  c: DeckClient;
  // The room's own branch when it is isolated, else "".
  branch: string;
  // The room's changed files may have changed (apply, discard).
  onChanged: () => void;
  sidebar: boolean;
  onShowSidebar: () => void;
  project: string;
  // The thread this sub-thread was opened from, and how to go there.
  parent?: { id: string; title: string } | null;
  onOpenParent?: (id: string) => void;
  open: Session | undefined;
  // The thread list arrived: without a thread, the room is a new one.
  loaded: boolean;
  roster: AgentLine[];
  theme: Theme | null;
  onRename: (title: string) => void;
  api: ProjectApi | undefined;
  git: Git;
  team: Team | string | null;
  shares: Record<string, string>;
  onShare: (peer: string, role: string) => Promise<string | null>;
  // Opens the share popover each time it changes (/share).
  shareAsk: number;
  files: ChangedFile[] | null;
  terminal: boolean;
  terminalKey: string;
  onTerminal: () => void;
  // The Changes tab shows.
  diff: boolean;
  panelOpen: boolean;
  onDiff: () => void;
  onPanel: () => void;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
  // Opens a dev server's URL in the Preview tab.
  onPreview: (url: string) => void;
  // The Layout menu (layout/LayoutMenu.tsx), last in the panels' capsule.
  layout?: ReactNode;
  // How the room shows its thread, and how to switch it. Without them the
  // switcher does not show.
  view?: RoomView;
  onView?: (v: RoomView) => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const ports = useOpenPorts(api);
  // On a Mac the traffic lights sit over the room when the sidebar is
  // hidden: the toolbar clears them and centres its items on them, as a
  // unified title bar does (they centre 17px below the sheet's top edge).
  const mac = useMemo(isMac, []);
  const first = roster[0];
  const added = files?.reduce((n, f) => n + f.added, 0) ?? 0;
  const removed = files?.reduce((n, f) => n + f.removed, 0) ?? 0;
  const title = open === undefined ? (loaded ? "New thread" : "Connecting…") : sessionTitle(open);
  return (
    <header
      className={cn(
        "topbar drag @container/top grid h-12 shrink-0 grid-cols-[1fr_auto_1fr] items-center gap-3 pr-2.5 pl-4",
        !sidebar && (mac ? "pb-3.5 pl-[4.75rem]" : "pl-2.5"),
      )}
      data-yolo-region=""
    >
      <div className="flex min-w-28 items-center gap-2">
        {!sidebar && (
          <Button variant="ghost-muted" size="icon-sm" aria-label="Show the sidebar" title="Show the sidebar" onClick={onShowSidebar}>
            <PanelLeft />
          </Button>
        )}
        {/* A Mac toolbar's title: the room on top, its project under it. */}
        <div className="flex min-w-0 flex-1 flex-col justify-center">
          <div className="flex h-5 min-w-0 items-center gap-1.5 text-sm">
            {renaming && open !== undefined ? (
              <input
                autoFocus
                defaultValue={open.title}
                aria-label="Thread title"
                onFocus={(e) => e.currentTarget.select()}
                onKeyDown={(e) => {
                  if (e.key === "Enter") {
                    const t = e.currentTarget.value.trim();
                    if (t !== "") onRename(t);
                    setRenaming(false);
                  } else if (e.key === "Escape") setRenaming(false);
                }}
                onBlur={() => setRenaming(false)}
                className="h-5 min-w-0 flex-1 rounded-md border border-input bg-card px-1 font-semibold text-sm outline-none focus:border-primary/50 focus:ring-[3px] focus:ring-ring"
              />
            ) : (
              <h1
                className="title min-w-0 truncate font-semibold text-foreground tracking-tight"
                title={open === undefined ? undefined : "Double-click to rename"}
                onDoubleClick={() => open !== undefined && setRenaming(true)}
              >
                {title}
              </h1>
            )}
            {open !== undefined && (
              <BranchMenu c={c} open={open} branch={branch} files={files} onChanged={onChanged} onInfo={onInfo} />
            )}
            {ports.length > 0 && (
              <span className="room-ports flex shrink-0 items-center gap-1" role="group" aria-label="Dev servers">
                {ports.map((p) => (
                  <PortChip key={p.port} p={p} onPreview={onPreview} />
                ))}
              </span>
            )}
          </div>
          <div className="flex h-4 min-w-0 items-center gap-1.5 text-2xs text-muted-foreground">
            {open !== undefined && (
              // The corner index: the first agent's glyph and the room's short id, in that agent's hue.
              <span
                className="room-index inline-flex shrink-0 items-center gap-1 font-mono"
                style={{ color: first === undefined ? "var(--text-faint)" : agentColour(first.id, first.kind, tintOf(first.color)) }}
                title={open.id}
              >
                <span aria-hidden>{first === undefined ? "●" : kindMark(theme, first.kind) || "●"}</span>
                <span>{shortId(open.id)}</span>
              </span>
            )}
            {open !== undefined && <span aria-hidden className="text-faint">·</span>}
            {parent !== null ? (
              <button
                type="button"
                className="room-parent inline-flex min-w-0 cursor-pointer items-center gap-1 truncate rounded-sm text-muted-foreground outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                title={`Open ${parent.title}`}
                onClick={() => onOpenParent?.(parent.id)}
              >
                <CornerDownRight aria-hidden className="size-3 shrink-0 rotate-180" />
                <span className="truncate">Sub-thread of {parent.title}</span>
              </button>
            ) : (
              <span className="project min-w-0 truncate" title={project || "Deck"}>
                {project || "Deck"}
              </span>
            )}
          </div>
        </div>
      </div>
      {view !== undefined && onView !== undefined ? <ViewSwitcher view={view} onView={onView} /> : <span />}
      <div className="flex items-center justify-end gap-1.5">
        {api !== undefined && git.status?.repo === true && (
          <Capsule>
            <GitActions git={git} project={api} threadTitle={open === undefined ? "" : sessionTitle(open)} onError={onError} onInfo={onInfo} />
          </Capsule>
        )}
        {/* ShareStack shows with a teammate to share with. */}
        {api !== undefined && open !== undefined && typeof team === "object" && team?.available === true && team.teammates.length > 0 && (
          <Capsule>
            <ShareStack team={team} shares={shares} onSet={onShare} onError={onError} ask={shareAsk} />
          </Capsule>
        )}
        {/* The panels: borderless glyphs in one capsule, a bezel while on. */}
        <Capsule className="panel-toggles">
          {api !== undefined && (
            <Button variant="ghost-muted" size="icon-sm" aria-label="Terminal" title={`Terminal (${terminalKey})`} aria-pressed={terminal} onClick={onTerminal}>
              <SquareTerminal weight={terminal ? "fill" : "regular"} />
            </Button>
          )}
          <Button variant="ghost-muted" size="sm" className="gap-1.5 px-2 font-normal" aria-pressed={diff} onClick={onDiff}>
            <FileDiff weight={diff ? "fill" : "regular"} />
            <span className="sr-only @3xl/top:not-sr-only">Diff</span>
            {files !== null && files.length > 0 && (
              <span className="font-mono text-2xs tabular-nums">
                <span className="text-diff-addition-foreground">+{added}</span> <span className="text-diff-deletion-foreground">−{removed}</span>
              </span>
            )}
          </Button>
          <Button
            variant="ghost-muted"
            size="icon-sm"
            aria-label={panelOpen ? "Hide the side panel" : "Show the side panel"}
            title={panelOpen ? "Hide the side panel" : "Show the side panel"}
            aria-pressed={panelOpen}
            onClick={onPanel}
          >
            <PanelRight weight={panelOpen ? "fill" : "regular"} />
          </Button>
          {layout}
        </Capsule>
      </div>
    </header>
  );
}
