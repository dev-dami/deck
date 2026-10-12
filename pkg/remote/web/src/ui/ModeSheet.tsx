import { useState } from "preact/hooks";
import { availableModes, modeLabel, needsConfirm } from "../modes";
import type { Mode, Session } from "../protocol";
import { type AppState, sessionMode } from "../state";
import { useController } from "./hooks";
import { Sheet } from "./Sheet";

// Mode is per session in v2 (§3.2).
export function ModeButton({ state, session }: { state: AppState; session: Session | undefined }) {
  const [open, setOpen] = useState(false);
  const mode = sessionMode(session);
  return (
    <>
      <button
        type="button"
        class={`btn chip mode-${mode ?? "unknown"}`}
        disabled={session === undefined || state.link !== "online"}
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
      >
        {modeLabel(mode)}
      </button>
      {open && session !== undefined ? (
        <ModeSheet state={state} session={session} onClose={() => setOpen(false)} />
      ) : null}
    </>
  );
}

function ModeSheet({
  state,
  session,
  onClose,
}: {
  state: AppState;
  session: Session;
  onClose: () => void;
}) {
  const controller = useController();
  const [pendingYolo, setPendingYolo] = useState<Mode | null>(null);
  const [busy, setBusy] = useState(false);
  const allowYolo = state.server?.allow_yolo === true;
  const modes = availableModes(allowYolo);
  const current = sessionMode(session);

  const apply = async (mode: Mode) => {
    setBusy(true);
    const ok = await controller.setMode(session.id, mode);
    setBusy(false);
    if (ok) onClose();
  };

  const choose = (mode: Mode) => {
    if (mode === current) return;
    if (needsConfirm(mode)) setPendingYolo(mode);
    else void apply(mode);
  };

  if (pendingYolo !== null) {
    return (
      <Sheet title="Enable yolo mode?" onClose={() => setPendingYolo(null)}>
        <p>
          Agents in this session will run commands and edit files without asking you first, until
          you switch back.
        </p>
        <div class="stack">
          <button
            type="button"
            class="btn danger block"
            disabled={busy}
            onClick={() => void apply(pendingYolo)}
          >
            Yes, enable yolo
          </button>
          <button type="button" class="btn block" onClick={() => setPendingYolo(null)}>
            Cancel
          </button>
        </div>
      </Sheet>
    );
  }

  return (
    <Sheet title="Mode" onClose={onClose}>
      <ul class="list">
        {modes.map((m) => (
          <li key={m.mode}>
            <button
              type="button"
              class={`row option ${m.mode === current ? "selected" : ""}`}
              aria-pressed={m.mode === current}
              disabled={busy}
              onClick={() => choose(m.mode)}
            >
              <span class="row-main">
                <span class="row-title">{m.label}</span>
                <span class="row-sub">{m.blurb}</span>
              </span>
            </button>
          </li>
        ))}
      </ul>
      {allowYolo ? null : (
        <p class="muted small-text">
          Yolo is off for this phone. Allow it from the desktop with{" "}
          <code>deck remote devices --allow-yolo {state.device?.id ?? "<id>"}</code>.
        </p>
      )}
    </Sheet>
  );
}
