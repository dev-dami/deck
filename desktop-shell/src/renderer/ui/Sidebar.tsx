import type { Session, Theme } from "@web/protocol";
import type { Link } from "@web/state";
import { sessionTitle } from "@web/state";
import { kindColour, kindMark } from "@web/theme";
import { relativeTime, toMillis } from "@web/time";
import {
  ArrowLeft,
  BarChart3,
  Check,
  ChevronDown,
  ChevronRight,
  CircleCheck,
  CircleDashed,
  Command,
  Folder,
  FolderPlus,
  Laptop,
  type LucideIcon,
  PanelLeft,
  Search,
  Settings,
  ShieldQuestion,
  SquarePen,
  Trash2,
  Unplug,
  X,
} from "lucide-react";
import { useState } from "react";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";
import type { RowStyle } from "../prefs";
import { SETTLE_AFTER_MS, type ThreadStatus, threadStatus } from "../threads";
import { SETTINGS_SECTIONS, type SettingsSection } from "../ui";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { MenuItem, MenuLabel, MenuSeparator, Popover } from "./kit/menu";
import { modKey } from "./keys";

// A project the sidebar knows: a recent folder or a paired Deck. key is its
// source ("folder:<dir>", "paired:<id>").
export interface ProjectEntry {
  key: string;
  name: string;
  detail: string;
  kind: "folder" | "paired";
  // The current project: its thread is open.
  active: boolean;
  open: boolean;
}

// A thread of another open project, with that project's client.
export interface ForeignThread {
  source: string;
  project: string;
  s: Session;
  c: DeckClient;
}

const STATUS: Record<Exclude<ThreadStatus, null>, { label: string; icon: LucideIcon; className: string }> = {
  working: { label: "Working", icon: CircleDashed, className: "text-info-foreground" },
  approval: { label: "Approval", icon: ShieldQuestion, className: "text-warning-foreground" },
  done: { label: "Done", icon: CircleCheck, className: "text-success-foreground" },
};

// The agents that wrote in a thread, as T3 Code stacks provider icons.
function KindStack({ kinds, theme }: { kinds: string[]; theme: Theme | null }) {
  if (kinds.length === 0) return null;
  return (
    <span className="ml-auto inline-flex shrink-0 items-center -space-x-0.5">
      {kinds.slice(0, 3).map((k) => (
        <span
          key={k}
          className="grid size-4 place-items-center rounded-full bg-sidebar font-mono text-[10px] ring-1 ring-sidebar"
          style={{ color: kindColour(k) }}
          title={k}
        >
          {kindMark(theme, k) || k.slice(0, 1)}
        </span>
      ))}
    </span>
  );
}

