import type { Decision } from "@web/protocol";
import { relativeTime } from "@web/time";
import { CircleCheck, FileDiff, GitCompareArrows, Inbox as InboxIcon, Info, ShieldQuestion, Users } from "lucide-react";
import { type KeyboardEvent, useEffect, useRef, useState } from "react";
import { approvalSummary, DECISIONS } from "../approvals";
import type { InboxItem, InboxState } from "../attention";
import { cn } from "../lib/cn";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { EmptyState } from "./kit/empty";

const GLYPH: Record<InboxState, { icon: typeof Info; className: string }> = {
  "needs-you": { icon: ShieldQuestion, className: "text-warning-foreground" },
  overlap: { icon: GitCompareArrows, className: "text-warning-foreground" },
  review: { icon: FileDiff, className: "text-info-foreground" },
  done: { icon: CircleCheck, className: "text-success-foreground" },
  notice: { icon: Info, className: "text-muted-foreground" },
};

export interface InboxActions {
  onAnswer: (item: InboxItem, d: Decision) => void;
  onContract: (item: InboxItem, accept: boolean) => void;
  onOpen: (item: InboxItem) => void;
}

function Row({
  item,
  selected,
  now,
  onSelect,
  actions,
}: {
  item: InboxItem;
  selected: boolean;
  now: number;
  onSelect: () => void;
  actions: InboxActions;
}) {
  const g = item.contract !== undefined ? { icon: Users, className: "text-warning-foreground" } : GLYPH[item.state];
  const ref = useRef<HTMLLIElement>(null);
  useEffect(() => {
    if (selected) ref.current?.scrollIntoView({ block: "nearest" });
  }, [selected]);
  // The buttons keep the focus on the list, so j and k go on working.
  const keep = (e: React.MouseEvent) => e.preventDefault();
  return (
    <li
      ref={ref}
      role="option"
      aria-selected={selected}
      data-item={item.id}
      data-state={item.state}
      className={cn("inbox-item flex cursor-pointer gap-3 rounded-lg px-3 py-2.5", selected ? "bg-selected inset-shadow-hl" : "hover:bg-hover")}
      onClick={onSelect}
      onDoubleClick={() => item.action === "open" && actions.onOpen(item)}
    >
      <g.icon aria-hidden className={cn("mt-0.5 size-4 shrink-0", g.className)} />
      <div className="flex min-w-0 flex-1 flex-col gap-1">
        <div className="flex min-w-0 items-center gap-2 text-2xs text-faint">
          <span className="min-w-0 truncate">
            {item.projectName} · {item.title}
          </span>
          {item.at > 0 && <span className="ml-auto shrink-0 tabular-nums">{relativeTime(new Date(item.at).toISOString(), now)}</span>}
        </div>
        <span className="inbox-label min-w-0 truncate text-sm">{item.label}</span>
        {item.approval !== undefined && (
          <code className="block max-h-24 min-w-0 overflow-auto whitespace-pre-wrap break-all font-mono text-muted-foreground text-xs">
            {approvalSummary(item.approval)}
          </code>
        )}
        {item.contract !== undefined && item.contract.brief !== "" && (
          <p className="line-clamp-3 whitespace-pre-wrap text-muted-foreground text-xs">{item.contract.brief}</p>
        )}
        <div className="flex flex-wrap items-center justify-end gap-1.5">
          {item.approval !== undefined &&
            [...DECISIONS].reverse().map((d) => (
              <Button
                key={d.decision}
                size="xs"
                variant={d.decision === "allow" ? "default" : d.decision === "deny" ? "ghost-muted" : "outline"}
                onMouseDown={keep}
                onClick={(e) => {
                  e.stopPropagation();
                  actions.onAnswer(item, d.decision);
                }}
              >
                {d.label}
              </Button>
            ))}
          {item.contract !== undefined && (
            <>
              <Button size="xs" variant="ghost-muted" onMouseDown={keep} onClick={() => actions.onContract(item, false)}>
                Decline
              </Button>
              <Button size="xs" onMouseDown={keep} onClick={() => actions.onContract(item, true)}>
                Accept
              </Button>
            </>
          )}
          {item.room !== null && (
            <Button size="xs" variant={item.action === "open" ? "outline" : "ghost-muted"} onMouseDown={keep} onClick={() => actions.onOpen(item)}>
              Open
            </Button>
          )}
        </div>
      </div>
    </li>
  );
}

// Everything that waits for the person, across the open projects. Keyboard
// first: j and k move, y and n answer, Enter opens the room.
export function Inbox({ items, mac, ...actions }: { items: InboxItem[]; mac: boolean } & InboxActions) {
  const [pick, setPick] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const list = useRef<HTMLUListElement>(null);
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  const empty = items.length === 0;
  useEffect(() => {
    if (!empty) list.current?.focus();
  }, [empty]);
  // The selection stays on its item, or on the same place when the item
  // goes (an answered approval): the next one is then selected.
  const last = useRef(0);
  let index = items.findIndex((i) => i.id === pick);
  if (index < 0) index = Math.min(last.current, items.length - 1);
  last.current = Math.max(0, index);
  const current = items[index];

  const onKey = (e: KeyboardEvent) => {
    if (e.ctrlKey || e.metaKey || e.altKey) return;
    const move = (to: number) => {
      const it = items[Math.max(0, Math.min(items.length - 1, to))];
      if (it !== undefined) setPick(it.id);
    };
    if (e.key === "j" || e.key === "ArrowDown") move(index + 1);
    else if (e.key === "k" || e.key === "ArrowUp") move(index - 1);
    else if (current === undefined) return;
    else if (e.key === "y" || e.key === "n") {
      if (current.approval !== undefined) actions.onAnswer(current, e.key === "y" ? "allow" : "deny");
      else if (current.contract !== undefined) actions.onContract(current, e.key === "y");
      else return;
    } else if (e.key === "Enter" && current.room !== null) actions.onOpen(current);
    else return;
    e.preventDefault();
  };

  return (
    <section aria-label="Inbox" className="inbox flex min-h-0 flex-1 flex-col">
      <header className="drag flex h-13 shrink-0 items-center gap-2 px-5">
        <InboxIcon className="size-4 text-muted-foreground" />
        <h1 className="font-medium text-sm">Inbox</h1>
        {items.length > 0 && <span className="font-mono text-2xs text-faint tabular-nums">{items.length}</span>}
        <span className="ml-auto hidden items-center gap-1 text-2xs text-faint sm:flex">
          <Kbd>j</Kbd>
          <Kbd>k</Kbd> move · <Kbd>y</Kbd>
          <Kbd>n</Kbd> answer · <Kbd>Enter</Kbd> opens · <Kbd>{mac ? "⌘⌥L" : "Ctrl Alt L"}</Kbd> next room
        </span>
      </header>
      {empty ? (
        <EmptyState
          icon={InboxIcon}
          title="Nothing waits for you."
          hint="Approvals, finished rooms and overlaps show up here."
          className="inbox-empty flex-1 justify-center pb-[14vh]"
        />
      ) : (
        <ul
          ref={list}
          role="listbox"
          aria-label="Waiting for you"
          tabIndex={0}
          className="mx-auto flex w-full max-w-3xl flex-col gap-1 overflow-y-auto px-4 pb-6 outline-none"
          onKeyDown={onKey}
        >
          {items.map((it, i) => (
            <Row key={it.id} item={it} selected={i === index} now={now} onSelect={() => setPick(it.id)} actions={actions} />
          ))}
        </ul>
      )}
    </section>
  );
}
