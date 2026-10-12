import { useEffect } from "preact/hooks";
import { gateMessage } from "../push";
import type { AppState } from "../state";
import { useController } from "./hooks";

export function NotificationsPanel({ state }: { state: AppState }) {
  const controller = useController();
  const push = controller.push;
  const ui = state.push;
  const online = state.link === "online";

  // Permission and support can change while the app sits in the background, and
  // the server's view is only known once connected.
  useEffect(() => {
    if (online) void push?.sync();
    else push?.refresh();
  }, [push, online]);

  if (push === null) {
    return (
      <section class="settings-section" aria-labelledby="notif-h">
        <h2 id="notif-h">Notifications</h2>
        <p class="muted">This browser does not support push notifications.</p>
      </section>
    );
  }

  const blocked = ui.gate !== "ok";
  // A phone that was subscribed before the browser blocked notifications still
  // needs a way to clear the server's record.
  const canDisable = ui.enabled;

  return (
    <section class="settings-section" aria-labelledby="notif-h">
      <h2 id="notif-h">Notifications</h2>
      {!ui.checked ? <p class="muted">Checking...</p> : null}
      {ui.checked && blocked ? <p class="muted">{gateMessage(ui.gate)}</p> : null}
      {ui.checked && !blocked && !ui.enabled ? (
        <>
          <p class="muted">
            Get a notification on this phone when an agent needs approval or finishes, while Deck is
            closed.
          </p>
          <button
            type="button"
            class="btn primary block"
            disabled={ui.busy || !online}
            onClick={() => void push.enable()}
          >
            {ui.busy ? "Enabling..." : "Enable notifications"}
          </button>
          {!online ? <p class="muted small-text">Connect to Deck to turn this on.</p> : null}
        </>
      ) : null}
      {ui.enabled ? (
        <div class="stack">
          <label class="toggle">
            <input
              type="checkbox"
              checked={ui.approvals}
              disabled={ui.busy || !online || blocked}
              onChange={(e) =>
                void push.setPrefs({ approvals: e.currentTarget.checked, done: ui.done })
              }
            />
            <span>Approval requests</span>
          </label>
          <label class="toggle">
            <input
              type="checkbox"
              checked={ui.done}
              disabled={ui.busy || !online || blocked}
              onChange={(e) =>
                void push.setPrefs({ approvals: ui.approvals, done: e.currentTarget.checked })
              }
            />
            <span>Agent finished</span>
          </label>
          <div class="btn-row">
            <button
              type="button"
              class="btn"
              disabled={ui.busy || !online || blocked}
              onClick={() => void push.test()}
            >
              Send test
            </button>
            {canDisable ? (
              <button
                type="button"
                class="btn danger"
                disabled={ui.busy || !online}
                onClick={() => void push.disable()}
              >
                Disable
              </button>
            ) : null}
          </div>
        </div>
      ) : null}
    </section>
  );
}