function ThreadRow({
  s,
  project,
  active,
  unread,
  now,
  variant,
  kinds,
  theme,
  onOpen,
  onRename,
  onDelete,
}: {
  s: Session;
  project: string;
  active: boolean;
  unread: boolean;
  now: number;
  variant: RowStyle;
  kinds: string[];
  theme: Theme | null;
  onOpen: () => void;
  onRename: (title: string) => void;
  onDelete: () => void;
}) {
  const [renaming, setRenaming] = useState(false);
  const [confirm, setConfirm] = useState(false);
  const status = threadStatus(s, unread);
  const st = status === null ? null : STATUS[status];
  const recede = !active && status === null;
  const surface = cn(
    "session-main group/sidebar-row relative block w-full cursor-pointer overflow-hidden rounded-md text-left outline-none select-none focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring",
    active
      ? "bg-sidebar-row-active text-foreground"
      : recede
        ? "text-muted-foreground/75 hover:bg-sidebar-row-hover hover:text-foreground"
        : "text-foreground hover:bg-sidebar-row-hover",
  );
  const title = renaming ? (
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
      className="min-w-0 flex-1 rounded-sm border border-input bg-card px-1 font-medium text-sm outline-none focus:border-foreground"
    />
  ) : (
    <span
      className={cn(
        "session-title min-w-0 flex-1 truncate text-sm",
        recede ? "font-normal text-secondary-label" : "font-medium",
        active || unread ? "text-foreground" : !recede && "text-foreground/90",
      )}
    >
      {sessionTitle(s)}
    </span>
  );
  const statusSlot = (
    <span className="relative ml-auto flex h-5 min-w-8 shrink-0 items-center justify-end text-xs">
      <span
        className={cn(
          "flex items-center tabular-nums text-secondary-label transition-opacity group-hover/sidebar-row:pointer-events-none group-hover/sidebar-row:opacity-0",
        )}
      >
        {st !== null ? (
          <span className={cn("inline-flex items-center gap-1 font-medium", st.className)} data-status={status}>
            <st.icon className={cn("size-4 shrink-0", status === "working" && "animate-spin [animation-duration:3s]")} />
            <span role="status">{st.label}</span>
            {status === "approval" && s.pending > 1 && <span>{s.pending}</span>}
          </span>
        ) : (
          relativeTime(s.updated_at, now)
        )}
      </span>
      <span className="pointer-events-none absolute inset-y-0 right-0 flex items-center opacity-0 transition-opacity group-hover/sidebar-row:pointer-events-auto group-hover/sidebar-row:opacity-100">
        {confirm ? (
          <span className="flex items-center gap-1 text-xs">
            <button
              type="button"
              className="rounded px-1 text-destructive-foreground hover:underline"
              onClick={(e) => {
                e.stopPropagation();
                onDelete();
              }}
            >
              Delete
            </button>
            <button
              type="button"
              className="rounded px-1 text-muted-foreground hover:text-foreground"
              onClick={(e) => {
                e.stopPropagation();
                setConfirm(false);
              }}
            >
              Keep
            </button>
          </span>
        ) : (
          <button
            type="button"
            aria-label={`Delete ${sessionTitle(s)}`}
            title="Delete thread"
            className="-mr-1 inline-flex cursor-pointer items-center gap-1 rounded-md px-1.5 text-muted-foreground text-xs hover:text-foreground"
            onClick={(e) => {
              e.stopPropagation();
              setConfirm(true);
            }}
          >
            <Trash2 className="size-3.5" />
          </button>
        )}
      </span>
    </span>
  );
  const common = {
    role: "button" as const,
    tabIndex: 0,
    "aria-label": sessionTitle(s),
    "aria-current": active ? ("page" as const) : undefined,
    onClick: onOpen,
    onDoubleClick: (e: React.MouseEvent) => {
      if ((e.target as HTMLElement).closest("button, input")) return;
      setRenaming(true);
    },
    onKeyDown: (e: React.KeyboardEvent) => {
      if (e.target !== e.currentTarget || (e.key !== "Enter" && e.key !== " ")) return;
      e.preventDefault();
      onOpen();
    },
  };
  if (variant === "compact") {
    return (
      <li className={cn("session list-none", active && "open")} data-session={s.id}>
        <div {...common} className={cn(surface, "flex h-9 items-center gap-2.5 px-2.5")}>
          {title}
          {statusSlot}
        </div>
      </li>
    );
  }
  return (
    <li className={cn("session list-none", active && "open")} data-session={s.id}>
      <div {...common} className={surface}>
        <div className="relative h-[4.875rem] px-2.5 py-2">
          <div className="flex h-5 min-w-0 items-center gap-1.5">
            <Folder className="size-3.5 shrink-0 text-muted-foreground/70" />
            <span className={cn("min-w-0 flex-1 truncate text-secondary-label text-xs", !recede && "font-medium")}>
              {project}
            </span>
            {statusSlot}
          </div>
          <div className="mt-1 flex min-w-0">{title}</div>
          <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-secondary-label text-xs">
            <span className="min-w-0 flex-1 truncate text-muted-foreground/50">
              {s.last_text !== undefined && s.last_text !== "" ? s.last_text : `${s.mode} mode`}
            </span>
            <KindStack kinds={kinds} theme={theme} />
          </div>
        </div>
      </div>
    </li>
  );
}

function HeaderButton({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <Button variant="ghost-muted" size="icon-sm" aria-label={label} title={label} onClick={onClick}>
      {children}
    </Button>
  );
}

// The project filter, after T3 Code's "All projects" control: the threads
// of every open project or of one, the projects to open and close, and the
// ways to add one.
function ProjectFilter({
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
        <MenuItem
          onClick={() => {
            setOpen(false);
            onFilter(null);
          }}
        >
          <Folder />
          <span className="flex-1">All projects</span>
          {filter === null && <Check />}
        </MenuItem>
        {openOnes.length > 0 && <MenuLabel>Open</MenuLabel>}
        {openOnes.map((p) => (
          <div key={p.key} className="group flex items-center">
            <MenuItem
              className="flex-1 items-start"
              onClick={() => {
                setOpen(false);
                onFilter(p.key);
              }}
            >
              {icon(p)}
              <span className="flex min-w-0 flex-1 flex-col">
                <span className="truncate">{p.name}</span>
                <span className="truncate text-2xs text-muted-foreground">{p.detail}</span>
              </span>
              {filter === p.key && <Check className="mt-0.5" />}
            </MenuItem>
            <button
              type="button"
              aria-label={`Close ${p.name}`}
              title="Close project"
              className="mr-1 grid size-6 shrink-0 cursor-pointer place-items-center rounded text-muted-foreground opacity-0 hover:bg-accent hover:text-foreground group-hover:opacity-100"
              onClick={() => {
                setOpen(false);
                if (filter === p.key) onFilter(null);
                onCloseProject(p.key);
              }}
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
              <MenuItem
                key={p.key}
                className="items-start"
                onClick={() => {
                  setOpen(false);
                  onProject(p.key);
                }}
              >
                {icon(p)}
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate">{p.name}</span>
                  <span className="truncate text-2xs text-muted-foreground">{p.detail}</span>
                </span>
              </MenuItem>
            ))}
          </>
        )}
        <MenuSeparator />
        <MenuItem
          onClick={() => {
            setOpen(false);
            onAddProject();
          }}
        >
          <FolderPlus /> Add project…
        </MenuItem>
        <MenuItem
          onClick={() => {
            setOpen(false);
            onPair();
          }}
        >
          <Unplug /> Pair with a Deck…
        </MenuItem>
      </Popover>
      <HeaderButton label="Add project" onClick={onAddProject}>
        <FolderPlus />
      </HeaderButton>
    </div>
  );
}

