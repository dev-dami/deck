import type { ChangedFile, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import {
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  FileDiff,
  FileText,
  ListChecks,
  type LucideIcon,
  MessageCircleQuestion,
  MonitorPlay,
  Network,
  Maximize2,
  Minimize2,
  RefreshCw,
  Undo2,
  X,
} from "lucide-react";
import { type ComponentProps, useEffect, useRef, useState } from "react";
import type { DeckBridge } from "../../shared/bridge";
import { addLocalNotice } from "../appState";
import { type DeckClient, serves } from "../client";
import type { Crew } from "../crew";
import { cn } from "../lib/cn";
import { panelSlice, shallowEqual, useAppSlice } from "../select";
import { PANELS, type Panel } from "../ui";
import { AgentsTree } from "./AgentsTree";
import { PreviewPanel } from "./PreviewPanel";
import { CommentsBadge, DiffView } from "./DiffView";
import { SideChat } from "./SideChat";
import { type RaceStart, TasksPanel } from "./TasksPanel";
import { actionToast } from "./Toasts";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { Confirm } from "./kit/dialog";
import { EmptyState } from "./kit/empty";
import { useOpenPorts } from "./usePorts";

// The room's tabs: Changes, Tasks (the queue, the board and races), Agents
// (the tree), Side chat and, while a dev server answers, Preview.

const TAB_ICONS: Record<Panel, LucideIcon> = {
  changes: FileDiff,
  tasks: ListChecks,
  agents: Network,
  side: MessageCircleQuestion,
  preview: MonitorPlay,
};

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="empty px-4 py-16 text-center text-muted-foreground/60 text-sm">{children}</p>;
}

// How long the Undo of a revert stays.
const REVERT_UNDO_MS = 10_000;

const fileCount = (n: number) => `${n} ${n === 1 ? "file" : "files"}`;

// Puts files back as they were before the room changed them (the base its
// Changes show), then says so in the room and offers Undo. Null when the
// Deck cannot revert.
function revertWith(c: DeckClient, projectKey: string, sid: string, onRefresh: () => void): ((paths: string[]) => void) | null {
  if (!serves(c, "RevertFiles")) return null;
  return (paths) =>
    void c.revertFiles?.(sid, paths).then((done) => {
      if (done === null || done === undefined) return;
      onRefresh();
      addLocalNotice(projectKey, sid, `You reverted ${done.length === 1 ? done[0] : `${fileCount(done.length)}: ${done.join(", ")}`}`);
      const text = `Reverted ${fileCount(done.length)}.`;
      if (!serves(c, "UndoRevert")) {
        c.store.dispatch({ type: "toast.add", kind: "info", text });
        return;
      }
      actionToast(c.store, text, { label: "Undo", ms: REVERT_UNDO_MS, run: () => void c.undoRevert?.(sid).then(onRefresh) });
    });
}

function FileSection({
  f,
  open,
  focus,
  onToggle,
  onRevert,
}: {
  f: ChangedFile;
  open: boolean;
  focus: boolean;
  onToggle: () => void;
  // Asks first, then reverts the file; null when the Deck cannot.
  onRevert: (() => void) | null;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (focus) ref.current?.scrollIntoView({ block: "start" });
  }, [focus]);
  return (
    <div ref={ref} className="file border-border/60 border-b">
      <div className="file-head sticky top-0 z-10 flex items-center bg-background pr-2 hover:bg-accent/40">
        <button
          type="button"
          className="flex min-w-0 flex-1 cursor-pointer items-center gap-2 px-3 py-2 text-left"
          aria-expanded={open}
          onClick={onToggle}
        >
          {open ? (
            <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
          )}
          <FileText className="size-3.5 shrink-0 text-info-foreground" />
          <span className="file-path min-w-0 flex-1 truncate font-mono text-xs">{f.path}</span>
          <span className="del font-mono text-diff-deletion-foreground text-xs">−{f.removed}</span>
          <span className="add font-mono text-diff-addition-foreground text-xs">+{f.added}</span>
        </button>
        {onRevert !== null && (
          <Button variant="ghost-muted" size="xs" className="file-revert font-normal" aria-label={`Revert ${f.path}`} title="Put the file back" onClick={onRevert}>
            <Undo2 /> Revert
          </Button>
        )}
      </div>
      {open && <DiffView diff={f.diff ?? ""} truncated={f.diff_truncated} numbered path={f.path} />}
    </div>
  );
}

