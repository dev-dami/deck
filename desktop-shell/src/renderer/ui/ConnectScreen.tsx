import { useState } from "react";
import type { DeckBridge, PairOutcome, SavedServer } from "../../shared/bridge";
import { parsePairText } from "../connect";

function label(s: SavedServer): string {
  const host = s.host.includes(":") ? `[${s.host}]` : s.host;
  return `${host}:${s.port}`;
}

// The last part of a folder path, for the recent list.
function base(dir: string): string {
  return dir.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || dir;
}

function OpenFolder({
  bridge,
  deck,
  recent,
  opening,
  onFolder,
  setError,
}: {
  bridge: DeckBridge;
  deck: string | null;
  recent: string[];
  opening: string | null;
  onFolder: (dir: string) => Promise<string | null>;
  setError: (e: string | null) => void;
}) {
  const open = async (dir: string | null) => {
    if (dir === null) return;
    setError(null);
    setError(await onFolder(dir));
  };
  if (deck === null) {
    return (
      <section aria-label="Open a folder">
        <h2>Open a folder</h2>
        <p className="muted">
          Put the <code>deck</code> command on your PATH. Then this app can run Deck in a project folder on this
          computer.
        </p>
      </section>
    );
  }
  return (
    <section aria-label="Open a folder">
      <h2>Open a folder</h2>
      <p className="muted">
        The app runs <code>deck serve --rpc</code> in the folder and stops it when you close the folder or quit.
      </p>
      <button
        type="button"
        className="primary"
        disabled={opening !== null}
        onClick={() => void bridge.pickFolder().then(open)}
      >
        {opening === null ? "Open a folder…" : `Starting Deck in ${base(opening)}…`}
      </button>
      {recent.length > 0 && (
        <ul className="servers recent" aria-label="Recent folders">
          {recent.map((dir) => (
            <li key={dir}>
              <button type="button" className="server-open" disabled={opening !== null} onClick={() => void open(dir)}>
                <span>{base(dir)}</span>
                <span className="muted">{dir}</span>
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  );
}

function Pair({
  bridge,
  servers,
  onPaired,
  onOpen,
  onForget,
  setError,
}: {
  bridge: DeckBridge;
  servers: SavedServer[];
  onPaired: (s: SavedServer) => void;
  onOpen: (s: SavedServer) => void;
  onForget: (s: SavedServer) => void;
  setError: (e: string | null) => void;
}) {
  const [text, setText] = useState("");
  const [fp, setFp] = useState("");
  const [busy, setBusy] = useState(false);

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
    setBusy(true);
    finish(await bridge.pair(parsed.input));
    setBusy(false);
  };

  const https = text.trim().toLowerCase().startsWith("https:");

  return (
    <section aria-label="Pair">
      <h2>Pair with a Deck</h2>
      <p className="muted">For a Deck on another computer, or one that you started yourself.</p>
      {servers.length > 0 && (
        <ul className="servers" aria-label="Paired servers">
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
      )}
      <ol className="steps">
        <li>
          In the project, run <code>deck remote</code> (add <code>--lan</code> for another computer).
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
      <button type="button" onClick={() => void pair()} disabled={busy || text.trim() === ""}>
        {busy ? "Pairing…" : "Pair"}
      </button>
    </section>
  );
}

// Two ways in: a folder on this computer (deck serve --rpc, the default
// when deck is on PATH) or a Deck to pair with (deck remote, protocol v2).
export function ConnectScreen({
  bridge,
  deck,
  servers,
  recent,
  opening,
  message,
  onFolder,
  onPaired,
  onOpen,
  onForget,
}: {
  bridge: DeckBridge;
  deck: string | null;
  servers: SavedServer[];
  recent: string[];
  opening: string | null;
  message: string | null;
  onFolder: (dir: string) => Promise<string | null>;
  onPaired: (s: SavedServer) => void;
  onOpen: (s: SavedServer) => void;
  onForget: (s: SavedServer) => void;
}) {
  const [error, setError] = useState<string | null>(null);
  const folder = (
    <OpenFolder bridge={bridge} deck={deck} recent={recent} opening={opening} onFolder={onFolder} setError={setError} />
  );
  return (
    <main className="connect" data-screen="connect">
      <div className="connect-card">
        <h1>Connect to Deck</h1>
        <p className="muted">This app shows the sessions and agents of a Deck and lets you run many agents at once.</p>
        {message !== null && <p className="notice">{message}</p>}
        {deck !== null && folder}
        <Pair
          bridge={bridge}
          servers={servers}
          onPaired={onPaired}
          onOpen={onOpen}
          onForget={onForget}
          setError={setError}
        />
        {deck === null && folder}
        {error !== null && (
          <p className="error" role="alert">
            {error}
          </p>
        )}
      </div>
    </main>
  );
}
