import type { Approval } from "@web/protocol";
import { useCallback, useEffect, useState } from "react";
import type { AgentSummary, HighlightItem, HighlightKind } from "../../shared/bridge";
import { useRoomStatus } from "../appState";
import { approvalSummary } from "../approvals";
import type { DeckClient } from "../client";
import { closedItems, groupHighlights, summaryRows } from "../highlights";
import { cn } from "../lib/cn";
import { useRoomLive } from "../live";
import { focusMessage } from "../palette";
import { Check, ChevronDown, ChevronRight, CircleAlert, Eye, Flag, type IconType, ListChecks, ShieldQuestion, X } from "./icons";
import { Button } from "./kit/button";
import { EmptyState } from "./kit/empty";
import { segmentItem, segmentTrack } from "./kit/segmented";

// The Highlights tab: what the agents marked as worth the person's
// attention, and a short line from each agent. Showing an item only clears
// its unread flag; Resolve and Dismiss are the person's own say.

// The room's own Highlights, read from the Deck and acted on through it.
// The room's pending approvals ride along in Needs you, read from the
// attention state the composer already answers; the panel adds no second copy.
export function RoomHighlights({
  c,
  sid,
  projectKey,
  approvals,
  onOpenTasks,
}: {
  c: DeckClient;
  sid: string;
  projectKey: string;
  approvals: readonly Approval[];
  onOpenTasks: () => void;
}) {
  const live = useRoomLive(c, sid);
  // An error or a blocked task the room waits on; an approval is listed as
  // itself, so it is not named twice.
  const status = useRoomStatus(projectKey, sid);
  const attention = status?.state === "needs-you" && status.need === "error" ? status.label : null;
  const act = useCallback(
    (action: "resolve" | "dismiss", id: string) => void c.highlightAction?.(sid, { action, id }),
    [c, sid],
  );
  const onSeen = useCallback((ids: string[]) => void c.highlightAction?.(sid, { action: "seen", ids }), [c, sid]);
  const onOpenSource = useCallback(
    (item: HighlightItem) => {
      if (item.source?.message_id) focusMessage(sid, item.source.message_id);
      else if (item.source?.task_id) onOpenTasks();
    },
    [sid, onOpenTasks],
  );
  return (
    <HighlightsPanel
      items={live.items}
      summaries={live.summaries}
      approvals={approvals}
      attention={attention}
      onResolve={(id) => act("resolve", id)}
      onDismiss={(id) => act("dismiss", id)}
      onSeen={onSeen}
      onOpenSource={onOpenSource}
    />
  );
}

type View = "highlights" | "summary";

const KIND_ICON: Record<HighlightKind, IconType> = {
  needs_you: CircleAlert,
  blocker: Flag,
  finding: Eye,
};

const KIND_TONE: Record<HighlightKind, string> = {
  needs_you: "text-warning-foreground",
  blocker: "text-destructive-foreground",
  finding: "text-info-foreground",
};

export interface HighlightsPanelProps {
  items: readonly HighlightItem[];
  summaries: readonly AgentSummary[];
  // Approvals the room waits on: shown under Needs you, answered on the composer.
  approvals: readonly Approval[];
  // What else the room waits on (an error, a blocked task), from its status.
  attention: string | null;
  onResolve: (id: string) => void;
  onDismiss: (id: string) => void;
  // Clears the unread flag of items the panel has shown.
  onSeen: (ids: string[]) => void;
  // Scrolls the room to the message or task the item came from.
  onOpenSource: (item: HighlightItem) => void;
}

