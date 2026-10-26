import type { Decision } from "@web/protocol";
import { relativeTime } from "@web/time";
import { CircleCheck, FileDiff, GitCompareArrows, Inbox as InboxIcon, Info, ShieldQuestion, Users } from "./icons";
import { type KeyboardEvent, useEffect, useRef, useState } from "react";
import { approvalSummary, DECISIONS } from "../approvals";
import type { InboxItem, InboxState } from "../attention";
import { cn } from "../lib/cn";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { EmptyPlate } from "./kit/plate";
import { PLATES } from "../plates";

// Each state's glyph, its colour and its group, most urgent first: what
// waits for an answer wears the warning colour, changes to review the
// accent, a finished room the success colour, a notice nothing.
const STATES: Record<InboxState, { icon: typeof Info; tone: string; group: string }> = {
  "needs-you": { icon: ShieldQuestion, tone: "text-warning-foreground", group: "Needs you" },
  overlap: { icon: GitCompareArrows, tone: "text-warning-foreground", group: "Overlapping work" },
  review: { icon: FileDiff, tone: "text-info-foreground", group: "To review" },
  done: { icon: CircleCheck, tone: "text-success-foreground", group: "Finished" },
  notice: { icon: Info, tone: "text-faint", group: "Notices" },
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
  const s = STATES[item.state];
  const Icon = item.contract !== undefined ? Users : s.icon;
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (selected) ref.current?.scrollIntoView({ block: "nearest" });
  }, [selected]);
  // The buttons keep the focus on the list, so j and k go on working.
  const keep = (e: React.MouseEvent) => e.preventDefault();
  // Only the selected item's answer wears the accent: y gives it.
  const strong = (on: boolean) => (on && selected ? "default" : "outline");
  return (
    <div
      ref={ref}
      role="option"
      aria-selected={selected}
      data-item={item.id}
      data-state={item.state}
      className={cn(
        "inbox-item flex cursor-pointer gap-3 rounded-[12px] px-3 py-3 transition-colors",
        selected ? "bg-selected" : "hover:bg-hover",
      )}
      onClick={onSelect}
      onDoubleClick={() => item.action === "open" && actions.onOpen(item)}
    >
      <Icon aria-hidden className={cn("mt-px size-4 shrink-0", s.tone)} weight={item.state === "needs-you" ? "fill" : "regular"} />
      <div className="flex min-w-0 flex-1 flex-col gap-1.5">
        <div className="flex min-w-0 items-baseline gap-2">
          <span className="inbox-label min-w-0 truncate font-medium text-[13px]">{item.label}</span>
          {item.at > 0 && <span className="ml-auto shrink-0 text-2xs text-faint tabular-nums">{relativeTime(new Date(item.at).toISOString(), now)}</span>}
        </div>
        <div className="-mt-1 min-w-0 truncate text-2xs text-faint">
          {item.projectName} <span aria-hidden>·</span> <span className="text-muted-foreground">{item.title}</span>
        </div>
        {item.approval !== undefined && (
          <code className="block max-h-24 min-w-0 overflow-auto whitespace-pre-wrap break-all rounded-[8px] bg-code px-2.5 py-1.5 font-mono text-foreground/85 text-xs">
            {approvalSummary(item.approval)}
          </code>
        )}
        {item.contract !== undefined && item.contract.brief !== "" && (
          <p className="line-clamp-3 whitespace-pre-wrap text-pretty text-muted-foreground text-xs">{item.contract.brief}</p>
        )}
        {(item.approval !== undefined || item.contract !== undefined || item.room !== null) && (
          <div className="flex flex-wrap items-center justify-end gap-1.5 pt-0.5">
            {item.room !== null && (
              <Button size="xs" variant="ghost-muted" className="mr-auto -ml-2" onMouseDown={keep} onClick={() => actions.onOpen(item)}>
                Open
              </Button>
            )}
            {item.approval !== undefined &&
              [...DECISIONS].reverse().map((d) => (
                <Button
                  key={d.decision}
                  size="xs"
                  variant={d.decision === "allow" ? strong(true) : d.decision === "deny" ? "ghost-muted" : "outline"}
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
                <Button size="xs" variant={strong(true)} onMouseDown={keep} onClick={() => actions.onContract(item, true)}>
                  Accept
                </Button>
              </>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

// Everything that waits for the person, across the open projects, in
// groups by how urgent it is. Keyboard first: j and k move, y and n answer,
// Enter opens the room. With nothing waiting, a quiet print and the way
// back to the threads (onBack, when the shell gives it).
export function Inbox({
  items,
  mac,
  onBack,
  ...actions
}: { items: InboxItem[]; mac: boolean; onBack?: () => void } & InboxActions) {
  const [pick, setPick] = useState<string | null>(null);
  const [now, setNow] = useState(() => Date.now());
  const list = useRef<HTMLDivElement>(null);
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
  const urgent = items.filter((i) => i.state === "needs-you").length;

  // The items come sorted by state, so each group is a run of them.
  const groups: { state: InboxState; items: { item: InboxItem; i: number }[] }[] = [];
  items.forEach((item, i) => {
    const g = groups[groups.length - 1];
    if (g !== undefined && g.state === item.state) g.items.push({ item, i });
    else groups.push({ state: item.state, items: [{ item, i }] });
  });

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
        <h1 className="font-semibold text-sm">Inbox</h1>
        {urgent > 0 ? (
          <span className="font-medium text-warning-foreground text-xs tabular-nums">
            {urgent} {urgent === 1 ? "needs" : "need"} you
          </span>
        ) : (
          items.length > 0 && <span className="text-2xs text-faint tabular-nums">{items.length}</span>
        )}
        {!empty && (
          <span className="ml-auto hidden items-center gap-1 text-2xs text-faint lg:flex">
            <Kbd>j</Kbd>
            <Kbd>k</Kbd>
            <span className="mr-2 ml-0.5">move</span>
            <Kbd>y</Kbd>
            <Kbd>n</Kbd>
            <span className="mr-2 ml-0.5">answer</span>
            <Kbd>↵</Kbd>
            <span className="mr-2 ml-0.5">open</span>
            <Kbd>{mac ? "⌘⌥L" : "Ctrl Alt L"}</Kbd>
            <span className="ml-0.5">next room</span>
          </span>
        )}
      </header>
      {empty ? (
        <div className="flex flex-1 flex-col justify-center pb-[12vh]">
          <EmptyPlate
            art={PLATES.sea}
            focus={[0.5, 0.45]}
            title="Nothing waits for you."
            hint="Approvals, finished rooms and overlaps show up here, across every open project."
            className="inbox-empty"
            action={
              onBack !== undefined && (
                <Button variant="outline" size="sm" onClick={onBack}>
                  Back to threads
                </Button>
              )
            }
          />
        </div>
      ) : (
        <div
          ref={list}
          role="listbox"
          aria-label="Waiting for you"
          tabIndex={0}
          className="flex min-h-0 flex-1 flex-col overflow-y-auto outline-none"
          onKeyDown={onKey}
        >
          <div className="mx-auto flex w-full max-w-3xl flex-col gap-5 px-4 pt-1 pb-8">
            {groups.map((g) => (
              <div key={g.state} role="group" aria-label={STATES[g.state].group} className="flex animate-enter flex-col gap-1">
                <div aria-hidden className="flex items-baseline gap-2 px-3 pb-1 font-semibold text-xs">
                  <span className={g.state === "needs-you" ? "text-warning-foreground" : "text-muted-foreground"}>{STATES[g.state].group}</span>
                  <span className="font-normal text-faint tabular-nums">{g.items.length}</span>
                </div>
                {g.items.map(({ item, i }) => (
                  <Row key={item.id} item={item} selected={i === index} now={now} onSelect={() => setPick(item.id)} actions={actions} />
                ))}
              </div>
            ))}
          </div>
        </div>
      )}
    </section>
  );
}
