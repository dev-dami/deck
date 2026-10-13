import type { ComponentChildren } from "preact";
import { useState } from "preact/hooks";
import { fingerprintHex, inviteOrigin, type PairOffer } from "../auth";
import type { AppState } from "../state";
import { useController, useNow } from "./hooks";

function Shell({ children }: { children: ComponentChildren }) {
  return (
    <main class="screen pair">
      <div class="pair-card">
        <div class="logo" aria-hidden="true" />
        {children}
      </div>
    </main>
  );
}

// R9: the fingerprint of the self-signed certificate is shown and the person
// is asked to compare it with the browser's warning before the code is sent.
function Offer({ state, offer }: { state: AppState; offer: PairOffer }) {
  const controller = useController();
  const [checked, setChecked] = useState(false);
  const busy = state.auth === "pairing";
  const now = useNow(1000);
  // A 429 Retry-After keeps the button off until the server will accept a retry.
  const waitS =
    state.pairRetryAt === null ? 0 : Math.max(0, Math.ceil((state.pairRetryAt - now) / 1000));
  const origin = inviteOrigin(offer, location);
  const hex = offer.fingerprint === null ? null : fingerprintHex(offer.fingerprint);
  const secure = location.protocol === "https:";
  const expired = offer.expires !== null && offer.expires * 1000 < Date.now();

  if (!origin.matches && origin.url !== null) {
    return (
      <Shell>
        <h1>Open Deck's address</h1>
        <p class="muted">
          This invite is for{" "}
          <code>
            {offer.host}:{offer.port}
          </code>
          , but this page came from <code>{location.host}</code>. Pairing only works on the page
          Deck serves.
        </p>
        <a class="btn primary block" href={origin.url}>
          Open {offer.host}:{offer.port}
        </a>
        <button type="button" class="btn block" onClick={() => controller.cancelOffer()}>
          Back
        </button>
      </Shell>
    );
  }

  return (
    <Shell>
      <h1>{offer.name !== null ? `Pair with ${offer.name}` : "Pair with Deck"}</h1>
      {expired ? (
        <p class="error-text" role="alert">
          This invite has expired. Run <code>deck remote pair</code> again for a new one.
        </p>
      ) : null}
      {state.authMessage !== null ? (
        <p class="error-text" role="alert">
          {state.authMessage}
        </p>
      ) : null}
      {offer.fingerprint !== null ? (
        <>
          <p>
            Before pairing, compare this certificate fingerprint with the one your browser warned
            about when you opened this page. They must match exactly; if they do not, someone may be
            intercepting the connection.
          </p>
          <p class="fingerprint">{hex ?? offer.fingerprint}</p>
          {hex !== null ? <p class="muted small-text mono">{offer.fingerprint}</p> : null}
          <label class="toggle">
            <input
              type="checkbox"
              checked={checked}
              onChange={(e) => setChecked(e.currentTarget.checked)}
            />
            <span>The fingerprint matches what my browser shows</span>
          </label>
        </>
      ) : secure ? (
        <>
          <p>
            This invite carries no certificate fingerprint. Only continue if you trust the
            connection you accepted in the browser's warning.
          </p>
          <label class="toggle">
            <input
              type="checkbox"
              checked={checked}
              onChange={(e) => setChecked(e.currentTarget.checked)}
            />
            <span>I trust this connection</span>
          </label>
        </>
      ) : (
        <p class="muted">Pairing this phone as a device of this Deck.</p>
      )}
      <button
        type="button"
        class="btn primary block"
        disabled={
          busy || expired || waitS > 0 || ((offer.fingerprint !== null || secure) && !checked)
        }
        onClick={() => void controller.confirmPair()}
      >
        {busy ? "Pairing..." : waitS > 0 ? `Try again in ${waitS} s` : "Pair this phone"}
      </button>
      <button
        type="button"
        class="btn block"
        disabled={busy}
        onClick={() => controller.cancelOffer()}
      >
        Cancel
      </button>
    </Shell>
  );
}

function Input({ state }: { state: AppState }) {
  const controller = useController();
  const [text, setText] = useState("");
  const submit = (e: Event) => {
    e.preventDefault();
    if (text.trim() === "") return;
    controller.submitInput(text);
  };
  return (
    <Shell>
      <h1>Pair with Deck</h1>
      <p class="muted">
        Run <code>deck remote pair</code> on your computer and scan the QR code it shows with this
        phone's camera, or paste the invite line or code below.
      </p>
      {state.authMessage !== null ? (
        <p class="error-text" role="alert">
          {state.authMessage}
        </p>
      ) : null}
      <form onSubmit={submit} class="stack">
        <label class="field">
          <span>Invite or pairing code</span>
          <input
            type="text"
            inputMode="text"
            autoComplete="off"
            autoCapitalize="none"
            autoCorrect="off"
            spellcheck={false}
            value={text}
            onInput={(e) => setText(e.currentTarget.value)}
            placeholder="deck://pair?... or ABCDE-23456"
          />
        </label>
        <button type="submit" class="btn primary block" disabled={text.trim() === ""}>
          Continue
        </button>
      </form>
    </Shell>
  );
}

export function ProtocolMismatch() {
  const controller = useController();
  return (
    <Shell>
      <h1>Update needed</h1>
      <p>
        This page and the Deck on your computer speak different versions of the remote protocol.
        Update Deck, then reload this page.
      </p>
      <button type="button" class="btn primary block" onClick={() => location.reload()}>
        Reload
      </button>
      <button type="button" class="btn block" onClick={() => controller.forget(null)}>
        Forget this pairing
      </button>
    </Shell>
  );
}

export function Pairing({ state }: { state: AppState }) {
  if (state.auth === "protocol") return <ProtocolMismatch />;
  if ((state.auth === "offer" || state.auth === "pairing") && state.offer !== null) {
    return <Offer state={state} offer={state.offer} />;
  }
  return <Input state={state} />;
}
