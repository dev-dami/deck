import type { JSX } from "preact";
import { useState } from "preact/hooks";
import type { QRBlob } from "../protocol3";
import type { ComputerRecord } from "../store/computers";
import { pairWithComputer } from "./pairing";

export function PairingScreen(props: {
  blob: QRBlob;
  onPaired: (record: ComputerRecord) => void;
  onCancel: () => void;
}): JSX.Element {
  const [name, setName] = useState("My phone");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  return (
    <main class="hosted-screen">
      <h1>Pair {props.blob.name}</h1>
      <p>Pair only if you opened this code from a computer you trust.</p>
      <form
        onSubmit={(event) => {
          event.preventDefault();
          if (busy || !name.trim()) return;
          setBusy(true);
          setError(null);
          void pairWithComputer({ blob: props.blob, deviceName: name.trim() })
            .then(props.onPaired)
            .catch((err: unknown) => {
              setError(err instanceof Error ? err.message : "Pairing failed.");
              setBusy(false);
            });
        }}
      >
        <label>
          Phone name
          <input
            value={name}
            maxLength={80}
            disabled={busy}
            onInput={(e) => setName(e.currentTarget.value)}
          />
        </label>
        <button type="submit" disabled={busy || !name.trim()}>
          {busy ? "Pairing…" : "Pair computer"}
        </button>
        <button type="button" disabled={busy} onClick={props.onCancel}>
          Cancel
        </button>
      </form>
      {error && <p role="alert">{error}</p>}
    </main>
  );
}
