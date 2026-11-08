import type { Session } from "@web/protocol";
import { sessionTitle } from "@web/state";
import { relativeTime } from "@web/time";
import { Fragment, memo, type ReactElement, type ReactNode, useEffect, useRef, useState } from "react";
import type { ArchiveOffer, ThreadChoice } from "../../shared/bridge";
import type { RoomStatus } from "../attention";
import { cn } from "../lib/cn";
import { sameData } from "../select";
import { SETTINGS_SECTIONS, type SettingsSection } from "../ui";
import { ArrowLeft, ChevronDown, Plus } from "./icons";
import { Button } from "./kit/button";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "./kit/menu";
import type { SidebarItem } from "./ProjectGroup";
import type { ProjectEntry } from "./Sidebar";
import { Mark } from "./ThreadRow";

// The threads as a bar across the top of the window (Settings > Appearance
// > Layout, "Threads in a top bar"): the project switcher, the threads as
// chips with their state's dot, the open one a soft grey card, a "+" for
// a new thread and a menu of every thread, then Search, the inbox and the
// sidebar foot's controls. The chips scroll sideways (the wheel scrolls
// them too) and fade at an edge with more behind it. With Settings open
// the chips give way to its sections.

// One project's threads in the bar: the live ones, then the settled, then
// the archived (in the menu only).
export interface BarGroup {
  project: ProjectEntry;
  live: SidebarItem[];
  settled: SidebarItem[];
  archived: SidebarItem[];
}

// A tab of the bar: a pill of the selection when it is the open thread.
const barTab = (on: boolean) =>
  cn(
    "flex h-7 shrink-0 cursor-pointer items-center gap-1.5 whitespace-nowrap rounded-[8px] px-2.5 text-[12.5px] outline-none transition-colors focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring [&_svg]:size-3.5 [&_svg]:shrink-0",
    on ? "bg-selected text-foreground" : "text-muted-foreground hover:bg-hover hover:text-foreground",
  );

export function ThreadBar({
  lights,
  openId,
  groups,
  chip,
  settings,
  onSettings,
  onOpenItem,
  onNew,
  newTitle,
  left,
  right,
}: {
  onNew: () => void;
  newTitle: string;
  // A Mac's traffic lights sit at the bar's start.
  lights: boolean;
  openId: string | null;
  groups: BarGroup[];
  chip: (x: SidebarItem, settled: boolean) => ReactElement;
  settings: SettingsSection | null;
  onSettings: (s: SettingsSection | null) => void;
  onOpenItem: (x: SidebarItem) => void;
  // The project switcher, and Search, the inbox and the foot's controls.
  left: ReactNode;
  right: ReactNode;
}) {
  const strip = useRef<HTMLUListElement>(null);
  const [edges, setEdges] = useState({ start: false, end: false });
  const many = groups.length > 1;
  // Which edges have chips behind them, as the strip scrolls or resizes.
  useEffect(() => {
    const el = strip.current;
    if (el === null) return;
    const measure = () => {
      const start = el.scrollLeft > 1;
      const end = el.scrollLeft + el.clientWidth < el.scrollWidth - 1;
      setEdges((e) => (e.start === start && e.end === end ? e : { start, end }));
    };
    measure();
    el.addEventListener("scroll", measure, { passive: true });
    const watch = new ResizeObserver(measure);
    watch.observe(el);
    return () => {
      el.removeEventListener("scroll", measure);
      watch.disconnect();
    };
  }, [settings]);
  // The chip of a thread that opens comes into view.
  useEffect(() => {
    strip.current?.querySelector(".session.open")?.scrollIntoView({ block: "nearest", inline: "nearest" });
  }, [openId]);
  return (
    <div className={cn("drag relative flex h-(--topbar-h) w-full min-w-0 items-center gap-1 pr-1.5", lights ? "pl-[78px]" : "pl-2")}>
      {settings !== null ? (
        <>
          <button type="button" className={barTab(false)} onClick={() => onSettings(null)}>
            <ArrowLeft />
            Back to threads
          </button>
          <span aria-hidden className="bar-rule" />
          <ul ref={strip} className="no-drag flex min-w-0 flex-1 items-center gap-0.5 overflow-x-auto [scrollbar-width:none]" aria-label="Settings sections">
            {SETTINGS_SECTIONS.map((x) => (
              <li key={x.section} className="list-none">
                <button type="button" aria-current={settings === x.section ? "page" : undefined} className={barTab(settings === x.section)} onClick={() => onSettings(x.section)}>
                  {x.label}
                </button>
              </li>
            ))}
          </ul>
        </>
      ) : (
        <>
          {left}
          <span aria-hidden className="bar-rule" />
          <div className="flex min-w-0 flex-1 items-center gap-0.5">
            <ul
              ref={strip}
              className="thread-strip no-drag flex min-w-0 shrink items-center gap-0.5 overflow-x-auto [scrollbar-width:none]"
              data-fade-start={edges.start ? "" : undefined}
              data-fade-end={edges.end ? "" : undefined}
              onWheel={(e) => {
                if (Math.abs(e.deltaY) > Math.abs(e.deltaX)) e.currentTarget.scrollLeft += e.deltaY;
              }}
            >
              {groups.map((g) => (
                <Fragment key={g.project.key}>
                  {many && (g.live.length > 0 || g.settled.length > 0) && (
                    <li className="project-group bar-project list-none" data-project={g.project.name}>
                      <span className="flex h-7 items-center whitespace-nowrap pr-1 pl-2 font-medium text-2xs text-faint">{g.project.name}</span>
                    </li>
                  )}
                  {g.live.map((x) => chip(x, false))}
                  {g.settled.map((x) => chip(x, true))}
                </Fragment>
              ))}
            </ul>
            <Button variant="ghost-muted" size="icon-sm" className="new-thread shrink-0" aria-label="New thread" title={newTitle} onClick={onNew}>
              <Plus />
            </Button>
            <AllThreads groups={groups} onOpen={onOpenItem} />
            {/* The rest of the bar still drags the window. */}
            <span aria-hidden className="h-full min-w-4 flex-1" />
          </div>
        </>
      )}
      {right}
    </div>
  );
}

