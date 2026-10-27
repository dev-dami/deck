import type { AgentLine, ChangedFile, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import {
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  FileDiff,
  FileText,
  Graph,
  ListChecks,
  type IconType,
  MessageCircleQuestion,
  MonitorPlay,
  Network,
  Maximize2,
  Minimize2,
  RefreshCw,
  Undo2,
  X,
} from "./icons";
import { type ComponentProps, useEffect, useRef, useState } from "react";
import type { DeckBridge } from "../../shared/bridge";
import { addLocalNotice } from "../appState";
import { type DeckClient, serves } from "../client";
import type { Crew } from "../crew";
import { langName, langOf } from "../highlight";
import { cn } from "../lib/cn";
import { focusMessage } from "../palette";
import { panelSlice, shallowEqual, useAppSlice } from "../select";
import { PANELS, type Panel } from "../ui";
import { AgentsTree } from "./AgentsTree";
import { PreviewPanel } from "./PreviewPanel";
import { CommentsBadge, DiffToggles, DiffView, SplitAllowed } from "./DiffView";
import { SideChat } from "./SideChat";
import { type RaceStart, TasksPanel } from "./TasksPanel";
import { actionToast } from "./Toasts";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { Confirm } from "./kit/dialog";
import { EmptyState } from "./kit/empty";
import { segmentItem, segmentTrack } from "./kit/segmented";
import { WorkGraph } from "./graph/WorkGraph";
import { useOpenPorts } from "./usePorts";

// The room's tabs: Changes, Graph (the Work Graph, compact), Tasks (the
// queue, the board and races), Agents (the tree), Side chat and, while a
// dev server answers, Preview.

const TAB_ICONS: Record<Panel, IconType> = {
  changes: FileDiff,
  graph: Graph,
  tasks: ListChecks,
  agents: Network,
  side: MessageCircleQuestion,
  preview: MonitorPlay,
};

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="empty text-balance px-8 py-16 text-center text-faint text-sm">{children}</p>;
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
    <div ref={ref} className="file border-hairline border-b last:border-b-0">
      <div className="file-head group/head sticky top-0 z-10 flex h-9 items-center bg-background pr-2 transition-colors hover:bg-[color-mix(in_srgb,var(--hover),var(--background))]">
        <button
          type="button"
          className="flex h-full min-w-0 flex-1 cursor-pointer items-center gap-2 pl-3.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
          aria-expanded={open}
          onClick={onToggle}
        >
          {open ? (
            <ChevronDown className="size-3.5 shrink-0 text-muted-foreground" />
          ) : (
            <ChevronRight className="size-3.5 shrink-0 text-muted-foreground" />
          )}
          <FileText className="size-3.5 shrink-0 text-info-foreground" />
          <span className="file-path min-w-0 flex-1 truncate font-mono text-xs" title={f.path}>
            {f.path}
          </span>
          <FileLang path={f.path} />
          <span className="del font-mono text-diff-deletion-foreground text-xs tabular-nums">−{f.removed}</span>
          <span className="add font-mono text-diff-addition-foreground text-xs tabular-nums">+{f.added}</span>
        </button>
        {onRevert !== null && (
          <Button
            variant="ghost-muted"
            size="xs"
            className="file-revert font-normal opacity-0 focus-visible:opacity-100 group-hover/head:opacity-100"
            aria-label={`Revert ${f.path}`} title="Put the file back" onClick={onRevert}>
            <Undo2 /> Revert
          </Button>
        )}
      </div>
      {open && <DiffView diff={f.diff ?? ""} truncated={f.diff_truncated} numbered path={f.path} />}
    </div>
  );
}

// The language of a changed file, in faint mono.
function FileLang({ path }: { path: string }) {
  const lang = langOf(path);
  return lang === null ? null : <span className="file-lang font-mono text-3xs text-faint">{langName(lang)}</span>;
}

