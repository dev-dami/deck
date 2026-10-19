import { ChevronRight, Folder, FolderOpen, Laptop, Link2, Loader2, Lock, X } from "lucide-react";
import { useState } from "react";
import type { DeckBridge, PairOutcome, SavedServer } from "../../shared/bridge";
import { parsePairText } from "../connect";
import { Button } from "./kit/button";

function label(s: SavedServer): string {
  const host = s.host.includes(":") ? `[${s.host}]` : s.host;
  return `${host}:${s.port}`;
}

// The last part of a folder path, for the recent list.
function base(dir: string): string {
  return dir.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || dir;
}

const input =
  "h-9 w-full rounded-lg border border-input bg-background px-3 text-[13px] outline-none placeholder:text-muted-foreground/70 focus:border-primary";

function Section({ title, children, label: aria }: { title: string; children: React.ReactNode; label: string }) {
  return (
    <section aria-label={aria} className="flex flex-col gap-3">
      <h2 className="px-1 font-medium text-muted-foreground text-xs">{title}</h2>
      {children}
    </section>
  );
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
      <Section title="Open a folder" label="Open a folder">
        <p className="rounded-xl border border-dashed px-4 py-3 text-muted-foreground text-xs leading-relaxed">
          Put the <code className="font-mono text-foreground">deck</code> command on your PATH. Then this app can run
          Deck in a project folder on this computer.
        </p>
      </Section>
    );
  }
  return (
    <Section title="Open a folder" label="Open a folder">
      <Button
        size="lg"
        className="primary h-11 w-full rounded-xl text-[14px]"
        disabled={opening !== null}
        onClick={() => void bridge.pickFolder().then(open)}
      >
        {opening === null ? <FolderOpen /> : <Loader2 className="animate-spin" />}
        {opening === null ? "Open a folder…" : `Starting Deck in ${base(opening)}…`}
      </Button>
      {recent.length > 0 && (
        <ul className="servers recent flex flex-col gap-px" aria-label="Recent folders">
          {recent.map((dir) => (
            <li key={dir}>
              <button
                type="button"
                className="server-open group flex w-full cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-left hover:bg-accent disabled:cursor-default disabled:opacity-50"
                disabled={opening !== null}
                onClick={() => void open(dir)}
              >
                <Folder className="size-4 shrink-0 text-muted-foreground" />
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-[13px]">{base(dir)}</span>
                  <span className="truncate text-2xs text-muted-foreground">{dir}</span>
                </span>
                <ChevronRight className="size-3.5 text-muted-foreground opacity-0 group-hover:opacity-100" />
              </button>
            </li>
          ))}
        </ul>
      )}
      <p className="px-1 text-2xs text-muted-foreground leading-relaxed">
        The app runs <code className="font-mono">deck serve --rpc</code> in the folder and stops it when you close the
        folder or quit.
      </p>
    </Section>
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
    <Section title="Pair with a Deck" label="Pair">
      {servers.length > 0 && (
        <ul className="servers flex flex-col gap-px" aria-label="Paired servers">
          {servers.map((s) => (
            <li key={s.id} className="group flex items-center gap-1 rounded-lg hover:bg-accent">
              <button
                type="button"
                className="server-open flex min-w-0 flex-1 cursor-pointer items-center gap-3 px-3 py-2 text-left"
                onClick={() => onOpen(s)}
              >
                {s.tls ? (
                  <Lock className="size-4 shrink-0 text-muted-foreground" />
                ) : (
                  <Laptop className="size-4 shrink-0 text-muted-foreground" />
                )}
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-mono text-[13px]">{label(s)}</span>
                  <span className="truncate text-2xs text-muted-foreground">
                    {s.tls ? "https, pinned" : "this computer"}
                    {s.persisted ? "" : " · until the app quits"}
                  </span>
                </span>
              </button>
              <Button
                variant="ghost-muted"
                size="xs"
                className="mr-1 opacity-0 group-hover:opacity-100"
                onClick={() => onForget(s)}
              >
                <X />
                Forget
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className="flex flex-col gap-3 rounded-xl border bg-card p-4">
        <ol className="steps flex list-decimal flex-col gap-1 pl-4 text-muted-foreground text-xs leading-relaxed marker:text-muted-foreground/60">
          <li>
            In the project, run <code className="font-mono text-foreground/80">deck remote</code> (add{" "}
            <code className="font-mono text-foreground/80">--lan</code> for another computer).
          </li>
          <li>
            In another terminal, run <code className="font-mono text-foreground/80">deck remote pair</code>.
          </li>
          <li>Paste the link or the invite line it prints.</li>
        </ol>
        <label className="field flex flex-col gap-1.5">
          <span className="font-medium text-xs">Pairing link or invite line</span>
          <input
            className={input}
            value={text}
            placeholder="http://127.0.0.1:7878/#pair=…  or  deck://pair?…"
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void pair();
            }}
          />
        </label>
        {https && (
          <label className="field flex flex-col gap-1.5">
            <span className="font-medium text-xs">Certificate fingerprint (SHA-256, as deck remote prints it)</span>
            <input className={input} value={fp} placeholder="AB:CD:…" onChange={(e) => setFp(e.target.value)} />
          </label>
        )}
        <Button variant="outline" className="self-end" onClick={() => void pair()} disabled={busy || text.trim() === ""}>
          {busy ? <Loader2 className="animate-spin" /> : <Link2 />}
          {busy ? "Pairing…" : "Pair"}
        </Button>
      </div>
    </Section>
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
    <main className="connect drag flex h-full justify-center overflow-y-auto px-6" data-screen="connect">
      <div className="connect-card no-drag flex w-full max-w-md flex-col gap-8 py-[10vh]">
        <div className="flex flex-col items-center gap-3 text-center">
          <span className="grid size-11 place-items-center rounded-xl bg-foreground font-bold text-background text-xl">
            d
          </span>
          <h1 className="font-semibold text-2xl tracking-tight">Connect to Deck</h1>
          <p className="max-w-sm text-muted-foreground text-sm">
            Run many coding agents at once, in one window. Open a project to start.
          </p>
        </div>
        {message !== null && (
          <p className="notice rounded-xl border border-warning/30 bg-warning/8 px-4 py-3 text-[13px]">{message}</p>
        )}
        {error !== null && (
          <p className="error rounded-xl border border-destructive/40 bg-destructive/8 px-4 py-3 text-[13px]" role="alert">
            {error}
          </p>
        )}
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
      </div>
    </main>
  );
}
