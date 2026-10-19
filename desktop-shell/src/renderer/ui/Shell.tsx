import { applyAgentChip } from "@web/composer";
import type { Image, Session } from "@web/protocol";
import { sessionMode, sessionTitle } from "@web/state";
import { relativeTime } from "@web/time";
import { GitCompareArrows, ListOrdered, type LucideIcon, ShieldCheck } from "lucide-react";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore } from "react";
import type { DeckClient } from "../client";
import { addAgentMention } from "../completion";
import { cn } from "../lib/cn";
import { initialUi, PANELS, type Panel, reduceUi } from "../ui";
import { AgentsStrip } from "./AgentsStrip";
import { ApprovalCard } from "./ApprovalCard";
import { Composer } from "./Composer";
import { isMac, modKey } from "./keys";
import { Badge, Kbd } from "./kit/badge";
import { Button } from "./kit/button";
import { ModePicker } from "./ModePicker";
import { type Command, Palette } from "./Palette";
import { RightPanel } from "./RightPanel";
import { Sidebar } from "./Sidebar";
import { Timeline } from "./Timeline";

const PANEL_ICONS: Record<Panel, LucideIcon> = {
  changes: GitCompareArrows,
  approvals: ShieldCheck,
  queue: ListOrdered,
};

// The draft of the composer on the start screen, before a session exists.
const NEW = "";

// A title for a session started from its first message: the first line,
// without the agent mentions.
function titleFrom(text: string): string {
  const line = text.replace(/(^|\s)\$[\w-]+[+*]?/g, " ").trim().split("\n")[0] ?? "";
  const t = line.replace(/\s+/g, " ").trim();
  return t.length > 60 ? `${t.slice(0, 57).trimEnd()}…` : t;
}

