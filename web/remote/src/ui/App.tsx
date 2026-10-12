import { useEffect } from "preact/hooks";
import type { AppState } from "../state";
import { ApprovalSheet } from "./ApprovalSheet";
import { ChangesScreen } from "./ChangesScreen";
import { DevicesScreen } from "./DevicesScreen";
import { useAppState, useController, useRoute } from "./hooks";
import { Pairing } from "./Pairing";
import { SessionList } from "./SessionList";
import { SessionScreen } from "./SessionScreen";

const TOAST_MS = 6000;

function Toasts({ state }: { state: AppState }) {
  const controller = useController();
  const first = state.notices[0];
  const firstId = first?.id;
  useEffect(() => {
    if (firstId === undefined) return;
    const t = setTimeout(
      () => controller.store.dispatch({ type: "notice.dismiss", id: firstId }),
      TOAST_MS,
    );
    return () => clearTimeout(t);
  }, [controller, firstId]);
  if (state.notices.length === 0) return null;
  return (
    <div class="toasts" role="status" aria-live="polite">
      {state.notices.map((n) => (
        <button
          key={n.id}
          type="button"
          class={`toast toast-${n.kind}`}
          onClick={() => controller.store.dispatch({ type: "notice.dismiss", id: n.id })}
        >
          {n.text}
        </button>
      ))}
    </div>
  );
}

function Banner({ state }: { state: AppState }) {
  if (state.link === "online") return null;
  return (
    <div class="banner" role="status">
      {state.link === "connecting" ? "Connecting to Deck..." : "Offline. Reconnecting..."}
    </div>
  );
}

export function App() {
  const state = useAppState();
  const route = useRoute();

  if (state.auth !== "paired") return <Pairing state={state} />;

  let screen = <SessionList state={state} />;
  if (route.name === "session")
    screen = <SessionScreen key={route.id} state={state} id={route.id} />;
  else if (route.name === "changes") screen = <ChangesScreen state={state} id={route.id} />;
  else if (route.name === "devices") screen = <DevicesScreen state={state} />;

  return (
    <>
      <Banner state={state} />
      {screen}
      <ApprovalSheet state={state} />
      <Toasts state={state} />
    </>
  );
}
