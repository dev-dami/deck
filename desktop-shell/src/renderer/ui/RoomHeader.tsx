import type { AgentLine, ChangedFile, Session, Theme } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { kindMark } from "@web/theme";
import { ArrowDownToLine, FileDiff, GitBranch, PanelLeft, PanelRight, SquareTerminal, Trash2, Undo2 } from "lucide-react";
import { useState } from "react";
import { agentColour } from "../agents";
import { type DeckClient, serves } from "../client";
import type { Team } from "../crew";
import { shortId } from "../lanes";
import type { ProjectApi } from "../project";
import { type Git, GitActions } from "./Git";
import { Button } from "./kit/button";
import { Confirm } from "./kit/dialog";
import { MenuItem, Popover } from "./kit/menu";
import { PortChip } from "./PreviewPanel";
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
            className="room-branch flex h-6 min-w-0 max-w-48 shrink cursor-pointer items-center gap-1 rounded-md px-1.5 font-mono text-2xs text-faint outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
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

// The room header, one row: the room's corner index and title, the git
// actions, who follows the room, and the side panel's toggles.
export function RoomHeader({
  c,
  branch,
  onChanged,
  sidebar,
  onShowSidebar,
  project,
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
}: {
  c: DeckClient;
  // The room's own branch when it is isolated, else "".
  branch: string;
  // The room's changed files may have changed (apply, discard).
  onChanged: () => void;
  sidebar: boolean;
  onShowSidebar: () => void;
  project: string;
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
}) {
  const [renaming, setRenaming] = useState(false);
  const ports = useOpenPorts(api);
  const first = roster[0];
  const added = files?.reduce((n, f) => n + f.added, 0) ?? 0;
  const removed = files?.reduce((n, f) => n + f.removed, 0) ?? 0;
  const title = open === undefined ? (loaded ? "New thread" : "Connecting…") : sessionTitle(open);
  return (
    <header className="topbar drag @container/top flex h-13 shrink-0 items-center gap-2 px-4" data-yolo-region="">
      {!sidebar && (
        <Button variant="ghost-muted" size="icon-sm" aria-label="Show the sidebar" title="Show the sidebar" onClick={onShowSidebar}>
          <PanelLeft />
        </Button>
      )}
      {/* A Mac toolbar's title: the room on top, its project under it. */}
      <div className="flex min-w-28 flex-1 flex-col justify-center gap-px">
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
              className="h-5 min-w-0 flex-1 rounded-[5px] border border-input bg-card px-1 font-semibold text-sm outline-none focus:border-primary/50 focus:ring-[3px] focus:ring-ring"
            />
          ) : (
            <h1
              className="title min-w-0 truncate font-semibold text-foreground"
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
        <div className="flex h-3.5 min-w-0 items-center gap-1.5 text-2xs text-muted-foreground">
          {open !== undefined && (
            // The corner index: the first agent's glyph and the room's short id, in that agent's hue.
            <span
              className="room-index inline-flex shrink-0 items-center gap-1 font-mono"
              style={{ color: first === undefined ? "var(--text-faint)" : agentColour(first.id, first.kind) }}
              title={open.id}
            >
              <span aria-hidden>{first === undefined ? "●" : kindMark(theme, first.kind) || "●"}</span>
              <span>{shortId(open.id)}</span>
            </span>
          )}
          <span className="project min-w-0 truncate" title={project || "Deck"}>
            {project || "Deck"}
          </span>
        </div>
      </div>
      {api !== undefined && <GitActions git={git} project={api} threadTitle={open === undefined ? "" : sessionTitle(open)} onError={onError} onInfo={onInfo} />}
      {api !== undefined && open !== undefined && <ShareStack team={team} shares={shares} onSet={onShare} onError={onError} ask={shareAsk} />}
      {/* Toolbar glyphs: borderless, with a bezel on hover and while on. */}
      <div className="panel-toggles flex shrink-0 items-center gap-0.5">
        {api !== undefined && (
          <Button variant="ghost-muted" size="icon-sm" aria-label="Terminal" title={`Terminal (${terminalKey})`} aria-pressed={terminal} onClick={onTerminal}>
            <SquareTerminal />
          </Button>
        )}
        <Button variant="ghost-muted" size="sm" className="font-normal" aria-pressed={diff} onClick={onDiff}>
          <FileDiff />
          <span className="sr-only @3xl/top:not-sr-only">Diff</span>
          {files !== null && files.length > 0 && (
            <span className="font-mono text-xs">
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
          <PanelRight />
        </Button>
      </div>
    </header>
  );
}
