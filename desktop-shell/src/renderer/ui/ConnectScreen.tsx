import { CornerDownLeft, Laptop, Loader2, Lock, X } from "./icons";
import { type ReactNode, useEffect, useMemo, useState } from "react";
import type { DeckBridge, PairOutcome, SavedServer } from "../../shared/bridge";
import { parsePairText } from "../connect";
import { cn } from "../lib/cn";
import { Button } from "./kit/button";
import { fieldClass } from "./kit/dialog";
import { isMac } from "./keys";
import { PLATES } from "../plates";
import { Plate } from "./kit/plate";
import { base, Command, Credit, FirstStrip, Headline, Notice, StartFailed, type StartFailure, stagger } from "./Welcome";

// A list row on the backdrop: a name and its path; the return key shows on
// the row under the pointer.
const rowClass =
  "group flex w-full cursor-pointer items-center gap-3 rounded-[10px] px-3 py-2 text-left outline-none transition-colors hover:bg-hover focus-visible:bg-selected disabled:cursor-default disabled:opacity-50";

// The recent folders: a click starts Deck in one.
function Recent({
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
    <section aria-label="Open a folder" className="flex flex-col gap-1.5">
      <h2 className="font-medium text-faint text-xs">Recent projects</h2>
      {recent.length > 0 ? (
        <ul className="servers recent -mx-3 flex flex-col" aria-label="Recent folders">
          {recent.map((dir) => (
            <li key={dir}>
              <button type="button" className={cn("server-open", rowClass)} disabled={opening !== null} onClick={() => void open(dir)}>
                <span className="flex min-w-0 flex-1 items-baseline gap-3">
                  <span className="shrink-0 font-medium text-sm">{base(dir)}</span>
                  <span className="min-w-0 truncate font-mono text-2xs text-faint">{dir}</span>
                </span>
                {opening === dir ? (
                  <Loader2 className="size-3.5 shrink-0 animate-spin text-muted-foreground" />
                ) : (
                  <CornerDownLeft className="size-3.5 shrink-0 text-faint opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100" />
                )}
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-pretty text-muted-foreground text-xs leading-relaxed">
          None yet. Add a folder and Deck opens it; agents work there until you close it or quit.
        </p>
      )}
    </section>
  );
}

// One step of the pairing recipe, its number in the margin.
function Step({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="flex items-baseline gap-3">
      <span aria-hidden className="w-2 shrink-0 text-faint tabular-nums">
        {n}
      </span>
      <span className="text-pretty">{children}</span>
    </li>
  );
}

const cmd = "font-mono text-[11.5px] text-foreground";

function Pair({
  bridge,
  servers,
  invite,
  primary,
  onPaired,
  onOpen,
  onForget,
  setError,
}: {
  bridge: DeckBridge;
  servers: SavedServer[];
  invite: string;
  // Whether Pair is the window's one prominent action: the person came here to pair.
  primary: boolean;
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
    <section aria-label="Pair" className="flex animate-enter flex-col gap-1.5" style={stagger(1)}>
      <h2 className="font-medium text-faint text-xs">Pair with a Deck</h2>
      {servers.length > 0 && (
        <ul className="servers -mx-3 flex flex-col" aria-label="Paired servers">
          {servers.map((s) => (
            <li key={s.id} className="group relative flex items-center">
              <button type="button" className={cn("server-open pr-24", rowClass)} onClick={() => onOpen(s)}>
                {s.tls ? <Lock className="size-4 shrink-0 text-faint" /> : <Laptop className="size-4 shrink-0 text-faint" />}
                <span className="flex min-w-0 flex-1 items-baseline gap-2">
                  <span className="shrink-0 font-medium text-sm">Paired Deck</span>
                  <span className="truncate text-2xs text-faint">
                    {s.tls ? "Another computer" : "This computer"}
                    {s.persisted ? "" : " · until the app quits"}
                  </span>
                </span>
              </button>
              <Button
                variant="ghost-muted"
                size="xs"
                className="absolute right-2 opacity-0 focus-visible:opacity-100 group-focus-within:opacity-100 group-hover:opacity-100"
                onClick={() => onForget(s)}
              >
                <X />
                Forget
              </Button>
            </li>
          ))}
        </ul>
      )}
      <div className={cn("flex flex-col gap-4 pt-1", servers.length > 0 && "mt-1.5 border-hairline border-t pt-3.5")}>
        <ol className="steps flex flex-col gap-1.5 text-muted-foreground text-xs leading-relaxed">
          <Step n={1}>
            In the project, run <code className={cmd}>deck remote</code>, with <code className={cmd}>--lan</code> for another computer.
          </Step>
          <Step n={2}>
            In another terminal, run <code className={cmd}>deck remote pair</code>.
          </Step>
          <Step n={3}>Paste the link or the invite line it prints.</Step>
        </ol>
        <label className="field flex flex-col gap-1.5">
          <span className="font-medium text-xs">Pairing link or invite line</span>
          <input
            id="pair-input"
            className={cn(fieldClass, "h-9 font-mono text-xs placeholder:font-sans")}
            value={text}
            placeholder="http://127.0.0.1:7878/#pair=…  or  deck://pair?…"
            spellCheck={false}
            onChange={(e) => setText(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter") void pair();
            }}
          />
        </label>
        {https && (
          <label className="field flex animate-enter flex-col gap-1.5">
            <span className="font-medium text-xs">Certificate fingerprint (SHA-256, as deck remote prints it)</span>
            <input
              className={cn(fieldClass, "h-9 font-mono text-xs")}
              value={fp}
              placeholder="AB:CD:…"
              spellCheck={false}
              onChange={(e) => setFp(e.target.value)}
            />
          </label>
        )}
        <Button variant={primary ? "default" : "outline"} className="self-end" onClick={() => void pair()} disabled={busy || text.trim() === ""}>
          {busy && <Loader2 className="animate-spin" />}
          {busy ? "Pairing…" : "Pair"}
        </Button>
      </div>
    </section>
  );
}

// Two ways in: a folder on this computer, which the app's own engine opens
// (the default), or a Deck on another computer to pair with. The Welcome's
// family: a dithered picture, here a tall plate on the left, and the words,
// the action list, the pairing form and the recent folders in one column
// of type beside it. No glass: nothing here floats.
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
  const mac = useMemo(isMac, []);
  // Pairing is the way in for a Deck on another computer; folders on this
  // one come first.
  const [pairing, setPairing] = useState(deck === null || servers.length > 0 || invite !== null);
  // The accent goes to why the person came: Pair when a link or the
  // Welcome's Pair brought them, or when there is no engine; else Add project.
  const pairFirst = invite !== null || deck === null;
  useEffect(() => {
    if (invite !== null) document.getElementById("pair-input")?.focus();
  }, [invite]);
  const addProject = async () => {
    setError(null);
    setError(await onAdd());
  };
  return (
    <main className="connect flex h-full flex-col" data-screen="connect">
      <FirstStrip onBack={onBack} />
      <div className="flex min-h-0 flex-1">
        <figure className="hidden shrink-0 flex-col gap-1.5 pb-2 pl-2 md:flex md:w-[38%] lg:w-[42%]">
          <Plate art={PLATES.bridge} focus={[0.5, 0.7]} className="min-h-0 flex-1 rounded-(--sheet-radius)" />
          <Credit art={PLATES.bridge} className="px-1" />
        </figure>
        <div className="connect-card flex min-w-0 flex-1 flex-col overflow-y-auto px-6 md:px-10 lg:px-14">
          <div className="my-auto flex w-full max-w-[32rem] flex-col gap-8 py-8">
            <div className="flex animate-enter flex-col gap-4">
              <Headline loud="What should we" quiet="work on?" className="text-[clamp(2.25rem,4.2vw,3.75rem)] leading-[1]" />
              <p className="text-pretty text-[14px] text-muted-foreground leading-relaxed">
                {deck === null
                  ? "This install of Deck is missing its engine. Reinstall Deck, or pair with a Deck on another computer."
                  : "Add a project folder, or pair with a Deck that runs in a terminal or on another computer."}
              </p>
            </div>
            {(message !== null || error !== null || (failure !== null && !failure.noAgents)) && (
              <div className="flex flex-col gap-4">
                {message !== null && <Notice tone="warning">{message}</Notice>}
                {error !== null && <Notice tone="error">{error}</Notice>}
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
              </div>
            )}
            <div className="flex animate-enter flex-col" style={stagger(1)}>
              <Command
                primary={!pairFirst}
                className="primary"
                label={opening === null ? "Add project…" : `Starting Deck in ${base(opening)}…`}
                keys={opening === null ? [mac ? "⌘" : "Ctrl", "O"] : undefined}
                disabled={deck === null || opening !== null}
                onClick={() => void addProject()}
              />
              <Command
                primary={pairFirst && !pairing}
                label="Pair with a Deck…"
                hint={pairing ? undefined : "in a terminal, or on another computer"}
                aria-expanded={pairing}
                onClick={() => {
                  setPairing(true);
                  requestAnimationFrame(() => document.getElementById("pair-input")?.focus());
                }}
              />
            </div>
            {pairing && (
              <Pair
                bridge={bridge}
                servers={servers}
                invite={invite ?? ""}
                primary={pairFirst}
                onPaired={onPaired}
                onOpen={onOpen}
                onForget={onForget}
                setError={setError}
              />
            )}
            {deck !== null && <Recent recent={recent} opening={opening} onFolder={onFolder} setError={setError} />}
          </div>
        </div>
      </div>
    </main>
  );
}
