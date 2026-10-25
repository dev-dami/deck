import { ArrowLeft, ChevronRight, Folder, Laptop, Link2, Loader2, Lock, Plus, X } from "lucide-react";
import { useEffect, useState } from "react";
import type { DeckBridge, PairOutcome, SavedServer } from "../../shared/bridge";
import { parsePairText } from "../connect";
import { Button } from "./kit/button";
import { base, StartFailed, type StartFailure } from "./Welcome";

const input =
  "h-9 w-full rounded-lg border border-input bg-background px-3 text-sm outline-none transition-[border-color,box-shadow] placeholder:text-faint focus:border-primary/50 focus:ring-[3px] focus:ring-ring";

function Section({ title, children, label: aria }: { title: string; children: React.ReactNode; label: string }) {
  return (
    <section aria-label={aria} className="flex flex-col gap-3">
      <h2 className="px-1 font-medium text-muted-foreground text-xs">{title}</h2>
      {children}
    </section>
  );
}

// The recent folders: a click starts Deck in one.
function OpenFolder({
  recent,
  opening,
  onFolder,
  setError,
}: {
  recent: string[];
  opening: string | null;
  onFolder: (dir: string) => Promise<string | null>;
  setError: (e: string | null) => void;
}) {
  const open = async (dir: string) => {
    setError(null);
    setError(await onFolder(dir));
  };
  return (
    <Section title="Recent projects" label="Open a folder">
      {recent.length > 0 ? (
        <ul className="servers recent flex flex-col gap-px" aria-label="Recent folders">
          {recent.map((dir) => (
            <li key={dir}>
              <button
                type="button"
                className="server-open group flex w-full cursor-pointer items-center gap-3 rounded-lg px-3 py-2 text-left hover:bg-accent disabled:cursor-default disabled:opacity-50"
                disabled={opening !== null}
                onClick={() => void open(dir)}
              >
                {opening === dir ? (
                  <Loader2 className="size-4 shrink-0 animate-spin text-muted-foreground" />
                ) : (
                  <Folder className="size-4 shrink-0 text-muted-foreground" />
                )}
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate text-sm">{base(dir)}</span>
                  <span className="truncate text-2xs text-muted-foreground">{dir}</span>
                </span>
                <ChevronRight className="size-3.5 text-muted-foreground opacity-0 group-focus-within:opacity-100 group-hover:opacity-100" />
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="px-3 text-muted-foreground text-xs">
          No projects yet. Add a folder and Deck opens it; agents work there until you close it or quit.
        </p>
      )}
    </Section>
  );
}

function Pair({
  bridge,
  servers,
  invite,
  onPaired,
  onOpen,
  onForget,
  setError,
}: {
  bridge: DeckBridge;
  servers: SavedServer[];
  invite: string;
  onPaired: (s: SavedServer) => void;
  onOpen: (s: SavedServer) => void;
  onForget: (s: SavedServer) => void;
  setError: (e: string | null) => void;
}) {
  const [text, setText] = useState(invite);
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
                  <span className="truncate text-[13px]">Paired Deck</span>
                  <span className="truncate text-2xs text-muted-foreground">
                    {s.tls ? "Another computer" : "This computer"}
                    {s.persisted ? "" : " · until the app quits"}
                  </span>
                </span>
              </button>
              <Button
                variant="ghost-muted"
                size="xs"
                className="mr-1 opacity-0 focus-visible:opacity-100 group-focus-within:opacity-100 group-hover:opacity-100"
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
            id="pair-input"
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

// Two ways in, after T3 Code's "no projects" hero: a folder on this
// computer, which the app's own engine opens (the default), or a Deck on
// another computer to pair with.
export function ConnectScreen({
  bridge,
  deck,
  servers,
  recent,
  opening,
  message,
  failure,
  invite,
  onAdd,
  onFolder,
  onRetry,
  onPaired,
  onOpen,
  onForget,
  onBack,
}: {
  bridge: DeckBridge;
  deck: string | null;
  servers: SavedServer[];
  recent: string[];
  opening: string | null;
  message: string | null;
  // The engine's last start error, when it is not a missing agent CLI.
  failure: StartFailure | null;
  // The invite line of a deck://pair link, put in the pairing form for the
  // person to confirm. "" opens the form empty (Pair on the Welcome).
  invite: string | null;
  // Picks a folder and opens it: the error text, or null.
  onAdd: () => Promise<string | null>;
  onFolder: (dir: string) => Promise<string | null>;
  // Opens the folder of the failure again.
  onRetry: () => Promise<string | null>;
  onPaired: (s: SavedServer) => void;
  onOpen: (s: SavedServer) => void;
  onForget: (s: SavedServer) => void;
  // Back to the open projects, when there are some.
  onBack?: () => void;
}) {
  const [error, setError] = useState<string | null>(null);
  // Pairing is the way in for a Deck on another computer; folders on this
  // one come first.
  const [pairing, setPairing] = useState(deck === null || servers.length > 0 || invite !== null);
  useEffect(() => {
    if (invite !== null) document.getElementById("pair-input")?.focus();
  }, [invite]);
  const addProject = async () => {
    setError(null);
    setError(await onAdd());
  };
  return (
    <main className="connect flex h-full flex-col overflow-y-auto" data-screen="connect">
      <div className="drag flex h-12 shrink-0 items-center px-3">
        {onBack !== undefined && (
          <Button variant="ghost-muted" size="sm" onClick={onBack}>
            <ArrowLeft />
            Back to threads
          </Button>
        )}
      </div>
      <div className="flex flex-1 justify-center px-6">
        <div className="connect-card flex w-full max-w-lg flex-col gap-10 pt-[8vh] pb-16">
          <div className="flex flex-col items-center gap-6 text-center">
            <div className="flex flex-col items-center gap-2">
              <h1 className="text-balance font-medium text-2xl tracking-tight sm:text-3xl">What should we work on?</h1>
              <p className="text-muted-foreground text-sm">
                {deck === null
                  ? "This install of Deck is missing its engine. Reinstall Deck, or pair with a Deck on another computer."
                  : "Add a project, or pair with a Deck on another computer."}
              </p>
            </div>
            <div className="flex justify-center gap-2">
              <Button size="sm" className="primary" disabled={deck === null || opening !== null} onClick={() => void addProject()}>
                {opening === null ? <Plus /> : <Loader2 className="animate-spin" />}
                {opening === null ? "Add project" : `Starting Deck in ${base(opening)}…`}
              </Button>
              <Button
                size="sm"
                variant="outline"
                aria-expanded={pairing}
                onClick={() => {
                  setPairing(true);
                  requestAnimationFrame(() => document.getElementById("pair-input")?.focus());
                }}
              >
                <Link2 />
                Pair with a Deck
              </Button>
            </div>
          </div>
          {message !== null && (
            <p className="notice rounded-xl border border-warning/30 bg-warning/8 px-4 py-3 text-sm">{message}</p>
          )}
          {error !== null && (
            <p className="error rounded-xl border border-destructive/40 bg-destructive/8 px-4 py-3 text-sm" role="alert">
              {error}
            </p>
          )}
          {failure !== null && !failure.noAgents && (
            <StartFailed
              failure={failure}
              onLogs={() => void bridge.openLogs()}
              onRetry={() => {
                setError(null);
                void onRetry().then(setError);
              }}
            />
          )}
          {deck !== null && (
            <OpenFolder
              recent={recent}
              opening={opening}
              onFolder={onFolder}
              setError={setError}
            />
          )}
          {pairing && (
          <Pair
            bridge={bridge}
            servers={servers}
            invite={invite ?? ""}
            onPaired={onPaired}
            onOpen={onOpen}
            onForget={onForget}
            setError={setError}
          />
          )}
        </div>
      </div>
    </main>
  );
}
