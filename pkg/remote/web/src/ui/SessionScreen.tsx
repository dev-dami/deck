import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { formatRoute } from "../route";
import { type AppState, sessionTitle } from "../state";
import { isNearBottom } from "../transcript";
import { AgentsSheet } from "./AgentsSheet";
import { Composer } from "./Composer";
import { Header } from "./Header";
import { navigate, useController } from "./hooks";
import { MessageView } from "./MessageView";
import { ModeButton } from "./ModeSheet";
import { QueueSheet } from "./QueueSheet";
import { Sheet } from "./Sheet";

const EMPTY: never[] = [];

export function SessionScreen({ state, id }: { state: AppState; id: string }) {
  const controller = useController();
  const session = state.sessions.find((s) => s.id === id);
  const messages = state.messages[id] ?? EMPTY;
  const roster = state.roster[id] ?? EMPTY;
  const queue = state.queue[id] ?? EMPTY;
  const page = state.pages[id];
  const loaded = page !== undefined;
  const online = state.link === "online";

  const [sheet, setSheet] = useState<"agents" | "queue" | "menu" | null>(null);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [showJump, setShowJump] = useState(false);

  // Keyed on the session only: a disconnect must not close it, so the resume
  // hello carries the focus and the welcome refetches (R11).
  useEffect(() => {
    void controller.openSession(id);
    return () => controller.closeSession();
  }, [controller, id]);

  // Follow the stream only while the reader is at the bottom; scrolling up to
  // read earlier output must not be yanked back.
  const last = messages[messages.length - 1];
  const tail = `${messages.length}:${last?.updated_at ?? ""}:${last?.parts.length ?? 0}`;
  useLayoutEffect(() => {
    const el = scroller.current;
    if (el !== null && stick.current) el.scrollTop = el.scrollHeight;
  }, [tail, loaded]);

  const onScroll = () => {
    const el = scroller.current;
    if (el === null) return;
    const near = isNearBottom(el);
    stick.current = near;
    setShowJump(!near);
  };

  const jump = () => {
    const el = scroller.current;
    if (el === null) return;
    el.scrollTo({ top: el.scrollHeight, behavior: "smooth" });
    stick.current = true;
    setShowJump(false);
  };

  const loadOlder = async () => {
    const el = scroller.current;
    const before = el?.scrollHeight ?? 0;
    setLoadingOlder(true);
    await controller.loadOlder(id);
    setLoadingOlder(false);
    requestAnimationFrame(() => {
      // Keep the message the reader was looking at in place after the prepend.
      if (el !== null) el.scrollTop += el.scrollHeight - before;
    });
  };

  const needsContinue = !(session?.busy ?? false) && last?.role === "assistant" && !last.finished;

  return (
    <div class="screen session-screen">
      <Header
        title={sessionTitle(session)}
        back={formatRoute({ name: "sessions" })}
        state={state}
        actions={
          <>
            <ModeButton state={state} session={session} />
            <button type="button" class="btn ghost" onClick={() => setSheet("agents")}>
              Agents
            </button>
            <button
              type="button"
              class="btn ghost icon"
              aria-label="Session menu"
              onClick={() => setSheet("menu")}
            >
              <span aria-hidden="true">&middot;&middot;&middot;</span>
            </button>
          </>
        }
      />
      <div class="transcript" ref={scroller} onScroll={onScroll}>
        {page?.more === true && messages.length > 0 ? (
          <button
            type="button"
            class="btn block"
            disabled={loadingOlder || !online}
            onClick={() => void loadOlder()}
          >
            {loadingOlder ? "Loading..." : "Load earlier messages"}
          </button>
        ) : null}
        {!loaded ? <p class="muted empty">Loading...</p> : null}
        {loaded && messages.length === 0 ? (
          <p class="muted empty">No messages yet. Say something below.</p>
        ) : null}
        {messages.map((m) => (
          <MessageView key={m.id} message={m} roster={roster} online={online} />
        ))}
        {needsContinue ? (
          <button
            type="button"
            class="btn block"
            disabled={!online}
            onClick={() => void controller.continueRun(id)}
          >
            Continue
          </button>
        ) : null}
      </div>
      {showJump ? (
        <button type="button" class="jump btn primary" onClick={jump}>
          Latest
        </button>
      ) : null}
      {queue.length > 0 ? (
        <button type="button" class="queue-bar" onClick={() => setSheet("queue")}>
          {queue.length} queued {queue.length === 1 ? "message" : "messages"}
        </button>
      ) : null}
      <Composer
        sessionId={id}
        roster={roster}
        busy={session?.busy ?? false}
        online={online}
        kinds={state.server?.kinds ?? EMPTY}
        theme={state.theme}
        files={state.server?.features.includes("changes") ?? false}
        lead={session?.lead}
        onAgents={() => setSheet("agents")}
        features={state.server?.features ?? EMPTY}
      />
      {sheet === "agents" ? (
        <AgentsSheet
          sessionId={id}
          roster={roster}
          lead={session?.lead?.agent}
          onClose={() => setSheet(null)}
        />
      ) : null}
      {sheet === "queue" ? (
        <QueueSheet sessionId={id} items={queue} onClose={() => setSheet(null)} />
      ) : null}
      {sheet === "menu" ? (
        <SessionMenu id={id} title={sessionTitle(session)} onClose={() => setSheet(null)} />
      ) : null}
    </div>
  );
}

function SessionMenu({ id, title, onClose }: { id: string; title: string; onClose: () => void }) {
  const controller = useController();
  const [name, setName] = useState(title);
  const [confirmDelete, setConfirmDelete] = useState(false);

  if (confirmDelete) {
    return (
      <Sheet title="Delete session?" onClose={() => setConfirmDelete(false)}>
        <p>This removes the session and its history from Deck on your computer.</p>
        <div class="stack">
          <button
            type="button"
            class="btn danger block"
            onClick={async () => {
              if (await controller.deleteSession(id)) {
                onClose();
                navigate(formatRoute({ name: "sessions" }));
              }
            }}
          >
            Delete
          </button>
          <button type="button" class="btn block" onClick={() => setConfirmDelete(false)}>
            Keep
          </button>
        </div>
      </Sheet>
    );
  }

  return (
    <Sheet title="Session" onClose={onClose}>
      <button
        type="button"
        class="btn block"
        onClick={() => {
          onClose();
          navigate(formatRoute({ name: "changes", id }));
        }}
      >
        View file changes
      </button>
      <form
        class="stack"
        onSubmit={async (e) => {
          e.preventDefault();
          const next = name.trim();
          if (next === "") return;
          await controller.renameSession(id, next);
          onClose();
        }}
      >
        <label class="field">
          <span>Title</span>
          <input type="text" value={name} onInput={(e) => setName(e.currentTarget.value)} />
        </label>
        <button type="submit" class="btn primary block" disabled={name.trim() === ""}>
          Rename
        </button>
      </form>
      <button type="button" class="btn danger block" onClick={() => setConfirmDelete(true)}>
        Delete session
      </button>
    </Sheet>
  );
}