// where names the Deck: host:port for a paired one, the folder for a local one.
export function Shell({ c, where, onDisconnect }: { c: DeckClient; where: string; onDisconnect: () => void }) {
  const state = useSyncExternalStore(c.store.subscribe, () => c.store.state);
  const [ui, dispatch] = useReducer(reduceUi, initialUi);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [now, setNow] = useState(() => Date.now());
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

  const focusInput = useCallback(() => requestAnimationFrame(() => inputRef.current?.focus()), []);

  const openSession = useCallback(
    (id: string | null) => {
      dispatch({ type: "open", id });
      focusInput();
    },
    [focusInput],
  );

  // A new session is the start screen: the first message makes it.
  const newSession = useCallback(() => openSession(null), [openSession]);

  // Sends from the start screen: makes the session, opens it and sends.
  const start = useCallback(
    async (text: string, images: Image[]): Promise<boolean> => {
      const s = await c.createSession(titleFrom(text) || undefined);
      if (s === null) return false;
      setDrafts((d) => ({ ...d, [NEW]: "" }));
      openSession(s.id);
      const ok = await c.send(s.id, text, images);
      // The session's own composer keeps the text of a failed send.
      if (!ok) setDrafts((d) => ({ ...d, [s.id]: text }));
      return ok;
    },
    [c, openSession],
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
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ui.palette, newSession]);

  const commands = useMemo((): Command[] => {
    const list: Command[] = [
      { id: "new", label: "New session", hint: `${modKey(mac)}N`, run: newSession },
      ...PANELS.map((p) => ({
        id: `panel-${p.panel}`,
        label: `Toggle ${p.label.toLowerCase()}`,
        run: () => dispatch({ type: "panel", panel: p.panel }),
      })),
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
  }, [state.sessions, now, mac, newSession, onDisconnect, openSession]);

  const online = state.link === "online";
  const roster = sid === null ? [] : (state.roster[sid] ?? []);
  const kinds = state.server?.kinds ?? [];
  const project = state.server?.project ?? "";
  const pending = sid === null ? [] : state.approvals.filter((a) => a.session_id === sid);
  const queued = sid === null ? 0 : (state.queue[sid]?.length ?? 0);
  const imagesOn = state.server?.features.includes("images") ?? false;
  const toast = (text: string) => c.store.dispatch({ type: "toast.add", kind: "error", text });

  const agents = (
    <AgentsStrip
      sessionId={sid}
      roster={roster}
      approvals={state.approvals}
      kinds={kinds}
      theme={state.theme}
      onInterrupt={(agent) => sid !== null && void c.interrupt(sid, agent)}
      onAdd={(kind) => {
        setDraft(addAgentMention(draft, kind));
        inputRef.current?.focus();
      }}
      onMention={(agent) => {
        setDraft(applyAgentChip(draft, agent));
        inputRef.current?.focus();
      }}
      listModels={(kind) => (sid === null ? Promise.resolve(null) : c.listModels(sid, kind))}
      setModel={(kind, model, effort) => sid !== null && void c.setModel(sid, kind, model, effort)}
    />
  );

  return (
    <div className="shell flex h-full">
      <Sidebar
        project={project}
        where={where}
        link={state.link}
        sessions={state.sessions}
        openId={ui.openId}
        unread={ui.unread}
        now={now}
        mac={mac}
        onOpen={openSession}
        onNew={newSession}
        onRename={(id, title) => void c.renameSession(id, title)}
        onDelete={(id) => void c.deleteSession(id)}
        onDisconnect={onDisconnect}
        onPalette={() => dispatch({ type: "palette", open: true })}
      />
      <main className="center flex min-w-0 flex-1 flex-col">
        <header className="topbar drag flex h-12 shrink-0 items-center gap-3 border-b px-4">
          <h1 className="title min-w-0 truncate font-medium text-[14px]">
            {open === undefined ? (state.sessionsLoaded ? "New session" : "Deck") : sessionTitle(open)}
          </h1>
          {open?.busy === true && (
            <span className="flex shrink-0 items-center gap-1.5 text-muted-foreground text-xs">
              <span className="size-1.5 animate-deck-pulse rounded-full bg-warning" />
              Working
            </span>
          )}
          <div className="panel-toggles ml-auto flex shrink-0 items-center gap-0.5">
            {PANELS.map((p) => {
              const Icon = PANEL_ICONS[p.panel];
              return (
                <Button
                  key={p.panel}
                  variant="ghost-muted"
                  size="sm"
                  className="font-normal"
                  aria-pressed={ui.panel === p.panel}
                  onClick={() => dispatch({ type: "panel", panel: p.panel })}
                >
                  <Icon />
                  {p.label}
                  {p.panel === "approvals" && state.approvals.length > 0 && (
                    <Badge variant="warning">{state.approvals.length}</Badge>
                  )}
                  {p.panel === "queue" && queued > 0 && <Badge variant="muted">{queued}</Badge>}
                </Button>
              );
            })}
          </div>
        </header>
        {open === undefined ? (
          <div className="welcome flex min-h-0 flex-1 flex-col items-center justify-center gap-8 overflow-y-auto px-6 pb-[12vh]">
            <div className="flex flex-col items-center gap-2 text-center">
              <p className="welcome-title font-semibold text-2xl tracking-tight">
                {state.sessionsLoaded ? (
                  <>
                    What should we build{project === "" ? "" : " in "}
                    {project !== "" && <span className="text-muted-foreground">{project}</span>}?
                  </>
                ) : (
                  "Connecting to Deck…"
                )}
              </p>
              <p className="max-w-md text-muted-foreground text-sm">
                Write a task to start a session. Type <code className="font-mono text-foreground/80">$claude</code>,{" "}
                <code className="font-mono text-foreground/80">$codex</code> or{" "}
                <code className="font-mono text-foreground/80">$codex+</code> to pick who works on it.
              </p>
            </div>
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
              toolbar={agents}
              placeholder="Describe a task. $ picks an agent."
            />
            {state.sessions.length > 0 && (
              <div className="flex w-full max-w-(--chat-max) flex-col gap-1">
                <p className="px-2 font-medium text-2xs text-muted-foreground">Recent</p>
                {state.sessions.slice(0, 4).map((s) => (
                  <button
                    key={s.id}
                    type="button"
                    className="flex cursor-pointer items-center gap-3 rounded-lg px-2 py-1.5 text-left text-[13px] text-foreground/80 hover:bg-accent"
                    onClick={() => openSession(s.id)}
                  >
                    <span className="min-w-0 flex-1 truncate">{sessionTitle(s)}</span>
                    <span className="text-2xs text-muted-foreground">{relativeTime(s.updated_at, now)}</span>
                  </button>
                ))}
              </div>
            )}
          </div>
        ) : (
          <>
            <Timeline
              messages={state.messages[open.id] ?? []}
              roster={roster}
              theme={state.theme}
              more={state.pages[open.id]?.more ?? false}
              onOlder={() => c.loadOlder(open.id)}
            />
            <div className="flex shrink-0 flex-col gap-2 px-6 pb-4">
              {pending.length > 0 && (
                <div className="mx-auto flex w-full max-w-(--chat-max) flex-col gap-2" aria-label="Approvals for this session">
                  {pending.slice(0, 2).map((a) => (
                    <ApprovalCard key={a.id} a={a} onAnswer={(d) => void c.answerApproval(a.session_id, a.id, d)} />
                  ))}
                  {pending.length > 2 && (
                    <Button
                      variant="link"
                      size="xs"
                      className="self-end"
                      onClick={() => dispatch({ type: "panel", panel: "approvals" })}
                    >
                      {pending.length - 2} more waiting
                    </Button>
                  )}
                </div>
              )}
              <div className="mx-auto flex w-full max-w-(--chat-max) items-center gap-2 px-1">
                {agents}
                {queued > 0 && ui.panel !== "queue" && (
                  <Button
                    variant="ghost-muted"
                    size="xs"
                    className="ml-auto rounded-full"
                    onClick={() => dispatch({ type: "panel", panel: "queue" })}
                  >
                    <ListOrdered />
                    {queued} queued
                  </Button>
                )}
              </div>
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
                busy={open.busy}
                onStop={() => void c.cancel(open.id)}
                toolbar={
                  <ModePicker
                    mode={sessionMode(open)}
                    allowYolo={state.server?.allow_yolo ?? false}
                    onSet={(m) => void c.setMode(open.id, m)}
                  />
                }
              />
              <p className="hint text-center text-2xs text-muted-foreground/70">
                <Kbd className="h-4 min-w-4 bg-transparent px-0.5">Enter</Kbd> sends ·{" "}
                <Kbd className="h-4 min-w-4 bg-transparent px-0.5">Shift Enter</Kbd> new line ·{" "}
                <Kbd className="h-4 min-w-4 bg-transparent px-0.5">$</Kbd> picks an agent
              </p>
            </div>
          </>
        )}
      </main>
      {ui.panel !== null && (
        <RightPanel
          c={c}
          state={state}
          panel={ui.panel}
          open={open}
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
              `toast toast-${t.kind} pointer-events-auto rounded-xl border bg-popover px-4 py-3 text-[13px] shadow-xl shadow-black/30`,
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
