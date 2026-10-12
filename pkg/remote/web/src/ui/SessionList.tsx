import { useState } from "preact/hooks";
import { modeLabel } from "../modes";
import { formatRoute } from "../route";
import { type AppState, sessionMode, sessionTitle } from "../state";
import { relativeTime } from "../time";
import { Header } from "./Header";
import { navigate, useController, useNow } from "./hooks";

export function SessionList({ state }: { state: AppState }) {
  const controller = useController();
  const now = useNow();
  const [creating, setCreating] = useState(false);

  const create = async () => {
    if (creating) return;
    setCreating(true);
    const s = await controller.createSession();
    setCreating(false);
    if (s !== null) navigate(formatRoute({ name: "session", id: s.id }));
  };

  return (
    <div class="screen">
      <Header
        title={state.server?.project ?? "Deck"}
        state={state}
        actions={
          <button
            type="button"
            class="btn ghost"
            onClick={() => navigate(formatRoute({ name: "devices" }))}
          >
            Devices
          </button>
        }
      />
      <main class="content">
        <button
          type="button"
          class="btn primary block"
          disabled={creating || state.link !== "online"}
          onClick={create}
        >
          {creating ? "Creating..." : "New session"}
        </button>
        {state.sessions.length === 0 ? (
          <p class="muted empty">
            {state.sessionsLoaded ? "No sessions yet. Start one above." : "Loading sessions..."}
          </p>
        ) : (
          <ul class="list" aria-label="Sessions">
            {state.sessions.map((s) => (
              <li key={s.id}>
                <a class="row" href={formatRoute({ name: "session", id: s.id })}>
                  <span class="row-main">
                    <span class="row-title">
                      {s.busy ? (
                        <span class="busy-dot" role="img" aria-label="Working" title="Working" />
                      ) : null}
                      <span class="ellipsis">{sessionTitle(s)}</span>
                    </span>
                    {s.last_text !== undefined && s.last_text !== "" ? (
                      <span class="row-sub ellipsis">{s.last_text}</span>
                    ) : null}
                  </span>
                  <span class="row-meta">
                    {s.pending > 0 ? (
                      <span class="badge" role="img" aria-label={`${s.pending} pending approvals`}>
                        {s.pending}
                      </span>
                    ) : null}
                    <span class={`tag mode-${sessionMode(s) ?? "unknown"}`}>
                      {modeLabel(sessionMode(s))}
                    </span>
                    <span class="muted small-text">{relativeTime(s.updated_at, now)}</span>
                  </span>
                </a>
              </li>
            ))}
          </ul>
        )}
      </main>
    </div>
  );
}
