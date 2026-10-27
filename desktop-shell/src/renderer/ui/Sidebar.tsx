import type { Session, Theme } from "@web/protocol";
import type { Link } from "@web/state";
import { sessionTitle } from "@web/state";
import { toMillis } from "@web/time";
import { ArrowLeft, Inbox as InboxIcon, PanelLeft, PanelRight, Search, SquarePen } from "./icons";
import { type MutableRefObject, useEffect, useRef, useState } from "react";
import type { ArchiveOffer, ThreadChoice } from "../../shared/bridge";
import { useRooms } from "../appState";
import { ARCHIVE_UNDO_MS, archiving, useArchiving } from "../archive";
import { byAttention } from "../attention";
import { type DeckClient, serves } from "../client";
import type { TeamThread } from "../crew";
import { cn } from "../lib/cn";
import { clampSidebar, SIDEBAR_WIDTH, type SidebarSide, setPrefs, usePrefs } from "../prefs";
import { EMPTY_ROOM, useRoomMetas } from "../roomMeta";
import { kindsBySession, sameKinds, useAppSlice } from "../select";
import { SETTLE_AFTER_MS } from "../threads";
import { SETTINGS_SECTIONS, type SettingsSection } from "../ui";
import { Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { DeckMark } from "./kit/mark";

import { modKey } from "./keys";
import { navRow, ProjectFilter, ProjectGroup, RecentProjects, SharedGroup, type SharedEntry, type SidebarItem, ThreadList } from "./ProjectGroup";
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

// Deck's mark (the app icon's prompt), then the name. No tile; type does it.
function Brand() {
  return (
    <span className="flex min-w-0 items-center gap-1.5 font-semibold text-sm tracking-tight">
      <DeckMark className="h-3 text-foreground" />
      Deck
    </span>
  );
}

export function Sidebar({ data, actions, order }: { data: SidebarData; actions: SidebarActions; order: MutableRefObject<RowRef[]> }) {
  const { project, source, sessions, openId, unread, settings, inbox, projects, theme } = data;
  // Threads under their project folders (T3 Code's projects), or one list
  // across projects, newest first; card or compact rows. On the left or the
  // right of the window, as wide as the person dragged it.
  const { grouping, rows, sidebarSide, sidebarWidth } = usePrefs();
  // On a Mac the traffic lights sit in the window's top left corner.
  const lights = data.mac && sidebarSide === "left";
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
    const unarchive = serves(c, "UnarchiveSession") ? () => c.unarchiveSession?.(x.s.id) ?? Promise.resolve(false) : undefined;
    archiving.schedule(key, () => c.archiveSession?.(x.s.id) ?? Promise.resolve(false), unarchive);
    actionToast(data.c.store, `Archived "${sessionTitle(x.s)}".`, { label: "Undo", ms: ARCHIVE_UNDO_MS, run: () => archiving.undo(key) });
  };
  // An archived room comes back from the fold, when its Deck can do it.
  const unarchiveOf = (x: SidebarItem) => {
    const c = clientOf(x);
    return metaOf(x).archived && serves(c, "UnarchiveSession") ? () => void c.unarchiveSession?.(x.s.id) : undefined;
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
    const offer: ArchiveOffer = unarchiveOf(x) !== undefined ? "archived" : archiveOffer(x);
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
        onUnarchive={unarchiveOf(x)}
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
      estimate={rows === "compact" ? 34 : grouped ? 60 : 80}
    />
  );
  const recentClosed = projects.filter((p) => !p.open && p.kind === "folder");
  useEffect(() => {
    if (settings === null) order.current = shownRows;
  });
  return (
    // Above the room's sheet, so the project menu can open over it.
    <nav className="sidebar relative z-10 flex w-(--sidebar-w) shrink-0 flex-col rounded-(--panel-radius)" aria-label="Threads" data-yolo-region="">
      {/* The glass is a layer under the sidebar's parts, not the sidebar:
          a backdrop filter would hold the menus' own glass to it. Each part
          is positioned, so it paints over the layer. */}
      <div aria-hidden className="liquid absolute inset-0 rounded-(--panel-radius)" />
      {/* The first row is the title bar's: on a Mac the traffic lights sit
          at (18, 19) in the window, so a 36px row centres them. It stays
          36px in a compact window. */}
      <header className={cn("drag relative flex shrink-0 items-center gap-2 pr-2", data.mac ? "h-[36px]" : "h-11", lights ? "pl-[72px]" : "pl-3.5")}>
        <Brand />
        <span className="ml-auto flex items-center">
          <HeaderButton label="Hide the sidebar" onClick={actions.onCollapse}>
            {sidebarSide === "left" ? <PanelLeft /> : <PanelRight />}
          </HeaderButton>
          <HeaderButton label="New thread" onClick={actions.onNew}>
            <SquarePen />
          </HeaderButton>
        </span>
      </header>
      {settings !== null ? (
        <div className="relative flex min-h-0 flex-1 flex-col gap-0.5 px-2 pt-1">
          <button type="button" className={navRow(false)} onClick={() => actions.onSettings(null)}>
            <ArrowLeft />
            Back to threads
          </button>
          <div className="px-2.5 pt-4 pb-1 font-semibold text-2xs text-faint">Settings</div>
          <ul className="flex flex-col gap-0.5" aria-label="Settings sections">
            {SETTINGS_SECTIONS.map((x) => (
              <li key={x.section}>
                <button
                  type="button"
                  aria-current={settings === x.section ? "page" : undefined}
                  className={navRow(settings === x.section)}
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
          <div className="relative z-10 flex flex-col gap-0.5 px-2 pt-1 pb-2">
            <button type="button" className={navRow(false)} onClick={actions.onPalette}>
              <Search />
              Search
              <Kbd className="ml-auto border-0 bg-transparent px-0 font-sans text-2xs text-faint">{modKey(data.mac)}K</Kbd>
            </button>
            {/* What waits on you: the count in the warning's colour, no badge. */}
            <button type="button" aria-current={inbox ? "page" : undefined} className={cn("inbox-entry", navRow(inbox))} onClick={actions.onInbox}>
              <InboxIcon weight={inbox ? "fill" : "regular"} />
              Inbox
              {data.inboxCount > 0 && <span className="inbox-count ml-auto font-medium text-2xs text-warning-foreground tabular-nums">{data.inboxCount}</span>}
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
          <ul ref={scroller} className="sessions relative flex min-h-0 flex-1 flex-col gap-1 overflow-y-auto px-2 pt-0.5 pb-2">
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
        onDisconnect={actions.onDisconnect}
      />
      <Grip side={sidebarSide} width={sidebarWidth} />
    </nav>
  );
}

// How far across the window the grip goes before the sidebar moves to the
// other side, as a share of the window's width.
const ACROSS = 0.6;
const STEP = 16;

// The sidebar's inner edge, in the gutter beside the room: drag it to
// resize the sidebar, or across most of the window to move the sidebar to
// the other side (a dashed outline shows where it lands). The arrow keys
// resize it too, and a double click gives back the default width.
function Grip({ side, width }: { side: SidebarSide; width: number }) {
  const [across, setAcross] = useState(false);
  const grow = side === "left" ? "ArrowRight" : "ArrowLeft";
  const shrink = side === "left" ? "ArrowLeft" : "ArrowRight";
  const onPointerDown = (e: React.PointerEvent<HTMLDivElement>) => {
    const nav = e.currentTarget.closest("nav");
    if (e.button !== 0 || nav === null) return;
    e.preventDefault();
    const grip = e.currentTarget;
    grip.setPointerCapture(e.pointerId);
    // The sidebar's outer edge stays put; the width follows the pointer.
    const box = nav.getBoundingClientRect();
    const root = document.documentElement;
    let w = width;
    let over = false;
    const move = (ev: PointerEvent) => {
      const x = side === "left" ? ev.clientX : window.innerWidth - ev.clientX;
      over = x > window.innerWidth * ACROSS;
      setAcross(over);
      // Past the widest it goes, the drag is a move: the width it had stays.
      const to = side === "left" ? ev.clientX - box.left : box.right - ev.clientX;
      w = to > SIDEBAR_WIDTH.max + STEP * 3 ? width : clampSidebar(to);
      root.style.setProperty("--sidebar-w", `${w}px`);
    };
    const up = () => {
      grip.removeEventListener("pointermove", move);
      grip.removeEventListener("pointerup", up);
      grip.removeEventListener("pointercancel", up);
      setAcross(false);
      setPrefs(over ? { sidebarSide: side === "left" ? "right" : "left", sidebarWidth: width } : { sidebarWidth: w });
      root.style.setProperty("--sidebar-w", `${over ? width : w}px`);
    };
    grip.addEventListener("pointermove", move);
    grip.addEventListener("pointerup", up);
    grip.addEventListener("pointercancel", up);
  };
  return (
    <>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label="Resize the sidebar"
        aria-valuemin={SIDEBAR_WIDTH.min}
        aria-valuemax={SIDEBAR_WIDTH.max}
        aria-valuenow={width}
        tabIndex={0}
        title="Drag to resize, or across the window to move the sidebar"
        className={cn(
          "no-drag group/grip absolute inset-y-4 z-20 flex w-2.5 cursor-col-resize justify-center outline-none",
          side === "left" ? "-right-[7px]" : "-left-[7px]",
        )}
        onPointerDown={onPointerDown}
        onDoubleClick={() => setPrefs({ sidebarWidth: SIDEBAR_WIDTH.default })}
        onKeyDown={(e) => {
          if (e.key !== grow && e.key !== shrink) return;
          e.preventDefault();
          setPrefs({ sidebarWidth: clampSidebar(width + (e.key === grow ? STEP : -STEP)) });
        }}
      >
        <span
          aria-hidden
          className="h-full w-0.5 rounded-full bg-primary opacity-0 transition-opacity duration-150 group-hover/grip:opacity-60 group-focus-visible/grip:opacity-100 group-active/grip:opacity-100"
        />
      </div>
      {across && (
        <div
          aria-hidden
          className={cn(
            "pointer-events-none fixed inset-y-2 z-50 grid w-(--sidebar-w) place-items-center rounded-(--panel-radius) border-2 border-primary/70 border-dashed bg-primary/10 text-primary text-xs",
            side === "left" ? "right-2" : "left-2",
          )}
        >
          Move the sidebar here
        </div>
      )}
    </>
  );
}
