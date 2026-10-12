import { useState } from "preact/hooks";
import type { Device } from "../protocol";
import { formatRoute } from "../route";
import type { AppState } from "../state";
import { relativeTime } from "../time";
import { Header } from "./Header";
import { useController, useLoad, useNow } from "./hooks";
import { NotificationsPanel } from "./NotificationsPanel";

export function DevicesScreen({ state }: { state: AppState }) {
  const controller = useController();
  const now = useNow();
  const online = state.link === "online";
  const devices = useLoad<Device[]>(
    () => (online ? controller.devices() : Promise.resolve(null)),
    String(online),
  );
  const [confirming, setConfirming] = useState<string | null>(null);

  const revoke = async (d: Device) => {
    const ok = await controller.revokeDevice(d.id);
    setConfirming(null);
    // Revoking this phone ends the session server-side and returns the app to pairing.
    if (ok) devices.reload();
  };

  return (
    <div class="screen">
      <Header title="Settings" back={formatRoute({ name: "sessions" })} state={state} />
      <main class="content">
        <NotificationsPanel state={state} />
        <h2>Devices</h2>
        <p class="muted">
          Phones paired with {state.server?.project ?? "this Deck"}. Revoke a phone to sign it out.
        </p>
        {devices.loading ? <p class="muted empty">Loading...</p> : null}
        <ul class="list">
          {(devices.data ?? []).map((d) => (
            <li key={d.id} class="device-row">
              <div class="row-main">
                <span class="row-title">
                  {d.name}
                  {d.current === true || d.id === state.device?.id ? (
                    <span class="tag">this phone</span>
                  ) : null}
                </span>
                <span class="row-sub">Last seen {relativeTime(d.last_seen, now) || "never"}</span>
              </div>
              {confirming === d.id ? (
                <div class="btn-row">
                  <button type="button" class="btn danger" onClick={() => void revoke(d)}>
                    Confirm revoke
                  </button>
                  <button type="button" class="btn ghost" onClick={() => setConfirming(null)}>
                    Keep
                  </button>
                </div>
              ) : (
                <button type="button" class="btn" onClick={() => setConfirming(d.id)}>
                  Revoke
                </button>
              )}
            </li>
          ))}
        </ul>
        <button
          type="button"
          class="btn block"
          onClick={() => controller.forget("This phone was unpaired.")}
        >
          Forget this phone locally
        </button>
        <p class="muted small-text">
          Forgetting clears the saved token here only. Revoke the device above to also remove it
          from Deck.
        </p>
      </main>
    </div>
  );
}
