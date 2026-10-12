import { useState } from "preact/hooks";
import { availableModes, modeLabel, needsConfirm } from "../modes";
import type { Mode } from "../protocol";
import type { AppState } from "../state";
import { useController } from "./hooks";
import { Sheet } from "./Sheet";

export function ModeButton({ state }: { state: AppState }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button
        type="button"
        class={`btn chip mode-${state.mode ?? "unknown"}`}
        disabled={state.mode === null}
        onClick={() => setOpen(true)}
        aria-haspopup="dialog"
      >
        {modeLabel(state.mode)}
      </button>
      {open ? <ModeSheet state={state} onClose={() => setOpen(false)} /> : null}
    </>
  );
}

function ModeSheet({ state, onClose }: { state: AppState; onClose: () => void }) {
  const controller = useController();
  const [pendingYolo, setPendingYolo] = useState<Mode | null>(null);
  const [busy, setBusy] = useState(false);
  const modes = availableModes(state.server?.allow_yolo === true);

  const apply = async (mode: Mode) => {
    setBusy(true);
    const ok = await controller.setMode(mode);
    setBusy(false);
    if (ok) onClose();
  };

  const choose = (mode: Mode) => {
    if (mode === state.mode) return;
    if (needsConfirm(mode)) setPendingYolo(mode);
    else void apply(mode);
  };

  if (pendingYolo !== null) {
    return (
      <Sheet title="Enable yolo mode?" onClose={() => setPendingYolo(null)}>
        <p>
          Agents will run commands and edit files without asking you first, in every session, until
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
              class={`row option ${m.mode === state.mode ? "selected" : ""}`}
              aria-pressed={m.mode === state.mode}
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
      {state.server?.allow_yolo === true ? null : (
        <p class="muted small-text">
          Yolo is disabled. Start <code>deck serve --allow-yolo</code> to offer it here.
        </p>
      )}
    </Sheet>
  );
}