export function HighlightsPanel({ items, summaries, approvals, attention, onResolve, onDismiss, onSeen, onOpenSource }: HighlightsPanelProps) {
  const [view, setView] = useState<View>("highlights");
  const [history, setHistory] = useState(false);
  const groups = groupHighlights(items);
  const closed = closedItems(items);

  const unreadIds = view === "highlights" ? items.filter((i) => i.state === "open" && i.unread).map((i) => i.id) : [];
  const unreadKey = unreadIds.join(",");
  // biome-ignore lint/correctness/useExhaustiveDependencies: the joined ids stand for the list
  useEffect(() => {
    if (unreadIds.length > 0) onSeen(unreadIds);
  }, [unreadKey, onSeen]);

  return (
    <section aria-label="Highlights" className="highlights flex min-h-0 flex-1 flex-col">
      <div className="flex h-10 shrink-0 items-center border-hairline border-b px-3.5">
        <div role="tablist" aria-label="Highlights views" className={segmentTrack()}>
          {(["highlights", "summary"] as const).map((v) => (
            <button
              key={v}
              type="button"
              role="tab"
              aria-selected={view === v}
              className={segmentItem({ on: view === v })}
              onClick={() => setView(v)}
            >
              {v === "highlights" ? "Highlights" : "Summary"}
            </button>
          ))}
        </div>
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto">
        {view === "summary" ? (
          <SummaryView summaries={summaries} />
        ) : groups.length === 0 && closed.length === 0 && approvals.length === 0 && attention === null ? (
          <EmptyState icon={ListChecks} title="Nothing to flag" hint="When an agent finds a blocker, a risk or a decision for you, it shows here. You can pin any message too." />
        ) : (
          <>
            {groups.length === 0 && approvals.length === 0 && attention === null && <p className="px-4 py-6 text-center text-faint text-xs">Nothing open.</p>}
            {((approvals.length > 0 || attention !== null) && groups[0]?.kind !== "needs_you" ? [{ kind: "needs_you" as const, label: "Needs you", items: [] as HighlightItem[] }, ...groups] : groups).map((g) => (
              <div key={g.kind} className="border-hairline border-b px-3.5 py-3 last:border-b-0">
                <h3 className="mb-2 flex items-center gap-1.5 font-medium text-2xs text-muted-foreground uppercase tracking-wide">
                  {g.label}
                  <span className="tabular-nums">{g.items.length + (g.kind === "needs_you" ? approvals.length + (attention === null ? 0 : 1) : 0)}</span>
                </h3>
                <ul className="flex flex-col gap-2">
                  {g.kind === "needs_you" && attention !== null && (
                    <li className="card rounded-lg border border-hairline px-2.5 py-2" data-attention="">
                      <p className="flex items-start gap-2 text-sm">
                        <CircleAlert aria-hidden className="mt-0.5 size-4 shrink-0 text-warning-foreground" />
                        <span className="min-w-0 flex-1">{attention}</span>
                      </p>
                    </li>
                  )}
                  {g.kind === "needs_you" && approvals.map((a) => <ApprovalRow key={a.id} a={a} />)}
                  {g.items.map((i) => (
                    <ItemRow key={i.id} item={i} onResolve={onResolve} onDismiss={onDismiss} onOpenSource={onOpenSource} />
                  ))}
                </ul>
              </div>
            ))}
            {closed.length > 0 && (
              <div className="px-3.5 py-3">
                <button
                  type="button"
                  className="flex cursor-pointer items-center gap-1 text-muted-foreground text-xs outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
                  aria-expanded={history}
                  onClick={() => setHistory((h) => !h)}
                >
                  {history ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                  History ({closed.length})
                </button>
                {history && (
                  <ul className="mt-2 flex flex-col gap-1.5">
                    {closed.map((i) => (
                      <li key={i.id} className="flex items-center gap-2 text-faint text-xs">
                        <span className="min-w-0 flex-1 truncate line-through" title={i.title}>
                          {i.title}
                        </span>
                        <span>{i.state === "resolved" ? "Resolved" : "Dismissed"}</span>
                      </li>
                    ))}
                  </ul>
                )}
              </div>
            )}
          </>
        )}
      </div>
    </section>
  );
}

function ApprovalRow({ a }: { a: Approval }) {
  return (
    <li className="card rounded-lg border border-hairline px-2.5 py-2" data-approval-row={a.id}>
      <div className="flex items-start gap-2">
        <ShieldQuestion aria-hidden className="mt-0.5 size-4 shrink-0 text-warning-foreground" />
        <div className="min-w-0 flex-1">
          <p className="truncate font-mono text-xs" title={approvalSummary(a)}>
            {approvalSummary(a)}
          </p>
          <p className="mt-1 text-2xs text-faint">{a.agent} · Approve or decline above the composer</p>
        </div>
      </div>
    </li>
  );
}

function ItemRow({
  item,
  onResolve,
  onDismiss,
  onOpenSource,
}: {
  item: HighlightItem;
  onResolve: (id: string) => void;
  onDismiss: (id: string) => void;
  onOpenSource: (item: HighlightItem) => void;
}) {
  const Icon = KIND_ICON[item.kind];
  const hasSource = item.source !== null && (item.source.message_id !== "" || item.source.task_id !== "");
  return (
    <li className="group/item card rounded-lg border border-hairline px-2.5 py-2" data-kind={item.kind} data-unread={item.unread || undefined}>
      <div className="flex items-start gap-2">
        <Icon aria-hidden className={cn("mt-0.5 size-4 shrink-0", KIND_TONE[item.kind])} />
        <div className="min-w-0 flex-1">
          <p className="text-sm leading-snug">
            {item.title}
            {item.unread && <span aria-label="Unread" className="ml-1.5 inline-block size-1.5 rounded-full bg-info align-middle" />}
          </p>
          {item.detail !== "" && <p className="mt-0.5 whitespace-pre-wrap text-muted-foreground text-xs leading-relaxed">{item.detail}</p>}
          <p className="mt-1 flex items-center gap-2 text-2xs text-faint">
            <span>{item.agent}</span>
            {hasSource && (
              <button type="button" className="cursor-pointer underline-offset-2 outline-none hover:underline focus-visible:ring-2 focus-visible:ring-ring" onClick={() => onOpenSource(item)}>
                Show source
              </button>
            )}
          </p>
        </div>
        <div className="flex shrink-0 gap-0.5">
          <Button variant="ghost-muted" size="icon-xs" aria-label={`Resolve ${item.title}`} title="Resolve" onClick={() => onResolve(item.id)}>
            <Check />
          </Button>
          <Button variant="ghost-muted" size="icon-xs" aria-label={`Dismiss ${item.title}`} title="Dismiss" onClick={() => onDismiss(item.id)}>
            <X />
          </Button>
        </div>
      </div>
    </li>
  );
}

function SummaryView({ summaries }: { summaries: readonly AgentSummary[] }) {
  const rows = summaryRows(summaries);
  if (rows.length === 0) {
    return <EmptyState icon={ListChecks} title="No updates yet" hint="Each agent's latest short update shows here, with where it came from." />;
  }
  return (
    <ul className="flex flex-col gap-2.5 px-3.5 py-3">
      {rows.map((r) => (
        <li key={r.agent} className="text-sm">
          <p className="flex items-baseline gap-2">
            <span className="font-medium">{r.agent}</span>
            <span className="text-2xs text-faint">agent update</span>
          </p>
          <p className="mt-0.5 text-muted-foreground text-xs leading-relaxed">{r.text}</p>
        </li>
      ))}
    </ul>
  );
}
