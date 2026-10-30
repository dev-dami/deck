import type { Decision, QuestionAnswer, Session } from "@web/protocol";
import { sessionMode } from "@web/state";
import { type ComponentProps, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DeckBridge } from "../../shared/bridge";
import { dispatchUi, useEngine, useProjectUi } from "../appState";
import type { InboxItem } from "../attention";
import type { DeckClient } from "../client";
import type { OpenProject } from "../openProjects";
import { usePrefs } from "../prefs";
import { gateTeam } from "../teamGate";
import { panelSlice, shallowEqual, shellSlice, useAppSlice } from "../select";
import { isSide } from "../sideChat";
import { PANELS, type Panel, type UiAction } from "../ui";
import { LayoutMenu } from "../layout/LayoutMenu";
import { CONVERSATION, isTerminal, type PaneId, terminalNumber } from "../layout/tree";
import { useRoomLayout } from "../layout/useRoomLayout";
import { type PaneSpec, Workspace } from "../layout/Workspace";
import { ChatCircle, SquareTerminal } from "./icons";
import type { Landing } from "./App";
import { LineComments } from "./DiffView";
import { useGit } from "./Git";
import { Inbox } from "./Inbox";
import { isMac } from "./keys";
import { Palette } from "./Palette";
import { LivePanelPane, PanelBadge, panelAvailable, TAB_ICONS } from "./RightPanel";
import { Room } from "./Room";
import { ProfilesDeck } from "./ProfilesEditor";
import { SettingsPage } from "./Settings";
import { SharedRoom } from "./SharedRoom";
import { type ProjectEntry, Sidebar } from "./Sidebar";
import { TeamPage, useTeam } from "./Team";
import { TerminalPane, TerminalTabExtras, useTerminalShown } from "./Terminal";
import { Toasts } from "./Toasts";
import { useLimitCommands } from "./Limits";
import { UsagePage } from "./Usage";
import { setPreviewTarget, useOpenPorts, useWatchPorts } from "./usePorts";
import { useOpenRoom } from "./useRoom";
import { useForeignThreads, useModDigits, usePaletteRooms, useShellCommands, useShellNav, useUpdate } from "./useShell";
import { useYoloTrigger, YoloFx } from "./YoloFx";

// What App hands each project view: the inbox across projects and how to
// act on it.
export interface ShellAttention {
  items: InboxItem[];
  onOpenRoom: (project: string, room: string) => void;
  onAnswer: (item: InboxItem, d: Decision, answers?: QuestionAnswer[]) => void;
  onContract: (item: InboxItem, accept: boolean) => Promise<string | null>;
}

// Settings takes the whole state but reads only the panel slice.
// The profiles editor in Agents talks to this Deck, and keeps profile
// names clear of the team's people.
function LiveSettings({ c, teammates, ...props }: { c: DeckClient; teammates: readonly string[] } & Omit<ComponentProps<typeof SettingsPage>, "state">) {
  useAppSlice(c.store, panelSlice, shallowEqual);
  const deck = useMemo(() => ({ c, teammates }), [c, teammates]);
  return (
    <ProfilesDeck.Provider value={deck}>
      <SettingsPage {...props} state={c.store.state} />
    </ProfilesDeck.Provider>
  );
}

export interface ShellProps {
  attention: ShellAttention;
  c: DeckClient;
  // The project's key in the app state (appState.ts).
  projectKey: string;
  bridge: DeckBridge;
  // The open project's folder when it is a local one.
  folder: string | null;
  name: string;
  where: string;
  transport: string;
  // The other open projects, whose threads the sidebar lists too.
  others: OpenProject[];
  projects: ProjectEntry[];
  onProject: (source: string, to?: Landing) => Promise<string | null>;
  onAddProject: () => Promise<string | null>;
  onCloseProject: (source: string) => void;
  onDisconnect: () => void;
  onConnect: () => void;
  onWelcome: () => void;
}

