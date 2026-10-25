import type { Session, Theme } from "@web/protocol";
import type { Link } from "@web/state";
import { sessionTitle } from "@web/state";
import { toMillis } from "@web/time";
import { ArrowLeft, Inbox as InboxIcon, PanelLeft, Search, SquarePen, TerminalIcon } from "lucide-react";
import { type MutableRefObject, useEffect, useRef, useState } from "react";
import type { ArchiveOffer, ThreadChoice } from "../../shared/bridge";
import { useRooms } from "../appState";
import { ARCHIVE_UNDO_MS, archiving, useArchiving } from "../archive";
import { byAttention } from "../attention";
import { type DeckClient, serves } from "../client";
import type { TeamThread } from "../crew";
import { cn } from "../lib/cn";
import { usePrefs } from "../prefs";
import { EMPTY_ROOM, useRoomMetas } from "../roomMeta";
import { kindsBySession, sameKinds, useAppSlice } from "../select";
import { SETTLE_AFTER_MS } from "../threads";
import { SETTINGS_SECTIONS, type SettingsSection } from "../ui";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";

import { modKey } from "./keys";
import { ProjectFilter, ProjectGroup, RecentProjects, SharedGroup, type SharedEntry, type SidebarItem, ThreadList } from "./ProjectGroup";
import { SidebarFooter } from "./SidebarFooter";
import { ThreadRow } from "./ThreadRow";
import { actionToast } from "./Toasts";

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

// Where a row is in the sidebar, for Ctrl/Cmd+1 to 9.
export interface RowRef {
  source: string;
  id: string;
}

// What the sidebar shows.
export interface SidebarData {
  project: string;
  // The current project's source, and the threads of the other open ones.
  source: string;
  foreign: ForeignThread[];
  // The current project's client: the kinds that wrote in each thread.
  c: DeckClient;
  link: Link;
  sessions: Session[];
  openId: string | null;
  unread: Record<string, true>;
  mac: boolean;
  theme: Theme | null;
  projects: ProjectEntry[];
  settings: SettingsSection | null;
  // A downloaded update that waits for a restart.
  update: { version: string; onRestart: () => void } | null;
  usage: boolean;
  team: boolean;
  // The inbox page is open; how many items wait in it.
  inbox: boolean;
  inboxCount: number;
  // Ctrl/Cmd is held: rows show their digit.
  digits: boolean;
  // Rooms teammates share with you, the one open ("peer/thread"), and how
  // many teammates are online (null without a team link).
  shared: SharedEntry[];
  openShared: string | null;
  online: number | null;
}

// What the sidebar does. Rows call these through a ref, so a new handler
// does not render every row.
export interface SidebarActions {
  onOpen: (id: string) => void;
  onNew: () => void;
  onRename: (id: string, title: string) => void;
  onDelete: (id: string) => void;
  onThreadMenu: (id: string, title: string, archive: ArchiveOffer) => Promise<ThreadChoice>;
  onForeign: (source: string, id: string) => void;
  // A new thread in a project, by its source.
  onNewIn: (source: string) => void;
  onProject: (key: string) => void;
  onAddProject: () => void;
  onCloseProject: (source: string) => void;
  // The screen to add a folder or pair with a Deck.
  onConnect: () => void;
  onDisconnect: () => void;
  onPalette: () => void;
  onSettings: (s: SettingsSection | null) => void;
  onUsage: () => void;
  onTeam: () => void;
  onInbox: () => void;
  onShared: (peer: string, thread: TeamThread) => void;
  onCollapse: () => void;
}

const NO_KINDS: string[] = [];

function HeaderButton({ label, onClick, children }: { label: string; onClick: () => void; children: React.ReactNode }) {
  return (
    <Button variant="ghost-muted" size="icon-sm" aria-label={label} title={label} onClick={onClick}>
      {children}
    </Button>
  );
}

