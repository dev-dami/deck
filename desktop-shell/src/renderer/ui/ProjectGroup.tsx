import type { Session } from "@web/protocol";
import { Check, ChevronDown, ChevronRight, Folder, FolderPlus, Laptop, SquarePen, Unplug, X } from "./icons";
import { type ReactElement, type ReactNode, type RefObject, useState } from "react";
import type { RoomStatus } from "../attention";
import type { TeamThread } from "../crew";
import { cn } from "../lib/cn";
import { Button } from "./kit/button";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "./kit/menu";
import { VirtualList } from "./kit/virtual";
import type { ForeignThread, ProjectEntry } from "./Sidebar";

// One thread in the sidebar, with its project and its room state.
export interface SidebarItem {
  s: Session;
  source: string;
  project: string;
  foreign: ForeignThread | null;
  status: RoomStatus | undefined;
}

// A room a teammate shares with you (Team presence).
export interface SharedEntry {
  peer: string;
  thread: TeamThread;
}

const itemKey = (x: SidebarItem) => `${x.source}/${x.s.id}`;

// A fold of threads: its name and count, and a line to the chevron.
function Fold({ name, count, shown, onToggle }: { name: string; count: number; shown: boolean; onToggle: () => void }) {
  return (
    <li className="list-none">
      <button
        type="button"
        className="mt-1 flex h-7 w-full cursor-pointer items-center gap-2 rounded-md px-2.5 text-2xs text-faint outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
        aria-expanded={shown}
        onClick={onToggle}
      >
        <span>{shown ? name : `${name} (${count})`}</span>
        <span className="h-px flex-1 bg-hairline" />
        {shown ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
      </button>
    </li>
  );
}

// The live threads, then the settled ones behind a fold, then the archived
// ones behind another, closed at first. A list past VIRTUAL_AFTER rows
// renders the rows in view only.
export function ThreadList({
  live,
  settled,
  shown,
  onToggle,
  archived,
  archivedShown,
  onToggleArchived,
  row,
  scroller,
  estimate,
}: {
  live: SidebarItem[];
  settled: SidebarItem[];
  // The settled threads show.
  shown: boolean;
  onToggle: () => void;
  archived: SidebarItem[];
  archivedShown: boolean;
  onToggleArchived: () => void;
  row: (x: SidebarItem) => ReactElement;
  scroller: RefObject<HTMLElement | null>;
  // A row's height before it is measured.
  estimate: number;
}) {
  const list = (items: SidebarItem[]) => (
    <VirtualList items={items} keyOf={itemKey} render={row} scroller={scroller} estimate={estimate} gap={1} as="li" className="flex flex-col gap-px" />
  );
  return (
    <>
      {list(live)}
      {settled.length > 0 && <Fold name="Settled" count={settled.length} shown={shown} onToggle={onToggle} />}
      {shown && list(settled)}
      {archived.length > 0 && <Fold name="Archived" count={archived.length} shown={archivedShown} onToggle={onToggleArchived} />}
      {archivedShown && list(archived)}
    </>
  );
}

// An open project's heading (collapse, new thread, close) over its threads.
export function ProjectGroup({
  group,
  count,
  current,
  shut,
  onToggle,
  onNew,
  onClose,
  children,
}: {
  group: ProjectEntry;
  count: number;
  // The project whose thread is open.
  current: boolean;
  shut: boolean;
  onToggle: () => void;
  onNew: () => void;
  onClose: () => void;
  // The thread list.
  children: ReactNode;
}) {
  return (
    <li className="project-group list-none" data-project={group.name}>
      <div className="group/proj flex h-7 items-center gap-1 rounded-md pr-1">
        <button
          type="button"
          className="flex h-7 min-w-0 flex-1 cursor-pointer items-center gap-1.5 rounded-md pl-2 text-left font-semibold text-2xs text-faint outline-none transition-colors hover:text-muted-foreground focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={!shut}
          aria-label={`${group.name}: ${count} ${count === 1 ? "thread" : "threads"}`}
          onClick={onToggle}
        >
          {shut ? <ChevronRight className="size-3 shrink-0" /> : <ChevronDown className="size-3 shrink-0" />}
          {group.kind === "paired" && <Laptop className="size-3 shrink-0" />}
          <span className={cn("min-w-0 truncate", current && "text-muted-foreground")}>{group.name}</span>
          <span className="shrink-0 font-mono text-3xs">{count}</span>
        </button>
        <span className="flex items-center opacity-0 transition-opacity group-focus-within/proj:opacity-100 group-hover/proj:opacity-100">
          <Button variant="ghost-muted" size="icon-xs" aria-label={`New thread in ${group.name}`} title="New thread" onClick={onNew}>
            <SquarePen />
          </Button>
          <Button variant="ghost-muted" size="icon-xs" aria-label={`Close ${group.name}`} title="Close project" onClick={onClose}>
            <X />
          </Button>
        </span>
      </div>
      {!shut && (
        <ul className="flex flex-col gap-px pt-0.5 pb-3">
          {children}
          {count === 0 && (
            <li className="list-none">
              <button
                type="button"
                className="flex h-8 w-full cursor-pointer items-center gap-2 rounded-md px-2.5 text-left text-muted-foreground/70 text-xs hover:bg-sidebar-row-hover hover:text-foreground"
                onClick={onNew}
              >
                <SquarePen className="size-3.5" /> Start a thread
              </button>
            </li>
          )}
        </ul>
      )}
    </li>
  );
}

// "Shared with you", under the projects: one row per room, with the
// teammate and your role in it.
export function SharedGroup({ shared, open, onOpen }: { shared: SharedEntry[]; open: string | null; onOpen: (peer: string, t: TeamThread) => void }) {
  return (
    <li className="shared-group list-none pt-1 pb-3">
      <div className="flex h-7 items-center gap-1.5 pl-2 font-semibold text-2xs text-faint">Shared with you</div>
      <ul className="flex flex-col gap-px">
        {shared.map(({ peer, thread }) => {
          const active = open === `${peer}/${thread.id}`;
          return (
            <li key={`${peer}/${thread.id}`} className="list-none" data-shared={`${peer}/${thread.id}`}>
              <button
                type="button"
                aria-current={active ? "page" : undefined}
                className={cn(
                  "flex h-9 w-full cursor-pointer items-center gap-2 rounded-md px-2.5 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
                  active ? "bg-sidebar-row-active text-foreground inset-shadow-hl" : "text-foreground/90 hover:bg-sidebar-row-hover",
                )}
                onClick={() => onOpen(peer, thread)}
              >
                <span className="min-w-0 flex-1 truncate text-sm">{thread.title || thread.id}</span>
                <span className="shrink-0 font-mono text-2xs text-faint">
                  {peer} · {thread.role}
                </span>
              </button>
            </li>
          );
        })}
      </ul>
    </li>
  );
}

// The recent folders that are not open, to open again.
export function RecentProjects({ projects, onProject }: { projects: ProjectEntry[]; onProject: (key: string) => void }) {
  return (
    <li className="list-none pt-3">
      <div className="px-2 pb-1 font-semibold text-2xs text-faint">Recent projects</div>
      <ul className="flex flex-col">
        {projects.slice(0, 6).map((p) => (
          <li key={p.key} className="list-none">
            <button
              type="button"
              className="recent-project flex h-8 w-full cursor-pointer items-center gap-2 rounded-md px-2.5 text-left text-muted-foreground text-sm hover:bg-sidebar-row-hover hover:text-foreground"
              title={p.detail}
              onClick={() => onProject(p.key)}
            >
              <Folder className="size-4 shrink-0" />
              <span className="truncate">{p.name}</span>
            </button>
          </li>
        ))}
      </ul>
    </li>
  );
}

// The project filter, after T3 Code's "All projects" control: the threads
// of every open project or of one, the projects to open and close, and the
// ways to add one.
export function ProjectFilter({
  filter,
  projects,
  onFilter,
  onProject,
  onCloseProject,
  onAddProject,
  onPair,
}: {
  filter: string | null;
  projects: ProjectEntry[];
  onFilter: (source: string | null) => void;
  onProject: (source: string) => void;
  onCloseProject: (source: string) => void;
  onAddProject: () => void;
  onPair: () => void;
}) {
  const [open, setOpen] = useState(false);
  const openOnes = projects.filter((p) => p.open);
  const rest = projects.filter((p) => !p.open);
  const shown = projects.find((p) => p.key === filter);
  const icon = (p: ProjectEntry) => (p.kind === "folder" ? <Folder className="mt-0.5" /> : <Laptop className="mt-0.5" />);
  const run = (f: () => void) => () => {
    setOpen(false);
    f();
  };
  const detail = (p: ProjectEntry) => (
    <span className="flex min-w-0 flex-1 flex-col">
      <span className="truncate">{p.name}</span>
      <span className="truncate text-2xs text-muted-foreground">{p.detail}</span>
    </span>
  );
  return (
    <div className="flex items-center gap-1">
      <Popover
        open={open}
        onClose={() => setOpen(false)}
        label="Projects"
        className="w-72"
        trigger={
          <button
            type="button"
            className="project-filter flex h-8 min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-md px-2 text-left font-medium text-muted-foreground text-sm hover:bg-sidebar-row-hover hover:text-foreground"
            aria-expanded={open}
            aria-label="Projects"
            onClick={() => setOpen((o) => !o)}
          >
            <Folder className="size-4 shrink-0" />
            <span className="min-w-0 flex-1 truncate text-foreground">{shown === undefined ? "All projects" : shown.name}</span>
            <ChevronDown className="size-3.5 shrink-0" />
          </button>
        }
      >
        <MenuItem onClick={run(() => onFilter(null))}>
          <Folder />
          <span className="flex-1">All projects</span>
          {filter === null && <Check />}
        </MenuItem>
        {openOnes.length > 0 && <MenuLabel>Open</MenuLabel>}
        {openOnes.map((p) => (
          <div key={p.key} className="group flex items-center">
            <MenuItem className="flex-1 items-start" onClick={run(() => onFilter(p.key))}>
              {icon(p)}
              {detail(p)}
              {filter === p.key && <Check className="mt-0.5" />}
            </MenuItem>
            <button
              type="button"
              aria-label={`Close ${p.name}`}
              title="Close project"
              className="mr-1 grid size-6 shrink-0 cursor-pointer place-items-center rounded text-muted-foreground opacity-0 outline-none hover:bg-hover hover:text-foreground focus-visible:opacity-100 focus-visible:ring-2 focus-visible:ring-ring group-focus-within:opacity-100 group-hover:opacity-100"
              onClick={run(() => {
                if (filter === p.key) onFilter(null);
                onCloseProject(p.key);
              })}
            >
              <X className="size-3.5" />
            </button>
          </div>
        ))}
        {rest.length > 0 && (
          <>
            <MenuSeparator />
            <MenuLabel>Recent</MenuLabel>
            {rest.map((p) => (
              <MenuItem key={p.key} className="items-start" onClick={run(() => onProject(p.key))}>
                {icon(p)}
                {detail(p)}
              </MenuItem>
            ))}
          </>
        )}
        <MenuSeparator />
        <MenuItem onClick={run(onAddProject)}>
          <FolderPlus /> Add project…
        </MenuItem>
        <MenuItem onClick={run(onPair)}>
          <Unplug /> Pair with a Deck…
        </MenuItem>
      </Popover>
      <Button variant="ghost-muted" size="icon-sm" aria-label="Add project" title="Add project" onClick={onAddProject}>
        <FolderPlus />
      </Button>
    </div>
  );
}
