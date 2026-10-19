import { applyAgentChip } from "@web/composer";
import type { ChangedFile, Image, Session } from "@web/protocol";
import { sessionMode, sessionTitle } from "@web/state";
import { relativeTime } from "@web/time";
import {
  Ellipsis,
  FileDiff,
  Folder,
  ListOrdered,
  PanelLeft,
  PanelRight,
  Settings as SettingsIcon,
  ShieldQuestion,
  Square,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore } from "react";
import { agentKind } from "../agents";
import type { DeckClient } from "../client";
import { addAgentMention } from "../completion";
import { cn } from "../lib/cn";
import { setPrefs, usePrefs } from "../prefs";
import { initialUi, PANELS, type Panel, reduceUi, SETTINGS_SECTIONS } from "../ui";
import { AgentPicker, AgentsStrip } from "./AgentsStrip";
import { ApprovalCard } from "./ApprovalCard";
import { Composer } from "./Composer";
import { isMac, modKey } from "./keys";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
import { MenuItem, MenuSeparator, Popover } from "./kit/menu";
import { ModePicker } from "./ModePicker";
import { type Command, Palette } from "./Palette";
import { RightPanel } from "./RightPanel";
import { SettingsPage } from "./Settings";
import { type ProjectEntry, Sidebar } from "./Sidebar";
import { Timeline } from "./Timeline";

// The draft of the composer on the new-thread screen, before a thread exists.
const NEW = "";

// A title for a thread started from its first message: the first line,
// without the agent mentions.
function titleFrom(text: string): string {
  const line = text.replace(/(^|\s)\$[\w-]+[+*]?/g, " ").trim().split("\n")[0] ?? "";
  const t = line.replace(/\s+/g, " ").trim();
  return t.length > 60 ? `${t.slice(0, 57).trimEnd()}…` : t;
}

