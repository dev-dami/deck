import type { ComponentChildren } from "preact";
import { useEffect, useLayoutEffect, useRef, useState } from "preact/hooks";
import { ChatSession, newIdempotencyKey } from "../chat3";
import type { ModelChoice } from "../protocol";
import type { SessionLike } from "../state3";
import { isNearBottom } from "../transcript";
import { DiffView } from "../ui/DiffView";
import { ChatTranscript } from "./ChatTranscript";

export interface ChatScreenProps {
  session: SessionLike | null;
  pj: string;
  sessionId: string;
  title?: string;
  online: boolean;
  onBack: () => void;
  onOpenSession: (sessionId: string) => void;
  onQueuePrompt?: (text: string) => Promise<void>;
  children?: ComponentChildren;
}

export function ChatScreen(props: ChatScreenProps) {
  const { session, pj, sessionId, online, onBack, onOpenSession, onQueuePrompt } = props;
  const [chat, setChat] = useState<ChatSession | null>(null);
  const [, update] = useState(0);
  const [error, setError] = useState<string | null>(null);
  const [pending, setPending] = useState(false);
  const actionLock = useRef(false);
  const [draft, setDraft] = useState("");
  const [retry, setRetry] = useState(0);
  const [models, setModels] = useState<ModelChoice[]>([]);
  const [kind, setKind] = useState("");
  const [model, setModel] = useState("");
  const [effort, setEffort] = useState("");
  const scroller = useRef<HTMLElement>(null);
  const follow = useRef(true);
  const sendKey = useRef<{ text: string; key: string } | null>(null);
  const generation = useRef(0);

  useEffect(() => {
    generation.current++;
    actionLock.current = false;
    setPending(false);
    setModels([]);
    setKind("");
    setModel("");
    setEffort("");
    let disposed = false;
    let current: ChatSession | null = null;
    let unsubscribe: (() => void) | undefined;
    setChat((previous) =>
      !online && previous?.pj === pj && previous.sessionId === sessionId ? previous : null,
    );
    setError(null);
    if (session !== null && online) {
      void ChatSession.open(session, pj, sessionId)
        .then((opened) => {
          if (disposed) {
            opened.detach();
            return;
          }
          current = opened;
          unsubscribe = opened.subscribe(() => update((n) => n + 1));
          setChat(opened);
        })
        .catch((err: unknown) => {
          if (!disposed) setError(errorText(err));
        });
    }
    return () => {
      generation.current++;
      disposed = true;
      unsubscribe?.();
      current?.detach();
    };
  }, [session, pj, sessionId, online, retry]);

  const last = chat?.messages[chat.messages.length - 1];
  const tail = `${chat?.messages.length}:${last?.updated_at}:${last?.parts.length}`;
  useLayoutEffect(() => {
    if (follow.current && scroller.current !== null)
      scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [tail]);

  async function perform(action: () => Promise<unknown>): Promise<boolean> {
    if (actionLock.current) return false;
    actionLock.current = true;
    const started = generation.current;
    setPending(true);
    setError(null);
    try {
      await action();
      return started === generation.current;
    } catch (err) {
      if (started === generation.current) setError(errorText(err));
      return false;
    } finally {
      if (started === generation.current) {
        actionLock.current = false;
        setPending(false);
      }
    }
  }

  const canAct = online && chat !== null && !chat.resyncing && !pending;
  const visibleError = error ?? chat?.error ?? null;
  const busy = chat?.busy === true;
  const selected = models.find((choice) => choice.id === model);

  async function send(): Promise<void> {
    const text = draft.trim();
    if (text === "" || pending) return;
    if (!online) {
      if (onQueuePrompt !== undefined && (await perform(() => onQueuePrompt(text)))) setDraft("");
      return;
    }
    if (chat === null || chat.resyncing) return;
    // A manual retry after a transport timeout must preserve the key too.
    if (sendKey.current?.text !== text) sendKey.current = { text, key: newIdempotencyKey() };
    const key = sendKey.current.key;
    if (await perform(() => chat.send(text, key))) {
      setDraft("");
      sendKey.current = null;
    }
  }

  async function chooseKind(next: string): Promise<void> {
    setKind(next);
    setModels([]);
    setModel("");
    setEffort("");
    if (chat === null || next === "") return;
    const started = generation.current;
    await perform(async () => {
      const choices = await chat.models(next);
      if (started !== generation.current) return;
      setModels(choices);
      setModel(choices.find((choice) => choice.selected)?.id ?? choices[0]?.id ?? "");
    });
  }

  return (
    <div class="screen session-screen">
      <header class="topbar">
        <button type="button" class="btn ghost" onClick={onBack}>
          ← Inbox
        </button>
        <div class="topbar-title">
          <h1>{props.title ?? "Session"}</h1>
          <p class="muted small-text" role="status">
            {online ? (busy ? "Running" : "Connected") : "Offline"}
          </p>
        </div>
      </header>
      {visibleError !== null ? (
        <div role="alert">
          <p>{visibleError}</p>
          {chat === null && online ? (
            <button type="button" class="btn" onClick={() => setRetry((n) => n + 1)}>
              Retry transcript
            </button>
          ) : null}
          {chat !== null && chat.error !== null ? (
            <button
              type="button"
              class="btn"
              disabled={pending}
              onClick={() => void perform(() => chat.refresh())}
            >
              Retry refresh
            </button>
          ) : null}
        </div>
      ) : null}
      {props.children}
      <fieldset class="btn-row" aria-label="Session actions">
        <button
          type="button"
          class="btn small"
          disabled={!canAct}
          onClick={() =>
            void perform(async () => {
              if (chat !== null) onOpenSession(await chat.createSession());
            })
          }
        >
          New session
        </button>
        <button
          type="button"
          class="btn small"
          disabled={!canAct || busy}
          onClick={() => void perform(() => chat?.cont() ?? Promise.resolve())}
        >
          Continue
        </button>
        <button
          type="button"
          class="btn small"
          disabled={!canAct || !busy}
          onClick={() => void perform(() => chat?.interrupt() ?? Promise.resolve())}
        >
          Interrupt
        </button>
        <button
          type="button"
          class="btn small"
          disabled={!canAct || !busy}
          onClick={() => void perform(() => chat?.cancel() ?? Promise.resolve())}
        >
          Cancel run
        </button>
      </fieldset>
      <section
        class="transcript"
        ref={scroller}
        onScroll={() => {
          if (scroller.current !== null) follow.current = isNearBottom(scroller.current);
        }}
        aria-label="Messages"
      >
        {chat === null ? (
          <p class="muted empty">
            {online ? "Loading transcript…" : "Reconnect to load this conversation."}
          </p>
        ) : (
          <>
            {chat.more ? (
              <button
                type="button"
                class="btn block"
                disabled={!canAct}
                onClick={() => {
                  const el = scroller.current;
                  const height = el?.scrollHeight ?? 0;
                  follow.current = false;
                  void perform(() => chat.loadOlder()).then(() =>
                    requestAnimationFrame(() => {
                      if (el !== null) el.scrollTop += el.scrollHeight - height;
                    }),
                  );
                }}
              >
                Load earlier messages
              </button>
            ) : null}
            {chat.messages.length === 0 ? (
              <p class="muted empty">No messages yet. Send a prompt below.</p>
            ) : null}
            <ChatTranscript messages={chat.messages} />
          </>
        )}
      </section>
      <details class="tool" style={{ flex: "none", maxHeight: "30vh", overflowY: "auto" }}>
        <summary>Agents and models</summary>
        <ul>
          {chat?.roster.map((agent) => (
            <li key={agent.id}>
              {agent.name} · {agent.model ?? agent.kind} · {agent.busy ? "running" : "idle"}
            </li>
          ))}
        </ul>
        <label class="field">
          Agent kind
          <select
            disabled={!canAct}
            value={kind}
            onChange={(event) => void chooseKind(event.currentTarget.value)}
          >
            <option value="">Choose an agent</option>
            {[...new Set(chat?.roster.map((agent) => agent.kind) ?? [])].map((value) => (
              <option key={value} value={value}>
                {value}
              </option>
            ))}
          </select>
        </label>
        {models.length > 0 ? (
          <form
            class="stack"
            onSubmit={(event) => {
              event.preventDefault();
              void perform(() => chat?.setModel(kind, model, effort) ?? Promise.resolve());
            }}
          >
            <label class="field">
              Model
              <select
                disabled={!canAct}
                value={model}
                onChange={(event) => {
                  setModel(event.currentTarget.value);
                  setEffort("");
                }}
              >
                {models.map((choice) => (
                  <option key={choice.id} value={choice.id}>
                    {choice.label}
                  </option>
                ))}
              </select>
            </label>
            {selected?.efforts?.length ? (
              <label class="field">
                Reasoning effort
                <select
                  disabled={!canAct}
                  value={effort}
                  onChange={(event) => setEffort(event.currentTarget.value)}
                >
                  <option value="">Default</option>
                  {selected.efforts.map((value) => (
                    <option key={value} value={value}>
                      {value}
                    </option>
                  ))}
                </select>
              </label>
            ) : null}
            <button type="submit" class="btn" disabled={!canAct || model === ""}>
              Set model
            </button>
          </form>
        ) : null}
      </details>
      <details class="tool" style={{ flex: "none", maxHeight: "30vh", overflowY: "auto" }}>
        <summary>File changes ({chat?.changes.length ?? 0})</summary>
        <p class="muted small-text">Read-only summaries. Full changes remain on your computer.</p>
        <button
          type="button"
          class="btn small"
          disabled={!canAct}
          onClick={() => void perform(() => chat?.refreshChanges() ?? Promise.resolve())}
        >
          Refresh changes
        </button>
        {chat?.changes.length === 0 ? <p>No file changes.</p> : null}
        {chat?.changes.map((file) => (
          <details key={file.path} class="file-card">
            <summary>
              {file.path} <span class="added">+{file.added}</span>{" "}
              <span class="removed">−{file.removed}</span>
            </summary>
            {file.diff ? (
              <DiffView diff={file.diff} truncated={file.diff_truncated === true} />
            ) : (
              <p>No diff preview available.</p>
            )}
          </details>
        ))}
      </details>
      <form
        class="composer"
        onSubmit={(event) => {
          event.preventDefault();
          void send();
        }}
      >
        <label class="field">
          {online ? "Message" : "Queue a prompt"}
          <textarea
            rows={3}
            value={draft}
            placeholder="Ask your agents…"
            onInput={(event) => setDraft(event.currentTarget.value)}
            disabled={pending || (!online && onQueuePrompt === undefined)}
          />
        </label>
        <button
          type="submit"
          class="btn primary"
          disabled={
            pending ||
            draft.trim() === "" ||
            (online ? chat === null || chat.resyncing : onQueuePrompt === undefined)
          }
        >
          {pending ? "Sending…" : online ? "Send" : "Queue prompt"}
        </button>
      </form>
    </div>
  );
}

function errorText(error: unknown): string {
  return error instanceof Error ? error.message : "The action could not be completed.";
}
