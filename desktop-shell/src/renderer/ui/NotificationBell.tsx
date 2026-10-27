import { relativeTime } from "@web/time";
import { useEffect, useState } from "react";
import { cn } from "../lib/cn";
import { type NotifyEntry, type NotifyKind, unreadCount } from "../notifications";
import { clearAll, openEntry, readAll, useNotifyEntries } from "../notifyCentre";
import { usePrefs } from "../prefs";
import { At, Bell, BellSlash, Check, Checks, CircleAlert, CircleCheck, type IconType, ListChecks, ShieldQuestion, Trash2 } from "./icons";
import { Button } from "./kit/button";
import { Popover } from "./kit/menu";
import { ProjectIcon } from "./ProjectIcon";

// Each kind's glyph and colour, as the inbox wears them: what waits for an
// answer in the warning colour, an error in the destructive one, what is
// done in the success colour.
const KINDS: Record<NotifyKind, { icon: IconType; tone: string }> = {
  approval: { icon: ShieldQuestion, tone: "text-warning-foreground" },
  error: { icon: CircleAlert, tone: "text-destructive-foreground" },
  mention: { icon: At, tone: "text-info-foreground" },
  task: { icon: ListChecks, tone: "text-success-foreground" },
  finished: { icon: CircleCheck, tone: "text-success-foreground" },
};

function Entry({ e, now, onOpen }: { e: NotifyEntry; now: number; onOpen: () => void }) {
  const k = KINDS[e.kind];
  return (
    <li className="group/note relative list-none" data-notification={e.kind} data-read={e.read ? "" : undefined}>
      <button
        type="button"
        className="flex w-full cursor-pointer gap-2.5 rounded-[10px] px-2 py-2 text-left outline-none transition-colors hover:bg-hover focus-visible:bg-selected"
        onClick={onOpen}
      >
        <k.icon aria-hidden weight={e.read ? "regular" : "fill"} className={cn("mt-px size-4 shrink-0", k.tone, e.read && "opacity-60")} />
        <span className="flex min-w-0 flex-1 flex-col gap-1">
          <span className={cn("line-clamp-2 text-pretty text-[13px] leading-snug", e.read ? "text-muted-foreground" : "font-medium text-foreground")}>
            {e.text}
            {e.count > 1 && <span className="ml-1 font-normal text-faint tabular-nums">×{e.count}</span>}
          </span>
          <span className="flex min-w-0 items-center gap-1.5 text-2xs text-faint">
            <ProjectIcon source={e.source} name={e.projectName} size={12} />
            <span className="min-w-0 truncate">
              {e.projectName} <span aria-hidden>·</span> {e.roomTitle}
            </span>
            <span className="ml-auto shrink-0 tabular-nums">{relativeTime(new Date(e.at).toISOString(), now)}</span>
          </span>
        </span>
        {!e.read && <span aria-label="Unread" className="mt-1.5 size-1.5 shrink-0 rounded-full bg-primary" />}
      </button>
      {!e.read && (
        <Button
          variant="ghost-muted"
          size="icon-xs"
          aria-label="Mark read"
          title="Mark read"
          className="absolute top-1.5 right-1 opacity-0 transition-opacity focus-visible:opacity-100 group-focus-within/note:opacity-100 group-hover/note:opacity-100"
          onClick={() => readAll([e.id])}
        >
          <Check />
        </Button>
      )}
    </li>
  );
}

// The notification centre: a bell in the sidebar's foot with the count of
// unread notifications, and a popover of the recent ones, newest first. A
// click opens the room; Mark all read and Clear all keep the list tidy.
// The inbox stays the place to act on what waits; this is what happened.
export function NotificationBell({ onSettings }: { onSettings: () => void }) {
  const list = useNotifyEntries();
  const prefs = usePrefs();
  const [open, setOpen] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!open) return;
    setNow(Date.now());
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, [open]);
  const unread = unreadCount(list);
  const label = unread > 0 ? `Notifications, ${unread} unread` : "Notifications";
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      label="Notifications"
      role="dialog"
      side="top"
      className="notifications w-80 p-1.5"
      trigger={
        <Button
          variant="ghost-muted"
          size="icon-sm"
          aria-label={label}
          title={label}
          aria-expanded={open}
          className="notification-bell relative"
          onClick={() => setOpen((o) => !o)}
        >
          {prefs.notifications ? <Bell weight={open ? "fill" : "regular"} /> : <BellSlash />}
          {unread > 0 && (
            <span
              aria-hidden
              className="-top-0.5 -right-0.5 absolute grid h-3.5 min-w-3.5 place-items-center rounded-full bg-primary px-1 font-semibold text-[9px] text-primary-foreground tabular-nums leading-none"
            >
              {unread > 99 ? "99+" : unread}
            </span>
          )}
        </Button>
      }
    >
      <div className="flex items-center gap-2 py-1 pr-0.5 pl-2">
        <h2 className="font-semibold text-[13px]">Notifications</h2>
        {unread > 0 && <span className="text-2xs text-faint tabular-nums">{unread} unread</span>}
        <span className="ml-auto flex items-center">
          <Button variant="ghost-muted" size="icon-xs" aria-label="Mark all read" title="Mark all read" disabled={unread === 0} onClick={() => readAll()}>
            <Checks />
          </Button>
          <Button variant="ghost-muted" size="icon-xs" aria-label="Clear all" title="Clear all" disabled={list.length === 0} onClick={clearAll}>
            <Trash2 />
          </Button>
        </span>
      </div>
      {!prefs.notifications && (
        <p className="px-2 pb-1.5 text-pretty text-2xs text-muted-foreground">
          Notifications are off.{" "}
          <button
            type="button"
            className="cursor-pointer text-foreground underline-offset-2 hover:underline"
            onClick={() => {
              setOpen(false);
              onSettings();
            }}
          >
            Turn them on
          </button>
        </p>
      )}
      {list.length === 0 ? (
        <p className="px-2 pt-3 pb-4 text-center text-faint text-xs">Nothing yet. Approvals, errors, mentions and finished work show up here.</p>
      ) : (
        <ul className="flex max-h-[min(26rem,60vh)] flex-col gap-0.5 overflow-y-auto">
          {list.map((e) => (
            <Entry
              key={e.id}
              e={e}
              now={now}
              onOpen={() => {
                setOpen(false);
                openEntry(e);
              }}
            />
          ))}
        </ul>
      )}
    </Popover>
  );
}
