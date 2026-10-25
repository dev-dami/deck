import type { Decision, Session } from "@web/protocol";
import { sessionMode } from "@web/state";
import { type ComponentProps, useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { DeckBridge } from "../../shared/bridge";
import { dispatchUi, useEngine, useProjectUi } from "../appState";
import type { InboxItem } from "../attention";
import type { DeckClient } from "../client";
import type { OpenProject } from "../openProjects";
import { usePrefs } from "../prefs";
import { panelSlice, shallowEqual, shellSlice, useAppSlice } from "../select";
import { isSide } from "../sideChat";
import type { Panel, UiAction } from "../ui";
import type { Landing } from "./App";
import { LineComments } from "./DiffView";
import { useGit } from "./Git";
import { Inbox } from "./Inbox";
import { isMac } from "./keys";
import { Palette } from "./Palette";
import { LiveRightPanel } from "./RightPanel";
import { Room } from "./Room";
import { SettingsPage } from "./Settings";
import { SharedRoom } from "./SharedRoom";
import { type ProjectEntry, Sidebar } from "./Sidebar";
import { TeamPage, useTeam } from "./Team";
import { TerminalDrawer, useTerminalShown } from "./Terminal";
import { Toasts } from "./Toasts";
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
  onAnswer: (item: InboxItem, d: Decision) => void;
  onContract: (item: InboxItem, accept: boolean) => Promise<string | null>;
}

// Settings takes the whole state but reads only the panel slice.
function LiveSettings({ c, ...props }: { c: DeckClient } & Omit<ComponentProps<typeof SettingsPage>, "state">) {
  useAppSlice(c.store, panelSlice, shallowEqual);
  return <SettingsPage {...props} state={c.store.state} />;
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
  const [wide, setWide] = useState(false);
  const [focus, setFocus] = useState<string | null>(null);
  const api = c.project;
  const [terminal, setTerminal] = useTerminalShown(api);
  const git = useGit(api);
  const { team } = useTeam(api, 15_000);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const mac = useMemo(isMac, []);
  const foreign = useForeignThreads(others);
  const digits = useModDigits();
  const update = useUpdate(bridge, c);
  const open: Session | undefined = state.sessions.find((s) => s.id === ui.openId);
  useWatchPorts(c, bridge);
  const ports = useOpenPorts(api);
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

  // Opens a panel, never closes it (the panel's own tabs and the menus).
  const openPanel = (p: Panel, path?: string) => {
    if (ui.panel !== p) dispatch({ type: "panel", panel: p });
    setFocus(path ?? null);
  };
  const togglePanel = () => dispatch({ type: "panel", panel: ui.panel ?? "changes" });
  const toggleTerminal = () => setTerminal((v) => !v);
  const preview = (url: string) => {
    if (api === undefined) return;
    setPreviewTarget(api, url);
    openPanel("preview");
  };
  const addProject = () => orToast(onAddProject());
  const commands = useShellCommands(
    {
      mac,
      terminal: api !== undefined,
      dispatch,
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
  const paletteRooms = usePaletteRooms(c, projectKey, project, others);
  // Settings, Usage, Team and the inbox take the place of the room.
  const away = ui.settings !== null || ui.usage || ui.team || ui.inbox;

  return (
    <div className="shell relative flex h-full">
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
            team: ui.team,
            inbox: ui.inbox,
            inboxCount: attention.items.length,
            digits,
            shared: teamOn?.teammates.flatMap((m) => m.threads.map((t) => ({ peer: m.name, thread: t }))) ?? [],
            openShared: shared === null || away ? null : `${shared.peer}/${shared.thread.id}`,
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
            onTeam: () => dispatch({ type: "team", open: !ui.team }),
            onInbox: () => dispatch({ type: "inbox", open: !ui.inbox }),
            onShared: openShared,
            onCollapse: () => setSidebar(false),
          }}
        />
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-background">
          {ui.inbox ? (
            <Inbox
              items={attention.items}
              mac={mac}
              onAnswer={attention.onAnswer}
              onContract={(item, accept) => orToast(attention.onContract(item, accept))}
              onOpen={(item) => item.room !== null && openRoom(item.project, item.room)}
            />
          ) : ui.usage ? (
            <UsagePage project={api} theme={state.theme} onOpenThread={openSession} />
          ) : ui.team ? (
            <TeamPage api={api} thread={open} onError={toast} onInfo={info} onOpenShared={openShared} />
          ) : ui.settings !== null ? (
            <LiveSettings c={c} section={ui.settings} where={where} transport={transport} mac={mac} bridge={bridge} folder={folder} onDisconnect={onDisconnect} />
          ) : shared !== null && api !== undefined ? (
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
              panel={ui.panel}
              onPanel={openPanel}
              onDiff={() => dispatch({ type: "panel", panel: "changes" })}
              onTogglePanel={togglePanel}
              onSettings={() => dispatch({ type: "settings", section: "general" })}
              onPreview={preview}
              toast={toast}
              info={info}
            />
          )}
        </main>
        {api !== undefined && (
          <TerminalDrawer project={api} shown={terminal && !away} onHide={() => setTerminal(false)} onError={toast} onPreview={preview} />
        )}
      </div>
      {ui.panel !== null && !away && shared === null && (
        <LineComments.Provider value={r.room.comments}>
          <LiveRightPanel
            projectKey={projectKey}
            c={c}
            bridge={bridge}
            panel={ui.panel}
            open={open}
            files={r.files}
            focus={focus}
            wide={wide}
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
            onPanel={(p) => openPanel(p)}
            onWide={() => setWide((w) => !w)}
            onClose={() => dispatch({ type: "panel", panel: null })}
          />
        </LineComments.Provider>
      )}
      {ui.palette && <Palette commands={commands} rooms={paletteRooms} onOpenRoom={openRoom} onClose={() => dispatch({ type: "palette", open: false })} />}
      <Toasts c={c} toasts={state.toasts} />
      <YoloFx play={yoloPlays} />
    </div>
  );
}