function Changes({
  files,
  focus,
  wide,
  onRefresh,
  revert,
}: {
  files: ChangedFile[] | null;
  focus: string | null;
  // The panel is expanded, so a diff may show split.
  wide: boolean;
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
      <div className="panel-tools flex h-10 shrink-0 items-center gap-2 border-hairline border-b px-3.5 text-xs">
        <span className="font-medium text-foreground">All changes</span>
        {files !== null && files.length > 0 && (
          <span className="font-mono tabular-nums">
            <span className="text-diff-addition-foreground">+{added}</span>{" "}
            <span className="text-diff-deletion-foreground">−{removed}</span>
          </span>
        )}
        <span className="text-faint">{files === null ? "Loading…" : `${files.length} ${files.length === 1 ? "file" : "files"}`}</span>
        <CommentsBadge />
        <div className="ml-auto flex items-center gap-0.5">
          <DiffToggles splitOk={wide} />
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
      <SplitAllowed.Provider value={wide}>
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
      </SplitAllowed.Provider>
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
  lane = null,
  onLane,
  onSteer,
  onStop,
  onOpenThread,
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
  // Opens a tab; a path to show with it, for Changes.
  onPanel: (p: Panel, path?: string) => void;
  onWide: () => void;
  onClose: () => void;
  // The room's lane filter and verbs, for the Graph tab's agent card.
  // Without them it puts "$agent " in the composer to steer, interrupts the
  // agent to stop it, and cannot filter the conversation.
  lane?: string | null;
  onLane?: (lane: string | null) => void;
  onSteer?: (agent: string) => void;
  onStop?: (agent: string) => void;
  // Opens another thread, for the Graph tab's History.
  onOpenThread?: (id: string) => void;
}) {
  const queued = open === undefined ? 0 : (state.queue[open.id]?.length ?? 0);
  const ports = useOpenPorts(c.project);
  const tabs = PANELS.filter((p) => p.panel !== "preview" || panel === "preview" || ports.length > 0);
  // A file the graph opened, until the Shell hands one itself.
  const [asked, setAsked] = useState<string | null>(null);
  useEffect(() => {
    if (panel !== "changes") setAsked(null);
  }, [panel]);
  return (
    <aside
      className={cn(
        // A sheet beside the room. Below 1100px it floats over the room as
        // thick glass (styles.css, Shell), and the room keeps its width.
        "panel sheet flex shrink-0 flex-col overflow-hidden rounded-(--sheet-radius) max-[1100px]:absolute max-[1100px]:inset-y-3 max-[1100px]:right-3 max-[1100px]:z-30 max-[1100px]:max-w-[calc(100%-4rem)]",
        wide ? "w-[min(60vw,900px)] min-w-[480px] shrink" : "w-[480px]",
      )}
      aria-label="Side panel"
      data-yolo-region=""
    >
      <header className="panel-head drag @container/head flex h-12 shrink-0 items-center gap-2 px-2.5">
        <h2 className="sr-only">{PANELS.find((p) => p.panel === panel)?.label}</h2>
        {/* The panel's views as a segmented control. */}
        <div className={cn(segmentTrack(), "min-w-0 overflow-x-auto [scrollbar-width:none]")} role="tablist" aria-label="Room">
          {tabs.map((p) => {
            const Icon = TAB_ICONS[p.panel];
            const on = panel === p.panel;
            return (
              <button
                key={p.panel}
                type="button"
                role="tab"
                aria-selected={on}
                aria-label={p.label}
                title={p.label}
                className={cn(segmentItem({ on }), "px-2.5")}
                onClick={() => onPanel(p.panel)}
              >
                <Icon className="size-3.5" weight={on ? "fill" : "regular"} />
                {/* Five tabs in a narrow panel: only the open one keeps its name. */}
                <span className={on || tabs.length < 5 ? undefined : "hidden @[36rem]/head:inline"}>{p.label}</span>
                {p.panel === "tasks" && queued > 0 && <Badge variant="muted">{queued}</Badge>}
              </button>
            );
          })}
        </div>
        <div className="liquid-clear ml-auto flex shrink-0 items-center gap-0.5 rounded-full p-0.5">
          <Button
            variant="ghost-muted"
            size="icon-xs"
            aria-label={wide ? "Narrow the panel" : "Widen the panel"}
            title={wide ? "Narrow the panel" : "Widen the panel"}
            onClick={onWide}
          >
            {wide ? <Minimize2 /> : <Maximize2 />}
          </Button>
          <Button variant="ghost-muted" size="icon-xs" aria-label="Close the panel" title="Close the panel" onClick={onClose}>
            <X />
          </Button>
        </div>
      </header>
      <div className={cn("panel-body flex min-h-0 flex-1 flex-col", panel === "graph" ? "overflow-hidden" : "overflow-y-auto")}>
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
          <Changes files={files} focus={focus ?? asked} wide={wide} onRefresh={onRefresh} revert={revertWith(c, projectKey, open.id, onRefresh)} />
        ) : panel === "graph" ? (
          <WorkGraph
            key={open.id}
            compact={!wide}
            c={c}
            onOpenMessage={(thread, mid) => {
              // The conversation beside the panel scrolls to it.
              focusMessage(thread, mid);
              if (thread !== open.id) onOpenThread?.(thread);
            }}
            api={c.project}
            sid={open.id}
            roster={state.roster[open.id] ?? NO_ROSTER}
            files={files}
            theme={state.theme}
            lane={lane}
            onLane={onLane ?? (() => {})}
            onOpenFile={(path) => {
              setAsked(path);
              onPanel("changes", path);
            }}
            onOpenTasks={() => onPanel("tasks")}
            onSteer={onSteer ?? ((agent) => onEditQueued(`$${agent} `))}
            onStop={onStop ?? ((agent) => void c.interrupt(open.id, agent))}
          />
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
            <AgentsTree crew={crew} at={crewAt} theme={state.theme} roster={state.roster[open.id]} />
          )
        ) : (
          <SideChat key={open.id} c={c} state={state} main={open} api={c.project} theme={state.theme} />
        )}
      </div>
    </aside>
  );
}

const NO_ROSTER: AgentLine[] = [];
const wholeState = (s: AppState): AppState => s;
// The graph reads the rooms' rosters too.
const graphSlice = (s: AppState) => ({ ...panelSlice(s), roster: s.roster });

// The side panel takes the whole state but reads only the panel slice (the
// side chat reads messages too, the graph the rosters), so it renders when
// that slice changes.
export function LiveRightPanel(props: Omit<ComponentProps<typeof RightPanel>, "state">) {
  useAppSlice(props.c.store, props.panel === "side" ? wholeState : props.panel === "graph" ? graphSlice : panelSlice, shallowEqual);
  return <RightPanel {...props} state={props.c.store.state} />;
}
