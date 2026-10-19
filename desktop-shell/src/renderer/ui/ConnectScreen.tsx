import { useEffect, useState } from "react";
import type { DeckBridge, PairOutcome, SavedServer } from "../../shared/bridge";
import { parsePairText } from "../connect";

function label(s: SavedServer): string {
  const host = s.host.includes(":") ? `[${s.host}]` : s.host;
  return `${host}:${s.port}`;
}

export function ConnectScreen({
  bridge,
  servers,
  message,
  onPaired,
  onOpen,
  onForget,
}: {
  bridge: DeckBridge;
  servers: SavedServer[];
  message: string | null;
  onPaired: (s: SavedServer) => void;
  onOpen: (s: SavedServer) => void;
  onForget: (s: SavedServer) => void;
}) {
  const [text, setText] = useState("");
  const [fp, setFp] = useState("");
  const [busy, setBusy] = useState<"pair" | "local" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [deck, setDeck] = useState<string | null>(null);

  useEffect(() => {
    void bridge.findDeck().then(setDeck);
  }, [bridge]);

  const finish = (out: PairOutcome) => {
    if (out.ok) {
      setText("");
      setFp("");
      onPaired(out.server);
    } else {
      setError(out.message);
    }
  };

  const pair = async () => {
    const parsed = parsePairText(text, fp);
    if (!parsed.ok) {
      setError(parsed.message);
      return;
    }
    setError(null);
    setBusy("pair");
    finish(await bridge.pair(parsed.input));
    setBusy(null);
  };

  const startLocal = async () => {
    const dir = await bridge.pickFolder();
    if (dir === null) return;
    setError(null);
    setBusy("local");
    finish(await bridge.startLocal(dir));
    setBusy(null);
  };

  const https = text.trim().toLowerCase().startsWith("https:");

  return (
    <main className="connect" data-screen="connect">
      <div className="connect-card">
        <h1>Connect to Deck</h1>
        <p className="muted">
          This app shows the sessions and agents of a Deck that runs on your computer or on your network.
        </p>
        {message !== null && <p className="notice">{message}</p>}

        {servers.length > 0 && (
          <section aria-label="Paired servers">
            <h2>Paired</h2>
            <ul className="servers">
              {servers.map((s) => (
                <li key={s.id}>
                  <button type="button" className="server-open" onClick={() => onOpen(s)}>
                    <span>{label(s)}</span>
                    <span className="muted">
                      {s.tls ? "https, pinned" : "this computer"}
                      {s.persisted ? "" : " · until the app quits"}
                    </span>
                  </button>
                  <button type="button" className="link-button" onClick={() => onForget(s)}>
                    Forget
                  </button>
                </li>
              ))}
            </ul>
          </section>
        )}

        <section aria-label="Pair">
          <h2>Pair with a running Deck</h2>
          <ol className="steps">
            <li>
              In your project, run <code>deck remote</code> (add <code>--lan</code> for another computer).
            </li>
            <li>
              In another terminal, run <code>deck remote pair</code>.
            </li>
            <li>Paste the link or the invite line it prints.</li>
          </ol>
          <label className="field">
            <span>Pairing link or invite line</span>
            <input
              value={text}
              placeholder="http://127.0.0.1:7878/#pair=…  or  deck://pair?…"
              onChange={(e) => setText(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter") void pair();
              }}
            />
          </label>
          {https && (
            <label className="field">
              <span>Certificate fingerprint (SHA-256, as deck remote prints it)</span>
              <input value={fp} placeholder="AB:CD:…" onChange={(e) => setFp(e.target.value)} />
            </label>
          )}
          <button type="button" className="primary" onClick={() => void pair()} disabled={busy !== null || text.trim() === ""}>
            {busy === "pair" ? "Pairing…" : "Pair"}
          </button>
        </section>

        <section aria-label="Start Deck">
          <h2>Start Deck here</h2>
          {deck === null ? (
            <p className="muted">
              Put the <code>deck</code> command on your PATH and this app can start it in a folder for you.
            </p>
          ) : (
            <button type="button" onClick={() => void startLocal()} disabled={busy !== null}>
              {busy === "local" ? "Starting Deck…" : "Start Deck in a folder…"}
            </button>
          )}
        </section>
        {error !== null && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
      </div>
    </main>
  );
}