function MoreMenu({
  busy,
  queued,
  pending,
  onStop,
  onPanel,
  onSettings,
}: {
  busy: boolean;
  queued: number;
  pending: number;
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
      <MenuItem onClick={run(() => onPanel("approvals"))}>
        <ShieldQuestion /> <span className="flex-1">Approvals</span>
        {pending > 0 && <Badge variant="warning">{pending}</Badge>}
      </MenuItem>
      <MenuItem onClick={run(() => onPanel("queue"))}>
        <ListOrdered /> <span className="flex-1">Queue</span>
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
  where,
  transport,
  projects,
  onProject,
  onAddProject,
  onDisconnect,
}: {
  c: DeckClient;
  where: string;
  transport: string;
  projects: ProjectEntry[];
  onProject: (key: string) => Promise<string | null>;
  onAddProject: () => Promise<string | null>;
  onDisconnect: () => void;
}) {
  const state = useSyncExternalStore(c.store.subscribe, () => c.store.state);
  const prefs = usePrefs();
  const [ui, dispatch] = useReducer(reduceUi, initialUi);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [now, setNow] = useState(() => Date.now());
  const [sidebar, setSidebar] = useState(true);
  const [wide, setWide] = useState(false);
  const [focus, setFocus] = useState<string | null>(null);
  const [changes, setChanges] = useState<{ id: string; files: ChangedFile[] } | null>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const mac = useMemo(isMac, []);

  const open: Session | undefined = state.sessions.find((s) => s.id === ui.openId);
  const sid = open?.id ?? null;
  const key = sid ?? NEW;
  const draft = drafts[key] ?? "";
  const setDraft = useCallback((text: string) => setDrafts((d) => ({ ...d, [key]: text })), [key]);

  useEffect(() => {
    if (state.sessionsLoaded) dispatch({ type: "sessions", sessions: state.sessions });
  }, [state.sessions, state.sessionsLoaded]);

  useEffect(() => {
    dispatch({ type: "messages", messages: state.messages });
  }, [state.messages]);

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

  const focusInput = useCallback(() => requestAnimationFrame(() => inputRef.current?.focus()), []);

  const openSession = useCallback(
    (id: string | null) => {
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
      setDrafts((d) => ({ ...d, [NEW]: "" }));
      openSession(s.id);
      const ok = await c.send(s.id, text, images);
      // The thread's own composer keeps the text of a failed send.
      if (!ok) setDrafts((d) => ({ ...d, [s.id]: text }));
      return ok;
    },
    [c, openSession],
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

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
      const k = e.key.toLowerCase();
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
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ui.palette, newSession]);

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
      ...SETTINGS_SECTIONS.map((x) => ({
        id: `settings-${x.section}`,
        label: `Settings: ${x.label}`,
        run: () => dispatch({ type: "settings", section: x.section }),
      })),
      { id: "theme-light", label: "Theme: light", run: () => setPrefs({ theme: "light" }) },
      { id: "theme-dark", label: "Theme: dark", run: () => setPrefs({ theme: "dark" }) },
      { id: "theme-deck", label: "Theme: follow Deck", run: () => setPrefs({ theme: "deck" }) },
      { id: "add-project", label: "Add project…", run: () => void addProject() },
      { id: "switch", label: "Switch Deck", run: onDisconnect },
    ];
    for (const s of state.sessions) {
      list.push({
        id: `open-${s.id}`,
        label: `Open ${sessionTitle(s)}`,
        hint: relativeTime(s.updated_at, now),
        run: () => openSession(s.id),
      });
    }
    return list;
  }, [state.sessions, now, mac, newSession, onDisconnect, addProject, openSession]);

  const online = state.link === "online";
  const roster = sid === null ? [] : (state.roster[sid] ?? []);
  const kinds = state.server?.kinds ?? [];
  const project = state.server?.project ?? "";
  const pending = sid === null ? [] : state.approvals.filter((a) => a.session_id === sid);
  const queued = sid === null ? 0 : (state.queue[sid]?.length ?? 0);
  const imagesOn = state.server?.features.includes("images") ?? false;
  const toast = (text: string) => c.store.dispatch({ type: "toast.add", kind: "error", text });
  const added = files?.reduce((n, f) => n + f.added, 0) ?? 0;
  const removed = files?.reduce((n, f) => n + f.removed, 0) ?? 0;

  // The kinds that wrote in a thread, for the sidebar's stack of marks.
  const kindsOf = useCallback(
    (id: string): string[] => {
      const list = state.roster[id] ?? [];
      const seen = new Set(list.map((l) => l.kind));
      for (const m of state.messages[id] ?? []) {
        if (m.role === "assistant" && m.agent !== undefined) seen.add(agentKind(m.agent, list));
      }
      return [...seen];
    },
    [state.roster, state.messages],
  );

  const picker = (
    <AgentPicker
      draft={draft}
      roster={roster}
      kinds={kinds}
      theme={state.theme}
      onAddress={(agent) => {
        setDraft(applyAgentChip(draft, agent));
        inputRef.current?.focus();
      }}
      onAuto={() => {
        setDraft(draft.replace(/^\$[\w-]+[+*]?[ \t]*/, ""));
        inputRef.current?.focus();
      }}
      onAdd={(kind) => {
        setDraft(addAgentMention(draft, kind));
        inputRef.current?.focus();
      }}
      listModels={(kind) => (sid === null ? Promise.resolve(null) : c.listModels(sid, kind))}
      setModel={(kind, model, effort) => sid !== null && void c.setModel(sid, kind, model, effort)}
    />
  );

  const header = (
    <header className="topbar drag flex h-12 shrink-0 items-center gap-2 px-4">
      {!sidebar && (
        <Button variant="ghost-muted" size="icon-sm" aria-label="Show the sidebar" title="Show the sidebar" onClick={() => setSidebar(true)}>
          <PanelLeft />
        </Button>
      )}
      <nav aria-label="Breadcrumb" className="min-w-0 flex-1">
        <ol className="m-0 flex min-w-0 list-none items-center gap-3 p-0 text-sm">
          <li className="flex min-w-0 shrink items-center gap-1.5 text-muted-foreground">
            <Folder className="size-3.5 shrink-0" />
            <span className="max-w-40 truncate">{project || "Deck"}</span>
          </li>
          <li aria-hidden className="text-muted-foreground/50">
            /
          </li>
          <li className="min-w-10 flex-1">
            <h1 className="title min-w-0 truncate font-medium text-foreground">
              {open === undefined ? (state.sessionsLoaded ? "New thread" : "Connecting…") : sessionTitle(open)}
            </h1>
          </li>
        </ol>
      </nav>
      <div className="panel-toggles flex shrink-0 items-center gap-1">
        <Button
          variant="outline"
          size="sm"
          className="font-normal"
          aria-pressed={ui.panel === "approvals"}
          onClick={() => showPanel("approvals")}
        >
          <ShieldQuestion />
          Approvals
          {state.approvals.length > 0 && <Badge variant="warning">{state.approvals.length}</Badge>}
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="font-normal"
          aria-pressed={ui.panel === "queue"}
          onClick={() => showPanel("queue")}
        >
          <ListOrdered />
          Queue
          {queued > 0 && <Badge variant="muted">{queued}</Badge>}
        </Button>
        <Button
          variant="outline"
          size="sm"
          className="font-normal"
          aria-pressed={ui.panel === "changes"}
          onClick={() => showPanel("changes")}
        >
          <FileDiff />
          Diff
          {files !== null && files.length > 0 && (
            <span className="font-mono text-xs">
              <span className="text-diff-addition-foreground">+{added}</span>{" "}
              <span className="text-diff-deletion-foreground">−{removed}</span>
            </span>
          )}
        </Button>
        <Button
          variant="ghost-muted"
          size="icon-sm"
          aria-label={ui.panel === null ? "Show the side panel" : "Hide the side panel"}
          title={ui.panel === null ? "Show the side panel" : "Hide the side panel"}
          onClick={() => dispatch({ type: "panel", panel: ui.panel ?? "changes" })}
        >
          <PanelRight />
        </Button>
      </div>
    </header>
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
        <span className="ml-auto flex min-w-0 shrink items-center gap-1.5 px-1.5 text-muted-foreground/70 text-xs" title={where}>
          <Folder className="size-3.5 shrink-0" />
          <span className="truncate">{project || where}</span>
        </span>
      </>
    );

  return (
    <div className="shell flex h-full">
      {sidebar && (
        <Sidebar
          project={project}
          where={where}
          link={state.link}
          sessions={state.sessions}
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
          onDisconnect={onDisconnect}
          onPalette={() => dispatch({ type: "palette", open: true })}
          onSettings={(section) => dispatch({ type: "settings", section })}
          onProject={(k) => void onProject(k).then((e) => e !== null && toast(e))}
          onAddProject={() => void addProject()}
          onCollapse={() => setSidebar(false)}
        />
      )}
      {ui.settings !== null ? (
        <main className="center flex min-w-0 flex-1 flex-col bg-background">
          <SettingsPage
            section={ui.settings}
            state={state}
            where={where}
            transport={transport}
            mac={mac}
            onDisconnect={onDisconnect}
          />
        </main>
      ) : (
        <main className="center flex min-w-0 flex-1 flex-col bg-background">
          {header}
          {open === undefined ? (
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
                toolbar={picker}
                placeholder="Ask anything. $ picks an agent, $codex+ starts another."
              />
            </div>
          ) : (
            <>
              <Timeline
                messages={state.messages[open.id] ?? []}
                roster={roster}
                theme={state.theme}
                more={state.pages[open.id]?.more ?? false}
                onOlder={() => c.loadOlder(open.id)}
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
                  toolbar={
                    <>
                      {picker}
                      <MoreMenu
                        busy={open.busy}
                        queued={queued}
                        pending={state.approvals.length}
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
      {ui.panel !== null && ui.settings === null && (
        <RightPanel
          c={c}
          state={state}
          panel={ui.panel}
          open={open}
          files={files}
          focus={focus}
          wide={wide}
          onRefresh={refreshChanges}
          onPanel={(p) => openPanel(p)}
          onWide={() => setWide((w) => !w)}
          onClose={() => dispatch({ type: "panel", panel: null })}
          onOpen={openSession}
        />
      )}
      {ui.palette && <Palette commands={commands} onClose={() => dispatch({ type: "palette", open: false })} />}
      <div className="toasts pointer-events-none fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2" aria-live="polite">
        {state.toasts.map((t) => (
          <div
            key={t.id}
            className={cn(
              `toast toast-${t.kind} pointer-events-auto rounded-xl border bg-popover px-4 py-3 text-sm shadow-xl shadow-black/30`,
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
