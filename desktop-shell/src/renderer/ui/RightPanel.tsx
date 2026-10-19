import type { ChangedFile, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import {
  ChevronDown,
  ChevronRight,
  ChevronsDownUp,
  ChevronsUpDown,
  CornerDownLeft,
  FileDiff,
  FileText,
  ListOrdered,
  type LucideIcon,
  MessageCircleQuestion,
  Maximize2,
  Minimize2,
  RefreshCw,
  ShieldQuestion,
  X,
} from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { groupApprovals } from "../approvals";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import { PANELS, type Panel } from "../ui";
import { ApprovalCard } from "./ApprovalCard";
import { DiffView } from "./DiffView";
import { SideChat } from "./SideChat";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";

const TAB_ICONS: Record<Panel, LucideIcon> = {
  changes: FileDiff,
  approvals: ShieldQuestion,
  queue: ListOrdered,
  side: MessageCircleQuestion,
};
const TAB_LABELS: Record<Panel, string> = { changes: "Diff", approvals: "Approvals", queue: "Queue", side: "Side chat" };

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="empty px-4 py-16 text-center text-muted-foreground/60 text-sm">{children}</p>;
}

function FileSection({
  f,
  open,
  focus,
  onToggle,
}: {
  f: ChangedFile;
  open: boolean;
  focus: boolean;
  onToggle: () => void;
}) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (focus) ref.current?.scrollIntoView({ block: "start" });
  }, [focus]);
  return (
    <div ref={ref} className="file border-border/60 border-b">
      <button
        type="button"
        className="file-head sticky top-0 z-10 flex w-full cursor-pointer items-center gap-2 bg-background px-3 py-2 text-left hover:bg-accent/40"
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
      {open && <DiffView diff={f.diff ?? ""} truncated={f.diff_truncated} numbered />}
    </div>
  );
}

function Changes({
  files,
  focus,
  onRefresh,
}: {
  files: ChangedFile[] | null;
  focus: string | null;
  onRefresh: () => void;
}) {
  // Files the person folded; the rest show their diff, as T3 Code opens them.
  const [closed, setClosed] = useState<Record<string, true>>({});
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
        <div className="ml-auto flex items-center gap-0.5">
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
        {files?.length === 0 && <Empty>No changes yet.</Empty>}
        {files?.map((f) => (
          <FileSection
            key={f.path}
            f={f}
            open={closed[f.path] !== true}
            focus={focus === f.path}
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
    </section>
  );
}

function Approvals({
  c,
  state,
  openId,
  onOpen,
}: {
  c: DeckClient;
  state: AppState;
  openId: string | null;
  onOpen: (id: string) => void;
}) {
  const groups = groupApprovals(state.approvals, state.sessions, openId);
  if (groups.length === 0) return <Empty>No approvals wait for you.</Empty>;
  return (
    <section aria-label="Approvals" className="flex flex-col gap-5 overflow-y-auto p-3">
      {groups.map((g) => (
        <div key={g.sessionId} className="approval-group flex flex-col gap-2">
          <button
            type="button"
            className="group-head flex w-fit cursor-pointer items-center px-1 font-medium text-muted-foreground text-xs hover:text-foreground"
            onClick={() => onOpen(g.sessionId)}
          >
            {g.title}
          </button>
          {g.items.map((a) => (
            <ApprovalCard key={a.id} a={a} onAnswer={(d) => void c.answerApproval(a.session_id, a.id, d)} />
          ))}
        </div>
      ))}
    </section>
  );
}

function Queue({ c, state, sessionId }: { c: DeckClient; state: AppState; sessionId: string }) {
  const items = state.queue[sessionId] ?? [];
  if (items.length === 0) return <Empty>Nothing waits in the queue.</Empty>;
  return (
    <section aria-label="Queue" className="flex flex-col gap-2 overflow-y-auto p-3">
      {items.map((q) => (
        <div key={q.id} className="queued flex flex-col gap-2 rounded-xl border bg-card p-3">
          <div className="text-muted-foreground text-xs">
            Waiting for <span className="approval-agent font-medium text-foreground">{q.agent}</span>
          </div>
          <p className="whitespace-pre-wrap text-sm">{q.text}</p>
          <div className="approval-actions flex justify-end gap-1.5">
            <Button size="xs" variant="ghost-muted" onClick={() => void c.cancelQueued(sessionId, q.id)}>
              Cancel
            </Button>
            <Button size="xs" variant="outline" onClick={() => void c.steer(sessionId, q.id)}>
              <CornerDownLeft />
              Send now
            </Button>
          </div>
        </div>
      ))}
    </section>
  );
}

export function RightPanel({
  c,
  state,
  panel,
  open,
  files,
  focus,
  wide,
  onRefresh,
  onPanel,
  onWide,
  onClose,
  onOpen,
}: {
  c: DeckClient;
  state: AppState;
  panel: Panel;
  open: Session | undefined;
  files: ChangedFile[] | null;
  focus: string | null;
  wide: boolean;
  onRefresh: () => void;
  onPanel: (p: Panel) => void;
  onWide: () => void;
  onClose: () => void;
  onOpen: (id: string) => void;
}) {
  const queued = open === undefined ? 0 : (state.queue[open.id]?.length ?? 0);
  return (
    <aside
      className={cn("panel flex shrink-0 flex-col border-l bg-background", wide ? "w-[min(60vw,900px)]" : "w-[480px]")}
      aria-label="Side panel"
    >
      <header className="panel-head drag flex h-12 shrink-0 items-center gap-1 border-b px-2">
        <h2 className="sr-only">{PANELS.find((p) => p.panel === panel)?.label}</h2>
        <div className="flex items-center gap-0.5" role="tablist" aria-label="Panels">
          {PANELS.map((p) => {
            const Icon = TAB_ICONS[p.panel];
            return (
              <button
                key={p.panel}
                type="button"
                role="tab"
                aria-selected={panel === p.panel}
                className={cn(
                  "flex h-7 cursor-pointer items-center gap-1.5 rounded-md px-2.5 text-sm",
                  panel === p.panel
                    ? "bg-accent font-medium text-foreground"
                    : "text-muted-foreground hover:bg-accent/60 hover:text-foreground",
                )}
                onClick={() => onPanel(p.panel)}
              >
                <Icon className="size-3.5" />
                {TAB_LABELS[p.panel]}
                {p.panel === "approvals" && state.approvals.length > 0 && (
                  <Badge variant="warning">{state.approvals.length}</Badge>
                )}
                {p.panel === "queue" && queued > 0 && <Badge variant="muted">{queued}</Badge>}
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
        {panel === "approvals" && <Approvals c={c} state={state} openId={open?.id ?? null} onOpen={onOpen} />}
        {panel === "changes" &&
          (open === undefined ? (
            <Empty>Open a thread to see its changes.</Empty>
          ) : (
            <Changes files={files} focus={focus} onRefresh={onRefresh} />
          ))}
        {panel === "queue" &&
          (open === undefined ? <Empty>Open a thread to see its queue.</Empty> : <Queue c={c} state={state} sessionId={open.id} />)}
        {panel === "side" &&
          (open === undefined ? (
            <Empty>Open a thread to ask about it.</Empty>
          ) : (
            <SideChat key={open.id} c={c} state={state} main={open} api={c.project} theme={state.theme} />
          ))}
      </div>
    </aside>
  );
}