const LINK_LABEL: Record<Link, string> = { online: "Connected", connecting: "Connecting…", offline: "Offline" };

const linkDot: Record<Link, string> = {
  online: "bg-success",
  connecting: "bg-warning animate-deck-pulse",
  offline: "bg-destructive",
};

export function Sidebar({
  project,
  source,
  foreign,
  onForeign,
  onCloseProject,
  onConnect,
  link,
  sessions,
  openId,
  unread,
  now,
  mac,
  rows,
  theme,
  kindsOf,
  projects,
  settings,
  onOpen,
  onNew,
  onRename,
  onDelete,
  onDisconnect,
  onPalette,
  onSettings,
  onProject,
  onAddProject,
  onCollapse,
  usage,
  onUsage,
}: {
  project: string;
  // The current project's source, and the threads of the other open ones.
  source: string;
  foreign: ForeignThread[];
  onForeign: (source: string, id: string) => void;
  onCloseProject: (source: string) => void;
  // The screen to add a folder or pair with a Deck.
  onConnect: () => void;
  link: Link;
  sessions: Session[];
  openId: string | null;
  unread: Record<string, true>;
  now: number;
  mac: boolean;
  rows: RowStyle;
  theme: Theme | null;
  kindsOf: (id: string) => string[];
  projects: ProjectEntry[];
  settings: SettingsSection | null;
  onOpen: (id: string) => void;
  onNew: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  onDisconnect: () => void;
  onPalette: () => void;
  onSettings: (s: SettingsSection | null) => void;
  onProject: (key: string) => void;
  onAddProject: () => void;
  onCollapse: () => void;
  usage: boolean;
  onUsage: () => void;
}) {
  const [showSettled, setShowSettled] = useState(false);
  const [filter, setFilter] = useState<string | null>(null);
  // Every open project's threads, newest first, as T3 Code lists them.
  type Item = { s: Session; source: string; project: string; foreign: ForeignThread | null };
  const items: Item[] = [
    ...sessions.map((s) => ({ s, source, project: project || "Deck", foreign: null })),
    ...foreign.map((f) => ({ s: f.s, source: f.source, project: f.project, foreign: f })),
  ]
    .filter((x) => filter === null || x.source === filter)
    .sort((a, b) => toMillis(b.s.updated_at) - toMillis(a.s.updated_at));
  const live: Item[] = [];
  const settled: Item[] = [];
  for (const x of items) {
    const mine = x.foreign === null;
    const holds = x.s.busy || x.s.pending > 0 || (mine && (unread[x.s.id] === true || x.s.id === openId));
    (now - toMillis(x.s.updated_at) > SETTLE_AFTER_MS && !holds ? settled : live).push(x);
  }
  const row = (x: Item) => {
    const s = x.s;
    const f = x.foreign;
    return (
      <ThreadRow
        key={`${x.source}/${s.id}`}
        s={s}
        project={x.project}
        active={f === null && s.id === openId && settings === null}
        unread={f === null && unread[s.id] === true}
        now={now}
        variant={rows}
        kinds={f === null ? kindsOf(s.id) : []}
        theme={theme}
        onOpen={() => (f === null ? onOpen(s.id) : onForeign(f.source, s.id))}
        onRename={(t) => void (f === null ? onRename(s.id, t) : f.c.renameSession(s.id, t))}
        onDelete={() => void (f === null ? onDelete(s.id) : f.c.deleteSession(s.id))}
      />
    );
  };
  return (
    <nav className="sidebar flex w-64 shrink-0 flex-col border-r bg-sidebar" aria-label="Threads">
      <header className={cn("brand-glow drag flex h-12 shrink-0 items-center gap-2 px-3", mac && "pl-20")}>
        <Button variant="ghost-muted" size="icon-sm" aria-label="Hide the sidebar" title="Hide the sidebar" onClick={onCollapse}>
          <PanelLeft />
        </Button>
        <span className="inline-flex items-baseline gap-1 font-medium text-sm tracking-tight">
          <span className="font-semibold">Deck</span>
          <span className="text-muted-foreground">Code</span>
        </span>
      </header>
      {settings !== null ? (
        <div className="flex min-h-0 flex-1 flex-col gap-1 px-2 pt-2">
          <button
            type="button"
            className="flex h-8 cursor-pointer items-center gap-2 rounded-md px-2 font-medium text-muted-foreground text-sm hover:bg-sidebar-row-hover hover:text-foreground"
            onClick={() => onSettings(null)}
          >
            <ArrowLeft className="size-4" />
            Back to threads
          </button>
          <div className="px-2 pt-3 pb-1 font-medium text-muted-foreground text-xs">Settings</div>
          <ul className="flex flex-col gap-px" aria-label="Settings sections">
            {SETTINGS_SECTIONS.map((x) => (
              <li key={x.section}>
                <button
                  type="button"
                  aria-current={settings === x.section ? "page" : undefined}
                  className={cn(
                    "flex h-8 w-full cursor-pointer items-center rounded-md px-2.5 text-left text-sm",
                    settings === x.section
                      ? "bg-sidebar-row-active font-medium text-foreground"
                      : "text-muted-foreground hover:bg-sidebar-row-hover hover:text-foreground",
                  )}
                  onClick={() => onSettings(x.section)}
                >
                  {x.label}
                </button>
              </li>
            ))}
          </ul>
        </div>
      ) : (
        <>
          <div className="flex flex-col gap-1 px-2 pt-2 pb-1">
            <div className="flex items-center gap-1">
              <button
                type="button"
                className="flex h-8 min-w-0 flex-1 cursor-pointer items-center gap-2 rounded-md px-2 text-left font-medium text-muted-foreground text-sm hover:bg-sidebar-row-hover hover:text-foreground"
                onClick={onPalette}
              >
                <Search className="size-4 shrink-0" />
                Search
                <Kbd className="ml-auto bg-transparent">{modKey(mac)}K</Kbd>
              </button>
              <HeaderButton label="New thread" onClick={onNew}>
                <SquarePen />
              </HeaderButton>
            </div>
            <ProjectFilter
              filter={filter}
              projects={projects}
              onFilter={setFilter}
              onProject={onProject}
              onCloseProject={onCloseProject}
              onAddProject={onAddProject}
              onPair={onConnect}
            />
          </div>
          <ul className="sessions flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-2 pt-1 pb-2">
            {live.map(row)}
            {settled.length > 0 && (
              <li className="list-none">
                <button
                  type="button"
                  className="mt-2 flex h-7 w-full cursor-pointer items-center gap-2 px-2.5 text-muted-foreground/70 text-xs hover:text-foreground"
                  aria-expanded={showSettled}
                  onClick={() => setShowSettled((v) => !v)}
                >
                  <span>{showSettled ? "Settled" : `Settled (${settled.length})`}</span>
                  <span className="h-px flex-1 bg-border" />
                  {showSettled ? <ChevronDown className="size-3.5" /> : <ChevronRight className="size-3.5" />}
                </button>
              </li>
            )}
            {showSettled && settled.map(row)}
            {items.length === 0 && (
              <li className="flex list-none flex-col items-center gap-2 px-2 py-6 text-center text-muted-foreground/60 text-xs">
                No threads yet
              </li>
            )}
          </ul>
        </>
      )}
      <footer className="side-foot flex items-center gap-0.5 px-2 py-2">
        <HeaderButton label="Settings" onClick={() => onSettings(settings === null ? "general" : null)}>
          <Settings />
        </HeaderButton>
        <Button
          variant="ghost-muted"
          size="icon-sm"
          aria-label="Usage"
          title="Usage"
          aria-pressed={usage}
          onClick={onUsage}
        >
          <BarChart3 />
        </Button>
        <HeaderButton label="Commands" onClick={onPalette}>
          <Command />
        </HeaderButton>
        <HeaderButton label="Close project" onClick={onDisconnect}>
          <Unplug />
        </HeaderButton>
        <span className="link ml-auto flex min-w-0 items-center gap-1.5 px-1 text-2xs text-muted-foreground">
          <span className={cn("dot size-1.5 shrink-0 rounded-full", linkDot[link])} />
          <span className="truncate">{LINK_LABEL[link]}</span>
        </span>
      </footer>
    </nav>
  );
}
