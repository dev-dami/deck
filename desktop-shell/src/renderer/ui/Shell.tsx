import { applyAgentChip } from "@web/composer";
import type { ChangedFile, Image, Session } from "@web/protocol";
import { needsConfirm } from "@web/modes";
import type { Mode } from "@web/protocol";
import { type AppState, sessionMode, sessionTitle } from "@web/state";
import { relativeTime } from "@web/time";
import {
  Ellipsis,
  FileDiff,
  Folder,
  ListChecks,
  MessageCircleQuestion,
  Settings as SettingsIcon,
  Square,
} from "lucide-react";
import { type ComponentProps, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { dispatchUi, setDraft as storeDraft, useDraft, useEngine, useProjectUi } from "../appState";
import type { DeckClient } from "../client";
import { addAgentMention } from "../completion";
import type { TeamThread } from "../crew";
import { buildLanes, filterLane } from "../lanes";
import { cn } from "../lib/cn";
import { isSide } from "../sideChat";
import { setPrefs, usePrefs } from "../prefs";
import { kindsBySession, panelSlice, sameKinds, shallowEqual, shellSlice, threadSlice, useAppSlice } from "../select";
import { PANELS, type Panel, SETTINGS_SECTIONS, type UiAction } from "../ui";
import { AgentCards, AgentsStrip } from "./AgentsStrip";
import { useCrew } from "./AgentsTree";
import { ApprovalCard } from "./ApprovalCard";
import { Composer } from "./Composer";
import { BranchPicker, useGit } from "./Git";
import { isMac, modKey } from "./keys";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { MenuItem, MenuSeparator, Popover } from "./kit/menu";
import type { DeckBridge, MenuCommand } from "../../shared/bridge";
import { Lanes } from "./Lanes";
import { ModePicker } from "./ModePicker";
import { type Command, Palette } from "./Palette";
import { RightPanel } from "./RightPanel";
import { RoomHeader } from "./RoomHeader";
import { SettingsPage } from "./Settings";
import { SharedRoom } from "./SharedRoom";
import { TerminalDrawer, useTerminalShown } from "./Terminal";
import { UsagePage } from "./Usage";
import { TeamPage, useShares, useTeam } from "./Team";
import { type ForeignThread, type ProjectEntry, Sidebar } from "./Sidebar";
import type { Landing } from "./App";
import type { OpenProject } from "../openProjects";
import type { EngineStop } from "../../shared/bridge";
import { Timeline } from "./Timeline";
import { yoloMove } from "../yolo";

// The threads of the other open projects, kept current from their stores.
function useForeignThreads(others: OpenProject[]): ForeignThread[] {
  const [rows, setRows] = useState<ForeignThread[]>([]);
  useEffect(() => {
    let lists: unknown[] = [];
    const read = () => {
      const next = others.map((o) => o.c.store.state.sessions);
      // Only a changed session list makes new rows.
      if (next.length === lists.length && next.every((l, i) => l === lists[i])) return;
      lists = next;
      setRows(
        others.flatMap((o) =>
          o.c.store.state.sessions.filter((s) => !isSide(s)).map((s) => ({ source: o.source, project: o.name, s, c: o.c })),
        ),
      );
    };
    read();
    const offs = others.map((o) => o.c.store.subscribe(read));
    return () => {
      for (const off of offs) off();
    };
  }, [others]);
  return rows;
}

// The palette command each menu command runs ("palette" opens the palette).
const MENU_COMMAND: Record<Exclude<MenuCommand, "palette">, string> = {
  "new-thread": "new",
  "open-folder": "add-project",
  settings: "settings-general",
  "toggle-sidebar": "sidebar",
  "toggle-panel": "panel",
  "toggle-terminal": "terminal",
  about: "settings-connection",
};

// The draft of the composer on the new-thread screen, before a thread exists.
const NEW = "";

// A title for a thread started from its first message: the first line,
// without the agent mentions.
function titleFrom(text: string): string {
  const line = text.replace(/(^|\s)\$[\w-]+[+*]?/g, " ").trim().split("\n")[0] ?? "";
  const t = line.replace(/\s+/g, " ").trim();
  return t.length > 60 ? `${t.slice(0, 57).trimEnd()}…` : t;
}

const NO_KINDS: string[] = [];
const wholeState = (s: AppState): AppState => s;

// The open thread's timeline. It reads that thread's messages alone, so
// tokens in other threads do not render it, nor the shell around it. lane
// shows one lane's messages alone (the lane strip's filter).
function LiveTimeline({
  c,
  sid,
  lane,
  ...rest
}: { c: DeckClient; sid: string; lane: string | null } & Omit<ComponentProps<typeof Timeline>, "messages" | "roster" | "more" | "onOlder">) {
  const select = useMemo(() => threadSlice(sid), [sid]);
  const t = useAppSlice(c.store, select, shallowEqual);
  const messages = useMemo(() => filterLane(t.messages, lane), [t.messages, lane]);
  return <Timeline messages={messages} roster={t.roster} more={t.more} onOlder={() => c.loadOlder(sid)} {...rest} />;
}

// The side panel and the Settings page take the whole state but read only
// the panel slice (the side chat reads messages too), so they render when
// that slice changes.
function LiveRightPanel(props: Omit<ComponentProps<typeof RightPanel>, "state">) {
  useAppSlice(props.c.store, props.panel === "side" ? wholeState : panelSlice, shallowEqual);
  return <RightPanel {...props} state={props.c.store.state} />;
}

function LiveSettings({ c, ...props }: { c: DeckClient } & Omit<ComponentProps<typeof SettingsPage>, "state">) {
  useAppSlice(c.store, panelSlice, shallowEqual);
  return <SettingsPage {...props} state={c.store.state} />;
}

// In place of the thread when the project's engine stopped by itself.
function EngineStopped({ stop, onRestart }: { stop: EngineStop; onRestart: () => Promise<void> }) {
  const [busy, setBusy] = useState(false);
  return (
    <div className="engine-stopped flex min-h-0 flex-1 flex-col items-center justify-center gap-4 overflow-y-auto px-5 pb-[14vh]" role="alert">
      <h2 className="text-balance text-center font-medium text-xl">{stop.message}</h2>
      {stop.stderr.length > 0 && (
        <pre className="engine-log max-h-64 w-full max-w-2xl overflow-auto rounded-lg border bg-code px-3 py-2 font-mono text-muted-foreground text-xs" aria-label="The engine's last output">
          {stop.stderr.join("\n")}
        </pre>
      )}
      <Button
        disabled={busy}
        onClick={() => {
          setBusy(true);
          void onRestart().finally(() => setBusy(false));
        }}
      >
        {busy ? "Restarting…" : "Restart"}
      </Button>
    </div>
  );
}

function MoreMenu({
  busy,
  queued,
  onStop,
  onPanel,
  onSettings,
}: {
  busy: boolean;
  queued: number;
  onStop: () => void;
  onPanel: (p: Panel) => void;
  onSettings: () => void;
}) {
  const [open, setOpen] = useState(false);
  const run = (f: () => void) => () => {
    setOpen(false);
    f();
  };
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      label="More"
      className="w-56"
      trigger={
        <Button
          variant="ghost-muted"
          size="icon-sm"
          aria-label="More"
          title="More"
          aria-expanded={open}
          onClick={() => setOpen((o) => !o)}
        >
          <Ellipsis />
        </Button>
      }
    >
      {busy && (
        <>
          <MenuItem className="text-destructive-foreground" onClick={run(onStop)}>
            <Square /> Stop all agents
          </MenuItem>
          <MenuSeparator />
        </>
      )}
      <MenuItem onClick={run(() => onPanel("changes"))}>
        <FileDiff /> Open diff
      </MenuItem>
      <MenuItem onClick={run(() => onPanel("side"))}>
        <MessageCircleQuestion /> <span className="flex-1">Side chat</span>
      </MenuItem>
      <MenuItem onClick={run(() => onPanel("tasks"))}>
        <ListChecks /> <span className="flex-1">Tasks</span>
        {queued > 0 && <Badge variant="muted">{queued}</Badge>}
      </MenuItem>
      <MenuSeparator />
      <MenuItem onClick={run(onSettings)}>
        <SettingsIcon /> Settings
      </MenuItem>
    </Popover>
  );
}