// Every thread, project by project, the archived last: what the bar has
// no room for, and the way back to an archived thread.
function AllThreads({ groups, onOpen }: { groups: BarGroup[]; onOpen: (x: SidebarItem) => void }) {
  const [open, setOpen] = useState(false);
  const now = Date.now();
  const item = (x: SidebarItem) => {
    const state = x.status === undefined || x.status.state === "idle" ? null : x.status.state;
    return (
      <MenuItem
        key={`${x.source}/${x.s.id}`}
        onClick={() => {
          setOpen(false);
          onOpen(x);
        }}
      >
        <Mark state={state} label={x.status?.label ?? ""} />
        <span className="min-w-0 flex-1 truncate">{sessionTitle(x.s)}</span>
        <span className="text-2xs text-faint tabular-nums">{relativeTime(x.s.updated_at, now)}</span>
      </MenuItem>
    );
  };
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      label="All threads"
      className="max-h-[70vh] w-80 overflow-y-auto"
      trigger={
        <Button variant="ghost-muted" size="icon-sm" className="shrink-0" aria-label="All threads" title="All threads" aria-expanded={open} onClick={() => setOpen((o) => !o)}>
          <ChevronDown />
        </Button>
      }
    >
      {groups.map((g, i) => (
        <Fragment key={g.project.key}>
          {i > 0 && <MenuSeparator />}
          <MenuLabel>{g.project.name}</MenuLabel>
          {g.live.map(item)}
          {g.settled.map(item)}
          {g.live.length + g.settled.length === 0 && <div className="px-2 pb-1.5 text-faint text-xs">No threads yet</div>}
          {g.archived.length > 0 && <MenuLabel>Archived</MenuLabel>}
          {g.archived.map(item)}
        </Fragment>
      ))}
    </Popover>
  );
}

// A thread as a chip of the bar: its state's dot and its title; a double
// click renames it, the context menu offers Rename, Archive and Delete.
export const ThreadChip = memo(function ThreadChip({
  s,
  active,
  unread,
  status,
  settled,
  digit,
  archive,
  onOpen,
  onRename,
  onArchive,
  onUnarchive,
  onDelete,
  onMenu,
}: {
  s: Session;
  active: boolean;
  unread: boolean;
  status: RoomStatus | undefined;
  settled: boolean;
  digit: number | null;
  archive: ArchiveOffer;
  onOpen: () => void;
  onRename: (title: string) => void;
  onArchive: () => void;
  onUnarchive: (() => void) | undefined;
  onDelete: () => void;
  onMenu: () => Promise<ThreadChoice>;
}) {
  const [renaming, setRenaming] = useState(false);
  const state = status === undefined || status.state === "idle" ? null : status.state;
  const label = status?.label ?? "";
  const title = sessionTitle(s);
  return (
    <li className={cn("session list-none", active && "open")} data-session={s.id} data-settled={settled || undefined}>
      <div
        role="button"
        tabIndex={0}
        aria-label={title}
        aria-current={active ? "page" : undefined}
        title={label !== "" ? `${title} · ${label}` : title}
        data-archive={archive}
        className={cn(
          "chip group/chip relative flex h-7 max-w-56 cursor-pointer select-none items-center gap-1.5 rounded-[8px] pr-2.5 text-[12.5px] outline-none transition-colors focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
          state === null ? "pl-2.5" : "pl-1.5",
          active ? "bg-selected text-foreground" : "hover:bg-hover",
          !active && (unread ? "font-medium text-foreground" : settled ? "text-faint hover:text-foreground" : "text-muted-foreground hover:text-foreground"),
        )}
        onClick={onOpen}
        onDoubleClick={() => setRenaming(true)}
        onKeyDown={(e) => {
          if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
          e.preventDefault();
          onOpen();
        }}
        onContextMenu={(e) => {
          if ((e.target as HTMLElement).closest("input")) return;
          e.preventDefault();
          void onMenu().then((choice) => {
            if (choice === "rename") setRenaming(true);
            else if (choice === "archive") onArchive();
            else if (choice === "unarchive") onUnarchive?.();
            else if (choice === "delete") onDelete();
          });
        }}
      >
        {state !== null && <Mark state={state} label={label} />}
        {renaming ? (
          <input
            autoFocus
            defaultValue={s.title}
            aria-label="Thread title"
            onClick={(e) => e.stopPropagation()}
            onFocus={(e) => e.currentTarget.select()}
            onKeyDown={(e) => {
              e.stopPropagation();
              if (e.key === "Enter") {
                const t = e.currentTarget.value.trim();
                if (t !== "") onRename(t);
                setRenaming(false);
              } else if (e.key === "Escape") setRenaming(false);
            }}
            onBlur={() => setRenaming(false)}
            className="w-36 min-w-0 rounded-sm border border-input bg-card px-1 text-[12.5px] outline-none focus:border-primary/50"
          />
        ) : (
          <span className="session-title min-w-0 truncate">{title}</span>
        )}
        {digit !== null ? (
          <span className="row-digit font-medium text-2xs text-foreground tabular-nums">{digit}</span>
        ) : (
          state === "needs-you" && s.pending > 1 && <span className="font-medium text-2xs text-warning-foreground tabular-nums">{s.pending}</span>
        )}
      </div>
    </li>
  );
}, sameData);