function Changes({
  files,
  focus,
  onRefresh,
  revert,
}: {
  files: ChangedFile[] | null;
  focus: string | null;
  onRefresh: () => void;
  revert: ((paths: string[]) => void) | null;
}) {
  // Files the person folded; the rest show their diff, as T3 Code opens them.
  const [closed, setClosed] = useState<Record<string, true>>({});
  // The paths a revert waits to confirm.
  const [asking, setAsking] = useState<string[] | null>(null);
  const added = files?.reduce((n, f) => n + f.added, 0) ?? 0;
  const removed = files?.reduce((n, f) => n + f.removed, 0) ?? 0;
  const allClosed = files !== null && files.length > 0 && files.every((f) => closed[f.path] === true);
  return (
    <section aria-label="Changes" className="flex min-h-0 flex-1 flex-col">
      <div className="panel-tools flex h-11 shrink-0 items-center gap-2 border-b px-3 text-xs">
        <span className="font-medium text-foreground">All changes</span>
        {files !== null && files.length > 0 && (
          <span className="font-mono">
            <span className="text-diff-addition-foreground">+{added}</span>{" "}
            <span className="text-diff-deletion-foreground">−{removed}</span>
          </span>
        )}
        <span className="text-muted-foreground">
          {files === null ? "Loading…" : `${files.length} ${files.length === 1 ? "file" : "files"}`}
        </span>
        <CommentsBadge />
        <div className="ml-auto flex items-center gap-0.5">
          {revert !== null && files !== null && files.length > 0 && (
            <Button variant="ghost-muted" size="xs" className="revert-all font-normal" onClick={() => setAsking(files.map((f) => f.path))}>
              <Undo2 /> Revert all
            </Button>
          )}
          <Button
            variant="ghost-muted"
            size="icon-xs"
            aria-label={allClosed ? "Expand all files" : "Collapse all files"}
            title={allClosed ? "Expand all files" : "Collapse all files"}
            onClick={() =>
              setClosed(allClosed || files === null ? {} : Object.fromEntries(files.map((f) => [f.path, true as const])))
            }
          >
            {allClosed ? <ChevronsUpDown /> : <ChevronsDownUp />}
          </Button>
          <Button variant="ghost-muted" size="icon-xs" aria-label="Refresh" title="Refresh" onClick={onRefresh}>
            <RefreshCw />
          </Button>
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {files?.length === 0 && (
          <EmptyState icon={FileDiff} title="No changes yet." hint="Files an agent edits show here with their diff." className="py-16" />
        )}
        {files?.map((f) => (
          <FileSection
            key={f.path}
            f={f}
            open={closed[f.path] !== true}
            focus={focus === f.path}
            onRevert={revert === null ? null : () => setAsking([f.path])}
            onToggle={() =>
              setClosed((c) => {
                if (c[f.path] === true) {
                  const { [f.path]: _gone, ...rest } = c;
                  return rest;
                }
                return { ...c, [f.path]: true };
              })
            }
          />
        ))}
      </div>
      {asking !== null && revert !== null && (
        <Confirm
          title={asking.length === 1 ? `Revert ${asking[0]}?` : `Revert all ${asking.length} files?`}
          description={
            asking.length === 1
              ? "The file goes back to how it was before this thread changed it. Undo stays for 10 seconds."
              : `Every file this thread changed goes back to how it was before: ${asking.join(", ")}. Undo stays for 10 seconds.`
          }
          action={asking.length === 1 ? "Revert" : "Revert all"}
          onClose={() => setAsking(null)}
          onConfirm={() => revert(asking)}
        />
      )}
    </section>
  );
}

