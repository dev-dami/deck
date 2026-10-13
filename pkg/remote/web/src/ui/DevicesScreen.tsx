import { useEffect, useState } from "preact/hooks";
import type { Device } from "../protocol";
import { formatRoute } from "../route";
import type { AppState } from "../state";
import { daysUntil, relativeTime } from "../time";
import { Header } from "./Header";
import { useController, useNow } from "./hooks";

function expiry(expiresAt: string, now: number): string {
  const days = daysUntil(expiresAt, now);
  if (days === null) return "";
  if (days <= 0) return "expired";
  return days === 1 ? "expires tomorrow" : `expires in ${days} days`;
}

function DeviceRow({ device, now }: { device: Device; now: number }) {
  const controller = useController();
  const [confirming, setConfirming] = useState(false);
  const self = device.self === true;
  return (
    <li class="device-row">
      <div class="row-main">
        <span class="row-title">
          {device.name}
          {self ? <span class="tag">this phone</span> : null}
          {device.allow_yolo ? <span class="tag tag-yolo">yolo allowed</span> : null}
        </span>
        <span class="row-sub">
          Last seen {relativeTime(device.last_seen, now) || "never"}
          {" · "}
          {expiry(device.expires_at, now)}
        </span>
      </div>
      {self ? (
        confirming ? (
          <div class="btn-row">
            <button type="button" class="btn danger" onClick={() => void controller.revokeSelf()}>
              Confirm revoke
            </button>
            <button type="button" class="btn ghost" onClick={() => setConfirming(false)}>
              Keep
            </button>
          </div>
        ) : (
          <button type="button" class="btn" onClick={() => setConfirming(true)}>
            Revoke this phone
          </button>
        )
      ) : (
        // R10: other devices are revoked from the desktop only.
        <p class="muted small-text">
          Revoke from the desktop: <code>deck remote revoke {device.id}</code>
        </p>
      )}
    </li>
  );
}

export function DevicesScreen({ state }: { state: AppState }) {
  const controller = useController();
  const now = useNow();
  const online = state.link === "online";
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!online) return;
    let live = true;
    setLoading(true);
    void controller.loadDevices().then(() => {
      if (live) setLoading(false);
    });
    return () => {
      live = false;
    };
  }, [controller, online]);

  const devices = state.devices ?? [];
  return (
    <div class="screen">
      <Header title="Devices" back={formatRoute({ name: "sessions" })} state={state} />
      <main class="content">
        <p class="muted">
          Phones paired with {state.server?.project ?? "this Deck"}
          {state.server?.version !== undefined ? ` (Deck ${state.server.version})` : ""}.
        </p>
        {state.device !== null ? (
          <p class="muted small-text">
            This phone is <strong>{state.device.name}</strong>; its key{" "}
            {expiry(state.device.expires_at, now)} and renews while it is used.
          </p>
        ) : null}
        {loading && state.devices === null ? <p class="muted empty">Loading...</p> : null}
        <ul class="list">
          {devices.map((d) => (
            <DeviceRow key={d.id} device={d} now={now} />
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
          Forgetting clears the saved key here only. Revoke this phone above to also remove it from
          Deck.
        </p>
      </main>
    </div>
  );
}