// One project's window: the sidebar, the page that shows (inbox, usage,
// team, settings, a shared room or the room), the terminal, the side panel,
// the palette and the toasts. where names the Deck: host:port for a paired
// one, the folder for a local one.
export function Shell(props: ShellProps) {
  const { c, projectKey, bridge, folder, name, where, transport, others, attention, projects } = props;
  const { onProject, onAddProject, onCloseProject, onDisconnect, onConnect, onWelcome } = props;
  const state = useAppSlice(c.store, shellSlice, shallowEqual);
  const prefs = usePrefs();
  const ui = useProjectUi(projectKey);
  const dispatch = useCallback((a: UiAction) => dispatchUi(projectKey, a), [projectKey]);
  const engine = useEngine(projectKey);
  const [sidebar, setSidebar] = useState(true);
  // The file Changes scrolls to, when a link asked for one.
  const [focus, setFocus] = useState<string | null>(null);
  const api = c.project;
  const [terminal, setTerminal] = useTerminalShown(api);
  const git = useGit(api);
  // With the team off (teamGate.ts) nothing of it is polled or shown.
  const polled = useTeam(prefs.team ? api : undefined, 15_000);
  const team = gateTeam(prefs.team, polled.team);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const mac = useMemo(isMac, []);
  const foreign = useForeignThreads(others);
  const digits = useModDigits();
  const update = useUpdate(bridge, c);
  const open: Session | undefined = state.sessions.find((s) => s.id === ui.openId);
  useWatchPorts(c, bridge);
  const yoloPlays = useYoloTrigger(projectKey, open?.id ?? null, sessionMode(open));

  useEffect(() => {
    if (ui.openId === null) c.closeSession();
    else void c.openSession(ui.openId);
  }, [c, ui.openId]);
  // Closing the room or the project closes the preview's page.
  useEffect(() => () => void bridge.previewClose(), [bridge, ui.openId]);

  const focusInput = useCallback(() => requestAnimationFrame(() => inputRef.current?.focus()), []);
  const toast = useCallback((text: string) => c.store.dispatch({ type: "toast.add", kind: "error", text }), [c]);
  const info = useCallback((text: string) => c.store.dispatch({ type: "toast.add", kind: "info", text }), [c]);
  const source = projects.find((p) => p.active)?.key ?? "";
  // A folder is named after itself; a paired Deck by what it says.
  const project = source.startsWith("folder:") ? name : state.server?.project || name;
  const nav = useShellNav({ projectKey, source, dispatch, ui, attention, onProject, focusInput, info, toast });
  const { shared, openSession, newSession, openRoom, openShared, orToast } = nav;
  // Rooms teammates share with you, and how many teammates are online.
  const teamOn = typeof team === "object" && team !== null && team.available ? team : null;
  const teammates = useMemo(() => teamOn?.teammates.map((m) => m.name) ?? [], [teamOn]);
  const r = useOpenRoom({
    c,
    projectKey,
    open,
    state,
    inputRef,
    openSession,
    newSession,
    canShare: api !== undefined && teamOn !== null && teamOn.teammates.length > 0,
    confirmYolo: prefs.confirmYolo,
    refreshGit: git.refresh,
    info,
    toast,
  });

  // The room's layout (layout/): the conversation, the terminals and the
  // side panel's views as panes the person arranges. The side panel's and
  // the terminal's buttons, keys and commands show and hide their panes.
  const { sidebarSide, panelDock } = prefs;
  const panelSide = (sidebarSide === "left") === (panelDock === "beside") ? "left" : "right";
  const ports = useOpenPorts(api);
  // Settings, Usage, Team and the inbox take the place of the room.
  const teamPage = ui.team && prefs.team;
  const away = ui.settings !== null || ui.usage || teamPage || ui.inbox;
  const rl = useRoomLayout({
    project: source,
    side: panelSide,
    api,
    terminal,
    setTerminal,
    panel: ui.panel,
    setPanel: (panel) => dispatch({ type: "panel", panel }),
    panelsOn: shared === null,
    available: panelAvailable(c, bridge, ports.length, ui.panel),
    inputRef,
    keys: !away,
  });
  // Opens a panel, never closes it (the panel's own tabs and the menus).
  const openPanel = (p: Panel, path?: string) => {
    rl.openPanel(p);
    setFocus(path ?? null);
  };
  const togglePanel = rl.togglePanel;
  const toggleTerminal = rl.toggleTerminal;
  const preview = (url: string) => {
    if (api === undefined) return;
    setPreviewTarget(api, url);
    openPanel("preview");
  };
  const addProject = () => orToast(onAddProject());
  // How the layout names and marks each pane.
  const paneSpec = (p: PaneId): PaneSpec => {
    if (p === CONVERSATION) return { label: "Conversation", icon: ChatCircle, keepAlive: true, close: null };
    if (isTerminal(p)) {
      const n = terminalNumber(rl.ctl.layout.root, p);
      const spec: PaneSpec = { label: `Terminal ${n}`, icon: SquareTerminal, keepAlive: true, close: `Close terminal ${n}` };
      return api === undefined ? spec : { ...spec, extras: <TerminalTabExtras project={api} slot={p} n={n} onPreview={preview} /> };
    }
    const panel = p as Panel;
    const label = PANELS.find((x) => x.panel === panel)?.label ?? p;
    const spec: PaneSpec = { label, icon: TAB_ICONS[panel], keepAlive: false, close: `Hide ${label}` };
    return panel === "tasks" ? { ...spec, extras: <PanelBadge c={c} panel={panel} sid={open?.id ?? null} /> } : spec;
  };
  const commands = useShellCommands(
    {
      mac,
      terminal: api !== undefined,
      team: prefs.team,
      // A view's toggle goes through the layout, which knows what shows.
      dispatch: (a) => (a.type === "panel" ? rl.panelAction(a.panel) : dispatch(a)),
      panel: ui.panel,
      palette: ui.palette,
      newThread: newSession,
      toggleSidebar: () => setSidebar((v) => !v),
      nextAttention: nav.nextAttention,
      room: nav.toRow,
      toggleTerminal,
      addProject,
      switchDeck: onDisconnect,
      welcome: onWelcome,
      toggleYolo: r.send.toggleYolo,
      ports,
      preview,
    },
    bridge,
  );
  const limitCommands = useLimitCommands(api, toast, info);
  const paletteRooms = usePaletteRooms(c, projectKey, project, others);
  // The Team page shows only while the team is on; turning it off closes it.
  useEffect(() => {
    if (ui.team && !prefs.team) dispatch({ type: "team", open: false });
  }, [ui.team, prefs.team, dispatch]);
  // What sits at the window's left edge, under a Mac's traffic lights: the
  // sidebar, else the layout's top-left pane (Workspace marks its stack).
  const leftmost = sidebarSide === "left" && sidebar ? "sidebar" : away ? "room" : "corner";

  return (
    // The window is a backdrop with panels on it (docs/design-glass.md,
    // section 4): an 8px gutter, the sidebar's glass, the room's sheet and
    // the side panel's sheet beside it, in the order the layout says.
    <div
      className="shell relative flex h-full gap-2 p-2"
      data-sidebar={sidebarSide}
      data-dock={panelDock}
      data-mac={mac ? "" : undefined}
      data-lights={mac && leftmost !== "sidebar" ? leftmost : undefined}
    >
      {/* The gutter along the top drags the window, as a title bar would. */}
      <div aria-hidden className="drag absolute inset-x-0 top-0 h-2" />
      {sidebar && (
        <Sidebar
          order={nav.order}
          data={{
            project,
            source,
            foreign,
            c,
            link: state.link,
            sessions: state.sessions.filter((s) => !isSide(s)),
            openId: ui.openId,
            unread: ui.unread,
            mac,
            theme: state.theme,
            projects,
            settings: ui.settings,
            update: update === null ? null : { version: update, onRestart: () => void bridge.installUpdate() },
            usage: ui.usage,
            team: teamPage,
            inbox: ui.inbox,
            inboxCount: attention.items.length,
            digits,
            shared: teamOn?.teammates.flatMap((m) => m.threads.map((t) => ({ peer: m.name, thread: t }))) ?? [],
            openShared: shared === null || away || !prefs.team ? null : `${shared.peer}/${shared.thread.id}`,
            online: teamOn === null ? null : teamOn.teammates.filter((m) => m.link === "online").length,
          }}
          actions={{
            onOpen: openSession,
            onNew: newSession,
            onRename: (id, title) => void c.renameSession(id, title),
            onDelete: (id) => void c.deleteSession(id),
            onThreadMenu: (id, title, archive) => bridge.threadMenu({ id, title, archive }),
            onForeign: (src, id) => orToast(onProject(src, id)),
            onNewIn: (src) => (src === source ? newSession() : orToast(onProject(src, "new"))),
            onProject: (k) => orToast(onProject(k, "new")),
            onAddProject: addProject,
            onCloseProject,
            onConnect,
            onDisconnect,
            onPalette: () => dispatch({ type: "palette", open: true }),
            onSettings: (section) => dispatch({ type: "settings", section }),
            onUsage: () => dispatch({ type: "usage", open: !ui.usage }),
            onTeam: prefs.team ? () => dispatch({ type: "team", open: !ui.team }) : null,
            onInbox: () => dispatch({ type: "inbox", open: !ui.inbox }),
            onShared: openShared,
            onCollapse: () => setSidebar(false),
          }}
        />
      )}
      {away ? (
        // A page in the room's place: one sheet.
        <div className="room-sheet sheet relative flex min-w-[min(30rem,100%)] flex-1 flex-col overflow-hidden rounded-(--sheet-radius)">
          <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
            {ui.inbox ? (
              <Inbox
                items={attention.items}
                mac={mac}
                onAnswer={attention.onAnswer}
                onContract={(item, accept) => orToast(attention.onContract(item, accept))}
                onOpen={(item) => item.room !== null && openRoom(item.project, item.room)}
                onBack={() => dispatch({ type: "inbox", open: false })}
              />
            ) : ui.usage ? (
              <UsagePage project={api} theme={state.theme} onOpenThread={openSession} onAddProject={addProject} />
            ) : teamPage ? (
              <TeamPage
                api={api}
                thread={open}
                onError={toast}
                onInfo={info}
                onOpenShared={openShared}
                onConnection={() => dispatch({ type: "settings", section: "connection" })}
                onAddProject={addProject}
              />
            ) : ui.settings !== null ? (
              <LiveSettings c={c} teammates={teammates} section={ui.settings} where={where} transport={transport} mac={mac} bridge={bridge} folder={folder} onDisconnect={onDisconnect} />
            ) : null}
          </main>
        </div>
      ) : (
        // The room: its panes as the person laid them out.
        <Workspace
          ctl={rl.ctl}
          visible={rl.visible}
          spec={paneSpec}
          render={(p, shown) =>
            p === CONVERSATION ? (
              <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-canvas">
      {shared !== null && api !== undefined && prefs.team ? (
                  <SharedRoom
                    key={`${shared.peer}/${shared.thread.id}`}
                    api={api}
                    peer={shared.peer}
                    thread={shared.thread}
                    theme={state.theme}
                    sidebar={sidebar}
                    onShowSidebar={() => setSidebar(true)}
                    onError={toast}
                  />
                ) : (
                  <Room
                    c={c}
                    state={state}
                    open={open}
                    project={project}
                    source={source}
                    mac={mac}
                    engine={engine}
                    r={r}
                    inputRef={inputRef}
                    focusInput={focusInput}
                    sidebar={sidebar}
                    onShowSidebar={() => setSidebar(true)}
                    git={git}
                    team={team}
                    terminal={terminal}
                    onTerminal={toggleTerminal}
                    diff={rl.diffShown}
                    panelOpen={rl.panelShown}
                    onPanel={openPanel}
                    onDiff={() => rl.panelAction("changes")}
                    onTogglePanel={togglePanel}
                    layout={<LayoutMenu mac={mac} {...rl.menu} />}
                    onSettings={() => dispatch({ type: "settings", section: "general" })}
                    onPreview={preview}
                    toast={toast}
                    info={info}
                    onOpenThread={openSession}
                  />
                )}
              </main>
            ) : isTerminal(p) ? (
              api !== undefined && <TerminalPane project={api} slot={p} visible={shown && terminal} onError={toast} />
            ) : (
              <LineComments.Provider value={r.room.comments}>
                <LivePanelPane
                  projectKey={projectKey}
                  c={c}
                  bridge={bridge}
                  panel={p as Panel}
                  open={open}
                  files={r.files}
                  focus={focus}
                  crew={r.crew}
                  crewAt={r.crewAt}
                  branch={git.status?.repo === true && !git.status.detached ? git.status.branch : null}
                  onRefresh={r.refreshChanges}
                  onEditQueued={(text) => {
                    r.setDraft(text);
                    focusInput();
                  }}
                  onError={toast}
                  onInfo={info}
                  onPanel={openPanel}
                  onOpenThread={openSession}
                />
              </LineComments.Provider>
            )
          }
          ops={rl.ops}
          float={panelDock === "float"}
          focused={rl.focused}
          ring={rl.ring}
          onFocused={rl.onFocused}
          mac={mac}
        />
      )}
      {ui.palette && (
        <Palette
          commands={limitCommands.length === 0 ? commands : [...commands, ...limitCommands]}
          rooms={paletteRooms}
          sources={[{ key: projectKey, c }, ...others.map((o) => ({ key: o.key, c: o.c }))]}
          onOpenRoom={openRoom}
          onClose={() => dispatch({ type: "palette", open: false })}
        />
      )}
      <Toasts c={c} toasts={state.toasts} />
      <YoloFx play={yoloPlays} />
    </div>
  );
}
