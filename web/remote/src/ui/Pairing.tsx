import { useState } from "preact/hooks";
import type { AppState } from "../state";
import { useController } from "./hooks";

export function Pairing({ state }: { state: AppState }) {
  const controller = useController();
  const [code, setCode] = useState("");
  const busy = state.auth === "pairing";

  const submit = (e: Event) => {
    e.preventDefault();
    const trimmed = code.trim();
    if (trimmed === "" || busy) return;
    void controller.pairWith(trimmed);
  };

  return (
    <main class="screen pair">
      <div class="pair-card">
        <div class="logo" aria-hidden="true" />
        <h1>Pair with Deck</h1>
        {busy ? (
          <p class="muted" role="status">
            Pairing this phone...
          </p>
        ) : (
          <>
            <p class="muted">
              Run <code>deck serve</code> on your computer and scan the QR code it shows with this
              phone's camera. Or paste the pairing code below.
            </p>
            {state.authMessage !== null ? (
              <p class="error-text" role="alert">
                {state.authMessage}
              </p>
            ) : null}
            <form onSubmit={submit} class="stack">
              <label class="field">
                <span>Pairing code</span>
                <input
                  type="text"
                  inputMode="text"
                  autoComplete="off"
                  autoCapitalize="none"
                  autoCorrect="off"
                  spellcheck={false}
                  value={code}
                  onInput={(e) => setCode(e.currentTarget.value)}
                  placeholder="Paste code"
                />
              </label>
              <button type="submit" class="btn primary block" disabled={code.trim() === ""}>
                Pair
              </button>
            </form>
          </>
        )}
      </div>
    </main>
  );
}
