import type { AgentLine, ChangedFile, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import {
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  FileDiff,
  FileText,
  GitBranch,
  Globe,
  Flag,
  Graph,
  ListChecks,
  type IconType,
  MessageCircleQuestion,
  MonitorPlay,
  Network,
  RefreshCw,
  Undo2,
} from "./icons";
import { type ComponentProps, useCallback, useEffect, useRef, useState } from "react";
import type { DeckBridge } from "../../shared/bridge";
import { addLocalNotice } from "../appState";
import { type DeckClient, serves } from "../client";
import type { Crew } from "../crew";
import { langName, langOf } from "../highlight";
import { cn } from "../lib/cn";
import { focusMessage } from "../palette";
import { usePrefs } from "../prefs";
import { useMentionables } from "../profiles";
import { NO_RACE, toWire } from "../race";
import { panelSlice, shallowEqual, useAppSlice } from "../select";
import { gateMentionables } from "../teamGate";
import { type Panel, WIDE_PX } from "../ui";
import { usePickerSource } from "./AgentPicker";
import { AgentsTree } from "./AgentsTree";
import { BrowserPanel } from "./BrowserPanel";
import { RoomHighlights } from "./HighlightsPanel";
import { PreviewPanel } from "./PreviewPanel";
import { CommentsBadge, DiffToggles, DiffView, SplitAllowed } from "./DiffView";
import { SideChat } from "./SideChat";
import { TasksPanel } from "./TasksPanel";
import { WorkPanel } from "./WorkPanel";
import { actionToast } from "./Toasts";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { PathTail } from "./kit/path";
import { Confirm } from "./kit/dialog";
import { EmptyState } from "./kit/empty";
import { WorkGraph } from "./graph/WorkGraph";

// The side panel's views, each a pane of the room's layout (layout/):
// Changes, Graph (the Work Graph, compact while narrow), Tasks (the queue,
// the board and races), Work (worktrees and capsules, when the Deck serves
// them), Agents (the tree), Side chat and, while a dev server answers,
// Preview.

export const TAB_ICONS: Record<Panel, IconType> = {
  changes: FileDiff,
  graph: Graph,
  tasks: ListChecks,
  highlights: Flag,
  browser: Globe,
  work: GitBranch,
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
          <PathTail path={f.path} className="file-path flex-1 text-xs" />
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

// The views the side panel offers now: Preview while a dev server answers,
// Work, Highlights and Browser where the Deck serves them, and whichever
// one is open regardless.
export function panelAvailable(c: DeckClient, bridge: DeckBridge, ports: number, open: Panel | null): (p: Panel) => boolean {
  return (p) =>
    p === open ||
    (p !== "preview" || ports > 0) &&
      (p !== "work" || serves(c, "Work")) &&
      (p !== "highlights" || serves(c, "Highlights")) &&
      (p !== "browser" || (serves(c, "BrowserState") && bridge.browserView !== undefined));
}


function useWide(): [(el: HTMLDivElement | null) => void, boolean] {
  const [wide, setWide] = useState(false);
  const ref = useCallback((el: HTMLDivElement | null) => {
    if (el === null) return;
    const set = () => setWide(el.getBoundingClientRect().width >= WIDE_PX);
    set();
    const watch = new ResizeObserver(set);
    watch.observe(el);
    return () => watch.disconnect();
  }, []);
  return [ref, wide];
}

// One view of the side panel, as a pane of the room's layout (layout/):
// Changes, Graph, Tasks and the rest. Its stack's tabs and buttons are the
// Workspace's; this is what shows under them.
export function PanelPane({
  c,
  bridge,
  projectKey,
  state,
  panel,
  open,
  files,
  focus,
  crew,
  crewAt,
  branch,
  onRefresh,
  onEditQueued,
  onError,
  onInfo,
  onPanel,
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
  // The room's agents tree (the Shell reads it for the lane strip too).
  crew: Crew | null;
  crewAt: number;
  branch: string | null;
  onRefresh: () => void;
  // Puts a queued message back in the composer (Tasks, Edit).
  onEditQueued: (text: string) => void;
  onError: (t: string) => void;
  onInfo: (t: string) => void;
  // Opens a view; a path to show with it, for Changes.
  onPanel: (p: Panel, path?: string) => void;
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
  // The race builder of the Tasks tab picks as the composer's /race does.
  const picker = usePickerSource(c, state.server, open?.id ?? null);
  const named = gateMentionables(usePrefs().team, useMentionables(c, null));
  // A wide pane shows a diff split and the graph whole.
  const [measure, wide] = useWide();
  return (
    <div ref={measure} className={cn("panel-body flex min-h-0 flex-1 flex-col", panel === "graph" ? "overflow-hidden" : "overflow-y-auto")} data-panel={panel}>
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
        <Changes files={files} focus={focus} wide={wide} onRefresh={onRefresh} revert={revertWith(c, projectKey, open.id, onRefresh)} />
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
          onOpenFile={(path) => onPanel("changes", path)}
          onOpenTasks={() => onPanel("tasks")}
          onOpenWork={() => onPanel("work")}
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
          named={named.profiles}
          picker={picker}
          theme={state.theme}
          branch={branch}
          canJudge={serves(c, "StartRace")}
          onRace={async (d) => {
            if (!serves(c, "StartRace")) {
              onInfo(NO_RACE);
              return false;
            }
            return ((await c.startRace?.(open.id, d.brief, toWire(d), d.judge)) ?? null) !== null;
          }}
          onEditQueued={onEditQueued}
          onError={onError}
          onInfo={onInfo}
        />
      ) : panel === "browser" ? (
        <BrowserPanel key={open.id} c={c} bridge={bridge} sid={open.id} roster={state.roster[open.id] ?? NO_ROSTER} theme={state.theme} onError={onError} />
      ) : panel === "highlights" ? (
        <RoomHighlights key={open.id} c={c} sid={open.id} projectKey={projectKey} approvals={state.approvals.filter((a) => a.session_id === open.id)} onOpenTasks={() => onPanel("tasks")} />
      ) : panel === "work" ? (
        c.project === undefined || !serves(c, "Work") ? (
          <Empty>Worktrees and capsules show for a project on this computer.</Empty>
        ) : (
          <WorkPanel
            key={open.id}
            api={c.project}
            sid={open.id}
            roster={state.roster[open.id] ?? NO_ROSTER}
            kinds={state.server?.kinds ?? []}
            onInfo={onInfo}
            onError={onError}
          />
        )
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
  );
}

// The badge a view's tab carries: Tasks counts the room's queue.
export function PanelBadge({ c, panel, sid }: { c: DeckClient; panel: Panel; sid: string | null }) {
  const queued = useAppSlice(c.store, (s) => (sid === null || panel !== "tasks" ? 0 : (s.queue[sid]?.length ?? 0)));
  return queued > 0 ? <Badge variant="muted">{queued}</Badge> : null;
}

const NO_ROSTER: AgentLine[] = [];
const wholeState = (s: AppState): AppState => s;
// The graph reads the rooms' rosters too.
const graphSlice = (s: AppState) => ({ ...panelSlice(s), roster: s.roster });

// A view takes the whole state but reads only the panel slice (the side
// chat reads messages too, the graph the rosters), so it renders when that
// slice changes.
export function LivePanelPane(props: Omit<ComponentProps<typeof PanelPane>, "state">) {
  useAppSlice(props.c.store, props.panel === "side" ? wholeState : props.panel === "graph" ? graphSlice : panelSlice, shallowEqual);
  return <PanelPane {...props} state={props.c.store.state} />;
}