// where names the Deck: host:port for a paired one, the folder for a local one.
export function Shell({
  c,
  projectKey,
  bridge,
  folder,
  name,
  where,
  transport,
  others,
  projects,
  onProject,
  onAddProject,
  onCloseProject,
  onDisconnect,
  onConnect,
}: {
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
}) {
  const state = useAppSlice(c.store, shellSlice, shallowEqual);
  const prefs = usePrefs();
  const ui = useProjectUi(projectKey);
  const dispatch = useCallback((a: UiAction) => dispatchUi(projectKey, a), [projectKey]);
  const engine = useEngine(projectKey);
  const [now, setNow] = useState(() => Date.now());
  const [sidebar, setSidebar] = useState(true);
  const [wide, setWide] = useState(false);
  const [focus, setFocus] = useState<string | null>(null);
  const api = c.project;
  const [terminal, setTerminal] = useTerminalShown(api);
  const git = useGit(api);
  const [changes, setChanges] = useState<{ id: string; files: ChangedFile[] } | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const mac = useMemo(isMac, []);
  const foreign = useForeignThreads(others);

  const open: Session | undefined = state.sessions.find((s) => s.id === ui.openId);
  const sid = open?.id ?? null;
  const key = sid ?? NEW;
  const draft = useDraft(projectKey, key);
  const setDraft = useCallback((text: string) => storeDraft(projectKey, key, text), [projectKey, key]);
  // The lane the timeline shows alone, kept for the room it was picked in.
  const [lanePick, setLanePick] = useState<{ sid: string | null; lane: string | null }>({ sid: null, lane: null });
  const lane = lanePick.sid === sid ? lanePick.lane : null;
  // A room a teammate shares with you, shown in place of this project's room.
  const [shared, setShared] = useState<{ peer: string; thread: TeamThread } | null>(null);

  useEffect(() => {
    if (ui.openId === null) c.closeSession();
    else void c.openSession(ui.openId);
  }, [c, ui.openId]);

  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), 30_000);
    return () => clearInterval(t);
  }, []);

  useEffect(() => {
    const timers = state.toasts.map((t) =>
      setTimeout(() => c.store.dispatch({ type: "toast.dismiss", id: t.id }), 6000),
    );
    return () => {
      for (const t of timers) clearTimeout(t);
    };
  }, [c, state.toasts]);

  // The open thread's changed files: for the card under the last reply and
  // the diff panel. Fetched on opening and each time the agents finish.
  const [tick, setTick] = useState(0);
  const refreshChanges = useCallback(() => setTick((t) => t + 1), []);
  const busy = open?.busy === true;
  useEffect(() => {
    if (sid === null) return;
    let live = true;
    void c.changes(sid).then((f) => {
      if (live) setChanges({ id: sid, files: f ?? [] });
    });
    return () => {
      live = false;
    };
  }, [c, sid, busy, tick]);
  const files = changes !== null && changes.id === sid ? changes.files : null;
  // The agents' edits change the work tree: look again when they stop.
  const refreshGit = git.refresh;
  useEffect(() => {
    if (!busy) void refreshGit();
  }, [busy, refreshGit]);

  // The room's agents tree (the lane strip and the Agents tab), who follows
  // the room, and the team (the share control and the sidebar).
  const { crew, at: crewAt } = useCrew(api, sid, busy);
  const { shares, set: setShare } = useShares(api, sid);
  const { team } = useTeam(api, 15_000);

  const focusInput = useCallback(() => requestAnimationFrame(() => inputRef.current?.focus()), []);

  const openSession = useCallback(
    (id: string | null) => {
      setShared(null);
      dispatch({ type: "open", id });
      focusInput();
    },
    [focusInput],
  );

  // A new thread is the empty screen: the first message makes it.
  const newSession = useCallback(() => openSession(null), [openSession]);

  // Sends from the new-thread screen: makes the thread, opens it and sends.
  const start = useCallback(
    async (text: string, images: Image[]): Promise<boolean> => {
      const s = await c.createSession(titleFrom(text) || undefined);
      if (s === null) return false;
      storeDraft(projectKey, NEW, "");
      openSession(s.id);
      const ok = await c.send(s.id, text, images);
      // The thread's own composer keeps the text of a failed send.
      if (!ok) storeDraft(projectKey, s.id, text);
      return ok;
    },
    [c, projectKey, openSession],
  );

  const showPanel = useCallback((p: Panel, path?: string) => {
    dispatch({ type: "panel", panel: p });
    if (path !== undefined) setFocus(path);
  }, []);
  // Opens a panel, never closes it (the panel's own tabs and the menus).
  const openPanel = useCallback(
    (p: Panel, path?: string) => {
      if (ui.panel !== p) dispatch({ type: "panel", panel: p });
      setFocus(path ?? null);
    },
    [ui.panel],
  );

  // The mode each thread had before Ctrl+Shift+Y put it in yolo, to go back to.
  const beforeYolo = useRef<Record<string, Mode>>({});
  const toggleYolo = useCallback(() => {
    if (open === undefined) return;
    const current = sessionMode(open);
    const move = yoloMove(current, beforeYolo.current[open.id] ?? null, state.server?.allow_yolo ?? false);
    if ("refused" in move) {
      c.store.dispatch({ type: "toast.add", kind: "error", text: move.refused });
      return;
    }
    if (needsConfirm(move.to) && prefs.confirmYolo && !window.confirm("Agents run everything without asking. Switch to Yolo?")) return;
    if (move.to === "yolo" && current !== null) beforeYolo.current[open.id] = current;
    void c.setMode(open.id, move.to);
  }, [open, c, state.server?.allow_yolo, prefs.confirmYolo]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey) return;
      const k = e.key.toLowerCase();
      if (e.shiftKey) {
        // With Shift, so Ctrl+Y stays Redo in a text field.
        if (k === "y") {
          e.preventDefault();
          toggleYolo();
        }
        return;
      }
      if (k === "k") {
        e.preventDefault();
        dispatch({ type: "palette", open: !ui.palette });
      } else if (k === "n") {
        e.preventDefault();
        newSession();
      } else if (k === "b") {
        e.preventDefault();
        setSidebar((v) => !v);
      } else if (k === ",") {
        e.preventDefault();
        dispatch({ type: "settings", section: "general" });
      } else if ((k === "j" || k === "`") && api !== undefined) {
        e.preventDefault();
        setTerminal((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ui.palette, newSession, api, toggleYolo]);

  const addProject = useCallback(async () => {
    const e = await onAddProject();
    if (e !== null) c.store.dispatch({ type: "toast.add", kind: "error", text: e });
  }, [c, onAddProject]);

  const commands = useMemo((): Command[] => {
    const list: Command[] = [
      { id: "new", label: "New thread", hint: `${modKey(mac)}N`, run: newSession },
      ...PANELS.map((p) => ({
        id: `panel-${p.panel}`,
        label: `Toggle ${p.panel === "changes" ? "diff" : p.label.toLowerCase()}`,
        run: () => dispatch({ type: "panel", panel: p.panel }),
      })),
      { id: "sidebar", label: "Toggle sidebar", hint: `${modKey(mac)}B`, run: () => setSidebar((v) => !v) },
      { id: "panel", label: "Toggle side panel", run: () => dispatch({ type: "panel", panel: ui.panel ?? "changes" }) },
      ...SETTINGS_SECTIONS.map((x) => ({
        id: `settings-${x.section}`,
        label: `Settings: ${x.label}`,
        run: () => dispatch({ type: "settings", section: x.section }),
      })),
      { id: "usage", label: "Open usage", run: () => dispatch({ type: "usage", open: true }) },
      { id: "team", label: "Open team", run: () => dispatch({ type: "team", open: true }) },
      ...(api === undefined
        ? []
        : [{ id: "terminal", label: "Toggle terminal", hint: `${modKey(mac)}J`, run: () => setTerminal((v) => !v) }]),
      { id: "theme-light", label: "Theme: light", run: () => setPrefs({ theme: "light" }) },
      { id: "theme-dark", label: "Theme: dark", run: () => setPrefs({ theme: "dark" }) },
      { id: "theme-deck", label: "Theme: follow Deck", run: () => setPrefs({ theme: "deck" }) },
      { id: "add-project", label: "Add project…", run: () => void addProject() },
      { id: "switch", label: "Switch Deck", run: onDisconnect },
    ];
    for (const s of state.sessions) {
      if (isSide(s)) continue;
      list.push({
        id: `open-${s.id}`,
        label: `Open ${sessionTitle(s)}`,
        hint: relativeTime(s.updated_at, now),
        run: () => openSession(s.id),
      });
    }
    return list;
  }, [state.sessions, now, mac, newSession, onDisconnect, addProject, openSession, api, ui.panel]);

  // The application menu runs the palette's commands.
  const commandsRef = useRef(commands);
  commandsRef.current = commands;
  useEffect(
    () =>
      bridge.onMenu((m) => {
        if (m === "palette") dispatch({ type: "palette", open: true });
        else commandsRef.current.find((x) => x.id === MENU_COMMAND[m])?.run();
      }),
    [bridge],
  );

  // A downloaded update waits for a restart; "Check for updates" answers
  // in a toast.
  const [update, setUpdate] = useState<string | null>(null);
  useEffect(
    () =>
      bridge.onUpdate((ev) => {
        if (ev.type === "ready") setUpdate(ev.version);
        else c.store.dispatch({ type: "toast.add", kind: "info", text: ev.message });
      }),
    [bridge, c],
  );

  const online = state.link === "online";
  const roster = sid === null ? [] : (state.roster[sid] ?? []);
  const kinds = state.server?.kinds ?? [];
  const source = projects.find((p) => p.active)?.key ?? "";
  // A folder is named after itself; a paired Deck by what it says.
  const project = source.startsWith("folder:") ? name : state.server?.project || name;
  const pending = sid === null ? [] : state.approvals.filter((a) => a.session_id === sid);
  const queued = sid === null ? 0 : (state.queue[sid]?.length ?? 0);
  const imagesOn = state.server?.features.includes("images") ?? false;
  const toast = useCallback((text: string) => c.store.dispatch({ type: "toast.add", kind: "error", text }), [c]);
  const info = useCallback((text: string) => c.store.dispatch({ type: "toast.add", kind: "info", text }), [c]);
  // Rooms teammates share with you, and how many teammates are online.
  const teamOn = typeof team === "object" && team !== null && team.available ? team : null;
  const sharedRooms = teamOn?.teammates.flatMap((m) => m.threads.map((t) => ({ peer: m.name, thread: t }))) ?? [];
  const mates = teamOn === null ? null : teamOn.teammates.filter((m) => m.link === "online").length;
  const openShared = useCallback(
    (peer: string, thread: TeamThread) => {
      // Opening a room closes Settings, Usage and Team, as a thread does.
      dispatch({ type: "open", id: ui.openId });
      setShared({ peer, thread });
    },
    [dispatch, ui.openId],
  );

  // The kinds that wrote in a thread, for the sidebar's stack of marks.
  const kindsMap = useAppSlice(c.store, kindsBySession, sameKinds);
  const kindsOf = useCallback((id: string): string[] => kindsMap[id] ?? NO_KINDS, [kindsMap]);

  const cards = (
    <AgentCards
      draft={draft}
      setDraft={setDraft}
      roster={roster}
      kinds={kinds}
      theme={state.theme}
      sessionId={sid}
      approvals={state.approvals}
      onAdd={(kind) => {
        setDraft(addAgentMention(draft, kind));
        inputRef.current?.focus();
      }}
      onFocus={() => inputRef.current?.focus()}
    />
  );

  const header = (
    <>
      <RoomHeader
        sidebar={sidebar}
        onShowSidebar={() => setSidebar(true)}
        project={project}
        open={open}
        loaded={state.sessionsLoaded}
        roster={roster}
        theme={state.theme}
        onRename={(title) => open !== undefined && void c.renameSession(open.id, title)}
        api={api}
        git={git}
        team={team}
        shares={shares}
        onShare={setShare}
        files={files}
        terminal={terminal}
        terminalKey={`${modKey(mac)}J`}
        onTerminal={() => setTerminal((v) => !v)}
        diff={ui.panel === "changes"}
        panelOpen={ui.panel !== null}
        onDiff={() => showPanel("changes")}
        onPanel={() => dispatch({ type: "panel", panel: ui.panel ?? "changes" })}
        onError={toast}
        onInfo={info}
      />
      {open !== undefined && (
        <Lanes
          lanes={buildLanes({ roster, agents: crew?.agents ?? null, shares, waiting: pending.map((a) => a.agent) })}
          theme={state.theme}
          filter={lane}
          onFilter={(l) => setLanePick({ sid: open.id, lane: l })}
          listModels={(kind) => c.listModels(open.id, kind)}
          setModel={(kind, model, effort) => void c.setModel(open.id, kind, model, effort)}
          onStop={(agent) => void c.interrupt(open.id, agent)}
          onSteer={(agent) => {
            setDraft(applyAgentChip(draft, agent));
            focusInput();
          }}
        />
      )}
    </>
  );

  const strip =
    open === undefined ? undefined : (
      <>
        <ModePicker
          mode={sessionMode(open)}
          allowYolo={state.server?.allow_yolo ?? false}
          confirmYolo={prefs.confirmYolo}
          onSet={(m) => void c.setMode(open.id, m)}
        />
        <span className="h-3.5 w-px shrink-0 bg-border" />
        <AgentsStrip
          sessionId={open.id}
          roster={roster}
          approvals={state.approvals}
          theme={state.theme}
          onInterrupt={(agent) => void c.interrupt(open.id, agent)}
          onMention={(agent) => {
            setDraft(applyAgentChip(draft, agent));
            inputRef.current?.focus();
          }}
          listModels={(kind) => c.listModels(open.id, kind)}
          setModel={(kind, model, effort) => void c.setModel(open.id, kind, model, effort)}
        />
        <span className="ml-auto flex min-w-0 shrink items-center">
          {api !== undefined && git.status?.repo === true ? (
            <BranchPicker git={git} project={api} onError={toast} />
          ) : (
            <span className="flex min-w-0 items-center gap-1.5 px-1.5 text-muted-foreground/70 text-xs">
              <Folder className="size-3.5 shrink-0" />
              <span className="truncate">{project || "Deck"}</span>
            </span>
          )}
        </span>
      </>
    );

  return (
    <div className="shell flex h-full">
      {sidebar && (
        <Sidebar
          project={project}
          source={source}
          foreign={foreign}
          onForeign={(src, id) => void onProject(src, id).then((e) => e !== null && toast(e))}
          onCloseProject={onCloseProject}
          onConnect={onConnect}
          onNewIn={(src) => (src === source ? newSession() : void onProject(src, "new").then((e) => e !== null && toast(e)))}
          grouping={prefs.grouping}
          link={state.link}
          sessions={state.sessions.filter((s) => !isSide(s))}
          openId={ui.openId}
          unread={ui.unread}
          now={now}
          mac={mac}
          rows={prefs.rows}
          theme={state.theme}
          kindsOf={kindsOf}
          projects={projects}
          settings={ui.settings}
          onOpen={openSession}
          onNew={newSession}
          onRename={(id, title) => void c.renameSession(id, title)}
          onDelete={(id) => void c.deleteSession(id)}
          onThreadMenu={(id, title) => bridge.threadMenu({ id, title })}
          update={update === null ? null : { version: update, onRestart: () => void bridge.installUpdate() }}
          onDisconnect={onDisconnect}
          onPalette={() => dispatch({ type: "palette", open: true })}
          onSettings={(section) => dispatch({ type: "settings", section })}
          usage={ui.usage}
          onUsage={() => dispatch({ type: "usage", open: !ui.usage })}
          team={ui.team}
          onTeam={() => dispatch({ type: "team", open: !ui.team })}
          onProject={(k) => void onProject(k, "new").then((e) => e !== null && toast(e))}
          onAddProject={() => void addProject()}
          onCollapse={() => setSidebar(false)}
          shared={sharedRooms}
          openShared={shared === null || ui.team || ui.usage || ui.settings !== null ? null : `${shared.peer}/${shared.thread.id}`}
          onShared={openShared}
          online={mates}
        />
      )}
      <div className="flex min-w-0 flex-1 flex-col">
        {ui.usage ? (
          <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-background">
            <UsagePage project={api} theme={state.theme} onOpenThread={openSession} />
          </main>
        ) : ui.team ? (
          <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-background">
            <TeamPage api={api} thread={open} onError={toast} onInfo={info} onOpenShared={openShared} />
          </main>
        ) : ui.settings !== null ? (
          <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-background">
            <LiveSettings
              c={c}
              section={ui.settings}
              where={where}
              transport={transport}
              mac={mac}
              bridge={bridge}
              folder={folder}
              onDisconnect={onDisconnect}
            />
          </main>
        ) : shared !== null && api !== undefined ? (
          <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-background">
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
          </main>
        ) : (
          <main className="center flex min-h-0 min-w-0 flex-1 flex-col bg-background">
            {header}
            {engine !== null ? (
              <EngineStopped stop={engine} onRestart={() => c.restart?.() ?? Promise.resolve()} />
            ) : open === undefined ? (
              <div className="welcome flex min-h-0 flex-1 flex-col items-center justify-center gap-6 overflow-y-auto px-5 pb-[14vh]">
                <h2 className="welcome-title text-balance text-center font-medium text-2xl tracking-tight sm:text-3xl">
                  {state.sessionsLoaded ? (
                    <>
                      What should we build{project === "" ? "" : " in "}
                      {project !== "" && (
                        <span className="underline decoration-foreground/30 decoration-dotted underline-offset-4">{project}</span>
                      )}
                      ?
                    </>
                  ) : (
                    "Connecting to Deck…"
                  )}
                </h2>
                <Composer
                  key={NEW}
                  draft={draft}
                  setDraft={setDraft}
                  roster={[]}
                  kinds={kinds}
                  images={imagesOn}
                  online={online && state.sessionsLoaded}
                  onSend={start}
                  onError={toast}
                  inputRef={inputRef}
                  sendKey={prefs.sendKey}
                  agents={cards}
                  placeholder="Ask anything. $ picks an agent, $codex+ starts another."
                />
              </div>
            ) : (
              <>
                <LiveTimeline
                  c={c}
                  sid={open.id}
                  lane={lane}
                  theme={state.theme}
                  changes={files}
                  onOpenDiff={(path) => openPanel("changes", path)}
                />
                <div className="shrink-0 px-5 pb-3">
                  <Composer
                    key={open.id}
                    draft={draft}
                    setDraft={setDraft}
                    roster={roster}
                    kinds={kinds}
                    images={imagesOn}
                    online={online}
                    onSend={(text: string, images: Image[]) => c.send(open.id, text, images)}
                    onError={toast}
                    inputRef={inputRef}
                    sendKey={prefs.sendKey}
                    busy={open.busy}
                    onStop={() => void c.cancel(open.id)}
                    banner={
                      pending.length > 0 && (
                        <div className="flex flex-col gap-1" aria-label="Approvals for this thread">
                          {pending[0] !== undefined && (
                            <ApprovalCard
                              a={pending[0]}
                              index={0}
                              count={pending.length}
                              className="border-0 bg-transparent"
                              onAnswer={(d) => void c.answerApproval(pending[0]!.session_id, pending[0]!.id, d)}
                            />
                          )}
                        </div>
                      )
                    }
                    agents={cards}
                    toolbar={
                      <>
                        <MoreMenu
                          busy={open.busy}
                          queued={queued}
                          onStop={() => void c.cancel(open.id)}
                          onPanel={(p) => openPanel(p)}
                          onSettings={() => dispatch({ type: "settings", section: "general" })}
                        />
                      </>
                    }
                    strip={strip}
                  />
                </div>
              </>
            )}
          </main>
        )}
        {api !== undefined && (
          <TerminalDrawer project={api} shown={terminal && ui.settings === null && !ui.usage && !ui.team} onHide={() => setTerminal(false)} onError={toast} />
        )}
      </div>
      {ui.panel !== null && ui.settings === null && !ui.usage && !ui.team && shared === null && (
        <LiveRightPanel
          c={c}
          panel={ui.panel}
          open={open}
          files={files}
          focus={focus}
          wide={wide}
          crew={crew}
          crewAt={crewAt}
          branch={git.status?.repo === true && !git.status.detached ? git.status.branch : null}
          onRefresh={refreshChanges}
          onError={toast}
          onInfo={info}
          onPanel={(p) => openPanel(p)}
          onWide={() => setWide((w) => !w)}
          onClose={() => dispatch({ type: "panel", panel: null })}
        />
      )}
      {ui.palette && <Palette commands={commands} onClose={() => dispatch({ type: "palette", open: false })} />}
      <div className="toasts pointer-events-none fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2" aria-live="polite">
        {state.toasts.map((t) => (
          <div
            key={t.id}
            className={cn(
              `toast toast-${t.kind} pointer-events-auto animate-enter rounded-xl border border-hairline-strong bg-surface-2 px-4 py-3 text-sm shadow-popover inset-shadow-hl`,
              t.kind === "error" && "border-destructive/40",
            )}
          >
            {t.text}
          </div>
        ))}
      </div>
    </div>
  );
}
