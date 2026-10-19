import type { ChangedFile, Session } from "@web/protocol";
import type { AppState } from "@web/state";
import { ChevronRight, CornerDownLeft, RefreshCw, X } from "lucide-react";
import { useEffect, useState } from "react";
import { groupApprovals } from "../approvals";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import { PANELS, type Panel } from "../ui";
import { ApprovalCard } from "./ApprovalCard";
import { DiffView } from "./DiffView";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";

function Empty({ children }: { children: React.ReactNode }) {
  return <p className="empty px-2 py-10 text-center text-muted-foreground text-sm">{children}</p>;
}

// "pkg/remote/auth/limit.go" → the name, then the folder in muted text.
function PathLabel({ path }: { path: string }) {
  const cut = path.lastIndexOf("/");
  return (
    <span className="file-path flex min-w-0 flex-1 items-baseline gap-1.5 truncate font-mono text-xs">
      <span className="text-foreground">{cut < 0 ? path : path.slice(cut + 1)}</span>
      {cut > 0 && <span className="truncate text-muted-foreground text-2xs">{path.slice(0, cut)}</span>}
    </span>
  );
}

function Changes({ c, sessionId }: { c: DeckClient; sessionId: string }) {
  const [files, setFiles] = useState<ChangedFile[] | null>(null);
  const [open, setOpen] = useState<string | null>(null);
  const [tick, setTick] = useState(0);
  useEffect(() => {
    let live = true;
    setFiles(null);
    void c.changes(sessionId).then((f) => {
      if (live) setFiles(f ?? []);
    });
    return () => {
      live = false;
    };
  }, [c, sessionId, tick]);
  const added = files?.reduce((n, f) => n + f.added, 0) ?? 0;
  const removed = files?.reduce((n, f) => n + f.removed, 0) ?? 0;
  return (
    <section aria-label="Changes" className="flex flex-col gap-2">
      <div className="panel-tools flex items-center gap-2 px-1 text-xs">
        <span className="text-muted-foreground">
          {files === null ? "Loading…" : `${files.length} ${files.length === 1 ? "file" : "files"} changed`}
        </span>
        {files !== null && files.length > 0 && (
          <span className="font-mono text-2xs">
            <span className="text-success-foreground">+{added}</span>{" "}
            <span className="text-destructive-foreground">−{removed}</span>
          </span>
        )}
        <Button
          variant="ghost-muted"
          size="icon-xs"
          className="ml-auto"
          aria-label="Refresh"
          title="Refresh"
          onClick={() => setTick((t) => t + 1)}
        >
          <RefreshCw />
        </Button>
      </div>
      {files?.length === 0 && <Empty>No changes yet.</Empty>}
      {files?.map((f) => (
        <div key={f.path} className="file overflow-hidden rounded-lg border bg-card">
          <button
            type="button"
            className="file-head flex w-full cursor-pointer items-center gap-2 px-3 py-2 text-left hover:bg-accent/60"
            onClick={() => setOpen(open === f.path ? null : f.path)}
            aria-expanded={open === f.path}
          >
            <ChevronRight
              className={cn("size-3 shrink-0 text-muted-foreground transition-transform", open === f.path && "rotate-90")}
            />
            <PathLabel path={f.path} />
            <span className="add font-mono text-2xs text-success-foreground">+{f.added}</span>
            <span className="del font-mono text-2xs text-destructive-foreground">−{f.removed}</span>
          </button>
          {open === f.path && (
            <div className="border-t">
              <DiffView diff={f.diff ?? ""} truncated={f.diff_truncated} />
            </div>
          )}
        </div>
      ))}
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
    <section aria-label="Approvals" className="flex flex-col gap-4">
      {groups.map((g) => (
        <div key={g.sessionId} className="approval-group flex flex-col gap-2">
          <button
            type="button"
            className="group-head flex w-fit cursor-pointer items-center gap-1 px-1 font-medium text-muted-foreground text-xs hover:text-foreground"
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
    <section aria-label="Queue" className="flex flex-col gap-2">
      {items.map((q) => (
        <div key={q.id} className="queued flex flex-col gap-2 rounded-lg border bg-card p-3">
          <div className="text-xs">
            <span className="approval-agent font-medium">for {q.agent}</span>
          </div>
          <p className="whitespace-pre-wrap text-[13px]">{q.text}</p>
          <div className="approval-actions flex justify-end gap-1.5">
            <Button size="sm" variant="ghost-muted" onClick={() => void c.cancelQueued(sessionId, q.id)}>
              Cancel
            </Button>
            <Button size="sm" variant="outline" onClick={() => void c.steer(sessionId, q.id)}>
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
  onClose,
  onOpen,
}: {
  c: DeckClient;
  state: AppState;
  panel: Panel;
  open: Session | undefined;
  onClose: () => void;
  onOpen: (id: string) => void;
}) {
  return (
    <aside className="panel flex w-[440px] shrink-0 flex-col border-l bg-background" aria-label="Side panel">
      <header className="panel-head drag flex h-12 shrink-0 items-center gap-1.5 border-b px-2">
        <h2 className="px-2 font-medium text-[13px]">{PANELS.find((p) => p.panel === panel)?.label}</h2>
        {panel === "approvals" && state.approvals.length > 0 && (
          <Badge variant="warning">{state.approvals.length}</Badge>
        )}
        <Button
          variant="ghost-muted"
          size="icon-sm"
          className="ml-auto"
          aria-label="Close the panel"
          onClick={onClose}
        >
          <X />
        </Button>
      </header>
      <div className="panel-body min-h-0 flex-1 overflow-y-auto p-3">
        {panel === "approvals" && <Approvals c={c} state={state} openId={open?.id ?? null} onOpen={onOpen} />}
        {panel === "changes" &&
          (open === undefined ? <Empty>Open a session to see its changes.</Empty> : <Changes c={c} sessionId={open.id} />)}
        {panel === "queue" &&
          (open === undefined ? <Empty>Open a session to see its queue.</Empty> : <Queue c={c} state={state} sessionId={open.id} />)}
      </div>
    </aside>
  );
}