export function RightPanel({
  c,
  bridge,
  projectKey,
  state,
  panel,
  open,
  files,
  focus,
  wide,
  crew,
  crewAt,
  branch,
  onRefresh,
  onEditQueued,
  onError,
  onInfo,
  onPanel,
  onWide,
  onClose,
}: {
  c: DeckClient;
  bridge: DeckBridge;
  // The project's key in the app state, for the room's own notes.
  projectKey: string;
  state: AppState;
  panel: Panel;
  open: Session | undefined;
  files: ChangedFile[] | null;
  focus: string | null;
  wide: boolean;
  // The room's agents tree (the Shell reads it for the lane strip too).
  crew: Crew | null;
  crewAt: number;
  branch: string | null;
  onRefresh: () => void;
  // Puts a queued message back in the composer (Tasks, Edit).
  onEditQueued: (text: string) => void;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
  onPanel: (p: Panel) => void;
  onWide: () => void;
  onClose: () => void;
}) {
  const queued = open === undefined ? 0 : (state.queue[open.id]?.length ?? 0);
  const ports = useOpenPorts(c.project);
  const tabs = PANELS.filter((p) => p.panel !== "preview" || panel === "preview" || ports.length > 0);
  return (
    <aside
      className={cn(
        // Below 1100px the panel floats over the room, which keeps its width.
        "panel flex shrink-0 flex-col border-l bg-background max-[1100px]:absolute max-[1100px]:inset-y-0 max-[1100px]:right-0 max-[1100px]:z-30 max-[1100px]:max-w-[calc(100%-3rem)] max-[1100px]:shadow-popover",
        wide ? "w-[min(60vw,900px)]" : "w-[480px]",
      )}
      aria-label="Side panel"
    >
      <header className="panel-head drag flex h-12 shrink-0 items-center gap-1 border-b px-2">
        <h2 className="sr-only">{PANELS.find((p) => p.panel === panel)?.label}</h2>
        <div className="flex min-w-0 items-center gap-0.5 overflow-x-auto" role="tablist" aria-label="Room">
          {tabs.map((p) => {
            const Icon = TAB_ICONS[p.panel];
            return (
              <button
                key={p.panel}
                type="button"
                role="tab"
                aria-selected={panel === p.panel}
                className={cn(
                  "flex h-7 shrink-0 cursor-pointer items-center gap-1.5 whitespace-nowrap rounded-md px-2 text-[13px]",
                  panel === p.panel
                    ? "bg-accent font-medium text-foreground"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
                onClick={() => onPanel(p.panel)}
              >
                <Icon className="size-3.5" />
                {p.label}
                {p.panel === "tasks" && queued > 0 && <Badge variant="muted">{queued}</Badge>}
              </button>
            );
          })}
        </div>
        <div className="ml-auto flex items-center gap-0.5">
          <Button
            variant="ghost-muted"
            size="icon-sm"
            aria-label={wide ? "Narrow the panel" : "Widen the panel"}
            title={wide ? "Narrow the panel" : "Widen the panel"}
            onClick={onWide}
          >
            {wide ? <Minimize2 /> : <Maximize2 />}
          </Button>
          <Button variant="ghost-muted" size="icon-sm" aria-label="Close the panel" title="Close the panel" onClick={onClose}>
            <X />
          </Button>
        </div>
      </header>
      <div className="panel-body flex min-h-0 flex-1 flex-col overflow-y-auto">
        {panel === "preview" ? (
          // A dev server belongs to the project, not to one thread.
          c.project === undefined ? (
            <Empty>The preview shows for a project on this computer.</Empty>
          ) : (
            <PreviewPanel key={open?.id ?? ""} api={c.project} bridge={bridge} />
          )
        ) : open === undefined ? (
          <Empty>Open a thread to see its changes, tasks and agents.</Empty>
        ) : panel === "changes" ? (
          <Changes files={files} focus={focus} onRefresh={onRefresh} revert={revertWith(c, projectKey, open.id, onRefresh)} />
        ) : panel === "tasks" ? (
          <TasksPanel
            key={open.id}
            c={c}
            api={c.project}
            sessionId={open.id}
            busy={open.busy}
            queue={state.queue[open.id] ?? []}
            kinds={state.server?.kinds ?? []}
            theme={state.theme}
            branch={branch}
            canJudge={serves(c, "StartRace")}
            onRace={async (r: RaceStart) =>
              serves(c, "StartRace") ? ((await c.startRace?.(open.id, r.brief, r.kind, r.count, r.judge)) ?? null) !== null : c.send(open.id, r.text, [])
            }
            onEditQueued={onEditQueued}
            onError={onError}
            onInfo={onInfo}
          />
        ) : panel === "agents" ? (
          c.project === undefined ? (
            <Empty>The agents tree shows for a project on this computer.</Empty>
          ) : (
            <AgentsTree crew={crew} at={crewAt} theme={state.theme} />
          )
        ) : (
          <SideChat key={open.id} c={c} state={state} main={open} api={c.project} theme={state.theme} />
        )}
      </div>
    </aside>
  );
}

const wholeState = (s: AppState): AppState => s;

// The side panel takes the whole state but reads only the panel slice (the
// side chat reads messages too), so it renders when that slice changes.
export function LiveRightPanel(props: Omit<ComponentProps<typeof RightPanel>, "state">) {
  useAppSlice(props.c.store, props.panel === "side" ? wholeState : panelSlice, shallowEqual);
  return <RightPanel {...props} state={props.c.store.state} />;
}