export function Sidebar({ data, actions, order }: { data: SidebarData; actions: SidebarActions; order: MutableRefObject<RowRef[]> }) {
  const { project, source, sessions, openId, unread, settings, inbox, projects, theme } = data;
  // Threads under their project folders (T3 Code's projects), or one list
  // across projects, newest first; card or compact rows.
  const { grouping, rows } = usePrefs();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);
  // The kinds that wrote in each thread, for the row's index.
  const kinds = useAppSlice(data.c.store, kindsBySession, sameKinds);
  const [showSettled, setShowSettled] = useState<Record<string, boolean>>({});
  const [showArchived, setShowArchived] = useState<Record<string, boolean>>({});
  const [collapsed, setCollapsed] = useState<Record<string, boolean>>({});
  const [filter, setFilter] = useState<string | null>(null);
  const latest = useRef(actions);
  latest.current = actions;
  const scroller = useRef<HTMLUListElement>(null);
  const rooms = useRooms();
  // What each room's Deck says of it beyond the session (branch, archived),
  // and the rooms that wait out their archive Undo.
  const metas = useRoomMetas(data.c.rooms);
  const waiting = useArchiving();
  const clientOf = (x: SidebarItem) => x.foreign?.c ?? data.c;
  const metaOf = (x: SidebarItem) => (x.foreign === null ? metas.get(x.s.id) : x.foreign.c.rooms?.get(x.s.id)) ?? EMPTY_ROOM;
  const isArchived = (x: SidebarItem) => metaOf(x).archived || waiting.has(`${x.source}/${x.s.id}`);
  // Never for a room whose agents work.
  const archiveOffer = (x: SidebarItem): ArchiveOffer =>
    isArchived(x) || !serves(clientOf(x), "ArchiveSession") ? "none" : x.s.busy || x.status?.state === "working" ? "busy" : "on";
  const archive = (x: SidebarItem) => {
    const c = clientOf(x);
    const key = `${x.source}/${x.s.id}`;
    archiving.schedule(key, () => c.archiveSession?.(x.s.id) ?? Promise.resolve(false));
    actionToast(data.c.store, `Archived "${sessionTitle(x.s)}".`, { label: "Undo", ms: ARCHIVE_UNDO_MS, run: () => archiving.undo(key) });
  };
  // Every open project's threads, as T3 Code lists them: a room that needs
  // the person, finished or overlaps comes first, then the newest.
  const items: SidebarItem[] = [
    ...sessions.map((s) => ({ s, source, project: project || "Deck", foreign: null })),
    ...data.foreign.map((f) => ({ s: f.s, source: f.source, project: f.project, foreign: f })),
  ]
    .filter((x) => filter === null || x.source === filter)
    .map((x) => ({ ...x, status: rooms[x.source]?.[x.s.id] }))
    .sort(byAttention);
  const split = (list: SidebarItem[]) => {
    const live: SidebarItem[] = [];
    const settled: SidebarItem[] = [];
    const archived: SidebarItem[] = [];
    for (const x of list) {
      if (isArchived(x)) {
        archived.push(x);
        continue;
      }
      const mine = x.foreign === null;
      const holds = (x.status !== undefined && x.status.state !== "idle") || x.s.busy || x.s.pending > 0 || (mine && (unread[x.s.id] === true || x.s.id === openId));
      (now - toMillis(x.s.updated_at) > SETTLE_AFTER_MS && !holds ? settled : live).push(x);
    }
    return { live, settled, archived };
  };
  const grouped = grouping === "projects";
  // The open projects, the current one first, then the others by name.
  const current = projects.find((p) => p.key === source);
  const groups = [
    ...(current === undefined ? [{ key: source, name: project || "Deck", detail: "", kind: "folder" as const, active: true, open: true }] : [current]),
    ...projects.filter((p) => p.open && p.key !== source).sort((a, b) => a.name.localeCompare(b.name)),
  ].filter((p) => filter === null || p.key === filter);
  const lists = (grouped ? groups.map((g) => g.key) : ["all"]).map((key) => {
    const mine = grouped ? items.filter((x) => x.source === key) : items;
    const parts = split(mine);
    return { key, count: mine.length - parts.archived.length, ...parts };
  });
  // The rows in the order they show, for Ctrl/Cmd+1 to 9: a virtual list
  // leaves rows out of the page, so the order comes from the lists.
  const shownRows: RowRef[] = [];
  for (const l of lists) {
    if (grouped && collapsed[l.key] === true) continue;
    for (const x of [...l.live, ...(showSettled[l.key] === true ? l.settled : [])]) shownRows.push({ source: x.source, id: x.s.id });
  }
  const digitOf = new Map(shownRows.slice(0, 9).map((r, i) => [`${r.source}/${r.id}`, i + 1]));
  const row = (x: SidebarItem) => {
    const s = x.s;
    const f = x.foreign;
    const offer = archiveOffer(x);
    return (
      <ThreadRow
        key={`${x.source}/${s.id}`}
        s={s}
        project={x.project}
        active={f === null && s.id === openId && settings === null && !inbox}
        unread={f === null && unread[s.id] === true}
        status={x.status}
        showLabel={f === null}
        digit={data.digits ? (digitOf.get(`${x.source}/${s.id}`) ?? null) : null}
        now={now}
        variant={rows}
        kinds={f === null ? (kinds[s.id] ?? NO_KINDS) : NO_KINDS}
        theme={theme}
        hideProject={grouped}
        branch={metaOf(x).branch}
        archive={offer}
        onOpen={() => (f === null ? latest.current.onOpen(s.id) : latest.current.onForeign(f.source, s.id))}
        onRename={(t) => void (f === null ? latest.current.onRename(s.id, t) : f.c.renameSession(s.id, t))}
        onDelete={() => void (f === null ? latest.current.onDelete(s.id) : f.c.deleteSession(s.id))}
        onArchive={() => archive(x)}
        onMenu={() => latest.current.onThreadMenu(s.id, sessionTitle(s), offer)}
      />
    );
  };
  const threads = (l: (typeof lists)[number]) => (
    <ThreadList
      live={l.live}
      settled={l.settled}
      shown={showSettled[l.key] === true}
      onToggle={() => setShowSettled((v) => ({ ...v, [l.key]: v[l.key] !== true }))}
      archived={l.archived}
      archivedShown={showArchived[l.key] === true}
      onToggleArchived={() => setShowArchived((v) => ({ ...v, [l.key]: v[l.key] !== true }))}
      row={row}
      scroller={scroller}
      estimate={rows === "compact" ? 37 : grouped ? 59 : 79}
    />
  );
  const recentClosed = projects.filter((p) => !p.open && p.kind === "folder");
  useEffect(() => {
    if (settings === null) order.current = shownRows;
  });
  return (
    <nav className="sidebar flex w-64 shrink-0 flex-col bg-sidebar" aria-label="Threads">
      <header className={cn("brand-glow drag flex h-12 shrink-0 items-center gap-2 px-3", data.mac && "pl-20")}>
        <Button variant="ghost-muted" size="icon-sm" aria-label="Hide the sidebar" title="Hide the sidebar" onClick={actions.onCollapse}>
          <PanelLeft />
        </Button>
        {/* The terminal prompt is the app's own suit. */}
        <span className="inline-flex items-center gap-1.5 font-medium text-sm tracking-tight">
          <TerminalIcon aria-hidden className="size-3.5 text-foreground" />
          <span>Deck</span>
          <span className="text-muted-foreground">Code</span>
        </span>
      </header>
      {settings !== null ? (
        <div className="flex min-h-0 flex-1 flex-col gap-1 px-2 pt-2">
          <button
            type="button"
            className="flex h-8 cursor-pointer items-center gap-2 rounded-md px-2 font-medium text-muted-foreground text-sm hover:bg-sidebar-row-hover hover:text-foreground"
            onClick={() => actions.onSettings(null)}
          >
            <ArrowLeft className="size-4" />
            Back to threads
          </button>
          <div className="px-2.5 pt-3 pb-1 font-medium text-2xs text-faint">Settings</div>
          <ul className="flex flex-col gap-px" aria-label="Settings sections">
            {SETTINGS_SECTIONS.map((x) => (
              <li key={x.section}>
                <button
                  type="button"
                  aria-current={settings === x.section ? "page" : undefined}
                  className={cn(
                    "flex h-8 w-full cursor-pointer items-center rounded-md px-2.5 text-left text-sm",
                    settings === x.section
                      ? "bg-sidebar-row-active text-foreground inset-shadow-hl"
                      : "text-muted-foreground hover:bg-sidebar-row-hover hover:text-foreground",
                  )}
                  onClick={() => actions.onSettings(x.section)}
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
                onClick={actions.onPalette}
              >
                <Search className="size-4 shrink-0" />
                Search
                <Kbd className="ml-auto bg-transparent">{modKey(data.mac)}K</Kbd>
              </button>
              <HeaderButton label="New thread" onClick={actions.onNew}>
                <SquarePen />
              </HeaderButton>
            </div>
            <button
              type="button"
              aria-current={inbox ? "page" : undefined}
              className={cn(
                "inbox-entry flex h-8 min-w-0 cursor-pointer items-center gap-2 rounded-md px-2 text-left font-medium text-sm",
                inbox ? "bg-sidebar-row-active text-foreground inset-shadow-hl" : "text-muted-foreground hover:bg-sidebar-row-hover hover:text-foreground",
              )}
              onClick={actions.onInbox}
            >
              <InboxIcon className="size-4 shrink-0" />
              Inbox
              {data.inboxCount > 0 && (
                <span className="inbox-count ml-auto rounded-full bg-selected px-1.5 font-mono text-2xs text-foreground tabular-nums">{data.inboxCount}</span>
              )}
            </button>
            <ProjectFilter
              filter={filter}
              projects={projects}
              onFilter={setFilter}
              onProject={actions.onProject}
              onCloseProject={actions.onCloseProject}
              onAddProject={actions.onAddProject}
              onPair={actions.onConnect}
            />
          </div>
          <ul ref={scroller} className="sessions flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-2 pt-1 pb-2">
            {grouped
              ? groups.map((g, i) => (
                  <ProjectGroup
                    key={g.key}
                    group={g}
                    count={lists[i]!.count}
                    current={g.key === source}
                    shut={collapsed[g.key] === true}
                    onToggle={() => setCollapsed((c) => ({ ...c, [g.key]: c[g.key] !== true }))}
                    onNew={() => actions.onNewIn(g.key)}
                    onClose={() => actions.onCloseProject(g.key)}
                  >
                    {threads(lists[i]!)}
                  </ProjectGroup>
                ))
              : threads(lists[0]!)}
            {data.shared.length > 0 && <SharedGroup shared={data.shared} open={data.openShared} onOpen={actions.onShared} />}
            {grouped && filter === null && recentClosed.length > 0 && <RecentProjects projects={recentClosed} onProject={actions.onProject} />}
            {!grouped && items.length === 0 && (
              <li className="flex list-none flex-col items-center gap-2 px-2 py-6 text-center text-muted-foreground/60 text-xs">No threads yet</li>
            )}
          </ul>
        </>
      )}
      <SidebarFooter
        link={data.link}
        settings={settings}
        usage={data.usage}
        team={data.team}
        online={data.online}
        update={data.update}
        onSettings={actions.onSettings}
        onUsage={actions.onUsage}
        onTeam={actions.onTeam}
        onPalette={actions.onPalette}
        onDisconnect={actions.onDisconnect}
      />
    </nav>
  );
}
