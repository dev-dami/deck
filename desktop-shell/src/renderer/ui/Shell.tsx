import type { Controller } from "@web/controller";
import { availableModes, needsConfirm } from "@web/modes";
import type { Image, Mode, Session } from "@web/protocol";
import { sessionMode, sessionTitle } from "@web/state";
import { relativeTime } from "@web/time";
import { useCallback, useEffect, useMemo, useReducer, useRef, useState, useSyncExternalStore } from "react";
import type { SavedServer } from "../../shared/bridge";
import { addAgentMention } from "../completion";
import { initialUi, PANELS, reduceUi } from "../ui";
import { AgentsStrip } from "./AgentsStrip";
import { Composer } from "./Composer";
import { type Command, Palette } from "./Palette";
import { RightPanel } from "./RightPanel";
import { Sidebar } from "./Sidebar";
import { Timeline } from "./Timeline";

function ModeSwitch({
  mode,
  allowYolo,
  onSet,
}: {
  mode: Mode | null;
  allowYolo: boolean;
  onSet: (m: Mode) => void;
}) {
  return (
    <div className="modes" role="radiogroup" aria-label="Mode">
      {availableModes(allowYolo).map((m) => (
        <button
          key={m.mode}
          type="button"
          role="radio"
          aria-checked={mode === m.mode}
          title={m.blurb}
          className={`mode mode-${m.mode}`}
          onClick={() => {
            if (mode === m.mode) return;
            if (needsConfirm(m.mode) && !window.confirm(`${m.blurb} Switch to ${m.label}?`)) return;
            onSet(m.mode);
          }}
        >
          {m.label}
        </button>
      ))}
    </div>
  );
}

export function Shell({ c, server, onDisconnect }: { c: Controller; server: SavedServer; onDisconnect: () => void }) {
  const state = useSyncExternalStore(c.store.subscribe, () => c.store.state);
  const [ui, dispatch] = useReducer(reduceUi, initialUi);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [now, setNow] = useState(() => Date.now());
  const inputRef = useRef<HTMLTextAreaElement>(null);

  const open: Session | undefined = state.sessions.find((s) => s.id === ui.openId);
  const sid = open?.id ?? null;
  const draft = sid === null ? "" : (drafts[sid] ?? "");
  const setDraft = useCallback(
    (text: string) => {
      if (sid !== null) setDrafts((d) => ({ ...d, [sid]: text }));
    },
    [sid],
  );

  useEffect(() => {
    if (state.sessionsLoaded) dispatch({ type: "sessions", sessions: state.sessions });
  }, [state.sessions, state.sessionsLoaded]);

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

  const openSession = useCallback((id: string) => {
    dispatch({ type: "open", id });
    requestAnimationFrame(() => inputRef.current?.focus());
  }, []);

  const newSession = useCallback(async () => {
    const s = await c.createSession();
    if (s !== null) openSession(s.id);
  }, [c, openSession]);

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (!(e.ctrlKey || e.metaKey) || e.altKey || e.shiftKey) return;
      const k = e.key.toLowerCase();
      if (k === "k") {
        e.preventDefault();
        dispatch({ type: "palette", open: !ui.palette });
      } else if (k === "n") {
        e.preventDefault();
        void newSession();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [ui.palette, newSession]);

  const commands = useMemo((): Command[] => {
    const list: Command[] = [
      { id: "new", label: "New session", hint: "Ctrl N", run: () => void newSession() },
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
  }, [state.sessions, now, newSession, onDisconnect, openSession]);

  const online = state.link === "online";
  const roster = sid === null ? [] : (state.roster[sid] ?? []);
  const kinds = state.server?.kinds ?? [];
  const where = `${server.host.includes(":") ? `[${server.host}]` : server.host}:${server.port}`;

  return (
    <div className={`shell${ui.panel === null ? "" : " with-panel"}`}>
      <Sidebar
        project={state.server?.project ?? ""}
        where={where}
        link={state.link}
        sessions={state.sessions}
        openId={ui.openId}
        unread={ui.unread}
        now={now}
        onOpen={openSession}
        onNew={() => void newSession()}
        onRename={(id, title) => void c.renameSession(id, title)}
        onDelete={(id) => void c.deleteSession(id)}
        onDisconnect={onDisconnect}
      />
      <main className="center">
        <header className="topbar">
          <h1 className="title">{open === undefined ? "Deck" : sessionTitle(open)}</h1>
          {open !== undefined && (
            <ModeSwitch
              mode={sessionMode(open)}
              allowYolo={state.server?.allow_yolo ?? false}
              onSet={(m) => void c.setMode(open.id, m)}
            />
          )}
          {open?.busy === true && (
            <button type="button" className="stop-all" onClick={() => void c.cancel(open.id)}>
              Stop all
            </button>
          )}
          <div className="panel-toggles">
            {PANELS.map((p) => (
              <button
                key={p.panel}
                type="button"
                aria-pressed={ui.panel === p.panel}
                onClick={() => dispatch({ type: "panel", panel: p.panel })}
              >
                {p.label}
                {p.panel === "approvals" && state.approvals.length > 0 && (
                  <span className="badge">{state.approvals.length}</span>
                )}
                {p.panel === "queue" && sid !== null && (state.queue[sid]?.length ?? 0) > 0 && (
                  <span className="badge quiet">{state.queue[sid]?.length}</span>
                )}
              </button>
            ))}
          </div>
        </header>
        {open === undefined ? (
          <div className="welcome">
            <p className="welcome-title">{state.sessionsLoaded ? "Pick a session" : "Connecting to Deck…"}</p>
            <p className="muted">
              Or start a new one with <kbd>Ctrl N</kbd>. Many agents can work in one session: type{" "}
              <code>$claude</code>, <code>$codex</code>, or <code>$codex+</code> for another one.
            </p>
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
            <AgentsStrip
              sessionId={open.id}
              roster={roster}
              approvals={state.approvals}
              kinds={kinds}
              theme={state.theme}
              onInterrupt={(agent) => void c.interrupt(open.id, agent)}
              onAdd={(kind) => {
                setDraft(addAgentMention(draft, kind));
                inputRef.current?.focus();
              }}
              listModels={(kind) => c.listModels(open.id, kind)}
              setModel={(kind, model, effort) => void c.setModel(open.id, kind, model, effort)}
            />
            <Composer
              key={open.id}
              draft={draft}
              setDraft={setDraft}
              roster={roster}
              kinds={kinds}
              images={state.server?.features.includes("images") ?? false}
              online={online}
              onSend={(text: string, images: Image[]) => c.send(open.id, text, images)}
              onError={(text) => c.store.dispatch({ type: "toast.add", kind: "error", text })}
              inputRef={inputRef}
            />
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
      <div className="toasts" aria-live="polite">
        {state.toasts.map((t) => (
          <div key={t.id} className={`toast toast-${t.kind}`}>
            {t.text}
          </div>
        ))}
      </div>
    </div>
  );
}
