import { ArrowLeft, ChevronRight, Folder, Laptop, Link2, Loader2, Lock, Plus, X } from "./icons";
import { type ReactNode, useEffect, useState } from "react";
import type { DeckBridge, PairOutcome, SavedServer } from "../../shared/bridge";
import { parsePairText } from "../connect";
import { cn } from "../lib/cn";
import { Button } from "./kit/button";
import { fieldClass } from "./kit/dialog";
import { base, DeckMark, Notice, StartFailed, type StartFailure, stagger } from "./Welcome";

// A pane of glass on the backdrop: a title, then what it holds.
function Pane({
  title,
  icon,
  label: aria,
  index,
  children,
}: {
  title: string;
  icon: ReactNode;
  label: string;
  index: number;
  children: ReactNode;
}) {
  return (
    <section aria-label={aria} className="liquid flex animate-enter flex-col gap-1 rounded-(--panel-radius) p-1.5" style={stagger(index)}>
      <h2 className="flex items-center gap-2 px-3.5 pt-2.5 pb-1.5 font-semibold text-[13px] [&_svg]:size-4 [&_svg]:text-muted-foreground">
        {icon}
        {title}
      </h2>
      {children}
    </section>
  );
}

// A row of a list on glass: a glyph in clear glass, a name over its detail.
const rowClass =
  "group flex w-full cursor-pointer items-center gap-3 rounded-[12px] px-2.5 py-2 text-left outline-none transition-colors hover:bg-hover focus-visible:bg-selected disabled:cursor-default disabled:opacity-50";

function Glyph({ children }: { children: ReactNode }) {
  return (
    <span aria-hidden className="liquid-clear grid size-8 shrink-0 place-items-center rounded-full text-muted-foreground [&_svg]:size-4">
      {children}
    </span>
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
    <Pane title="Recent projects" icon={<Folder weight="duotone" />} label="Open a folder" index={1}>
      {recent.length > 0 ? (
        <ul className="servers recent flex flex-col gap-px" aria-label="Recent folders">
          {recent.map((dir) => (
            <li key={dir}>
              <button type="button" className={cn("server-open", rowClass)} disabled={opening !== null} onClick={() => void open(dir)}>
                <Glyph>{opening === dir ? <Loader2 className="animate-spin" /> : <Folder />}</Glyph>
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-medium text-sm">{base(dir)}</span>
                  <span className="truncate font-mono text-2xs text-faint">{dir}</span>
                </span>
                <ChevronRight className="size-3.5 shrink-0 text-faint opacity-0 transition-opacity group-hover:opacity-100 group-focus-visible:opacity-100" />
              </button>
            </li>
          ))}
        </ul>
      ) : (
        <p className="text-pretty px-3.5 pb-3 text-muted-foreground text-xs leading-relaxed">
          No projects yet. Add a folder and Deck opens it; agents work there until you close it or quit.
        </p>
      )}
    </Pane>
  );
}

// One step of the pairing recipe, numbered in a small glass circle.
function Step({ n, children }: { n: number; children: ReactNode }) {
  return (
    <li className="flex items-start gap-2.5">
      <span aria-hidden className="liquid-clear mt-px grid size-[18px] shrink-0 place-items-center rounded-full font-medium text-3xs text-muted-foreground tabular-nums">
        {n}
      </span>
      <span className="text-pretty">{children}</span>
    </li>
  );
}

const cmd = "rounded-md bg-hover px-1.5 py-px font-mono text-[11.5px] text-foreground/85 ring-1 ring-hairline";

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
    <Pane title="Pair with a Deck" icon={<Link2 weight="duotone" />} label="Pair" index={2}>
      {servers.length > 0 && (
        <ul className="servers flex flex-col gap-px" aria-label="Paired servers">
          {servers.map((s) => (
            <li key={s.id} className="group relative flex items-center">
              <button type="button" className={cn("server-open pr-24", rowClass)} onClick={() => onOpen(s)}>
                <Glyph>{s.tls ? <Lock /> : <Laptop />}</Glyph>
                <span className="flex min-w-0 flex-1 flex-col">
                  <span className="truncate font-medium text-sm">Paired Deck</span>
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
      <div className={cn("flex flex-col gap-4 px-3.5 pt-1 pb-3.5", servers.length > 0 && "mt-1 border-hairline border-t pt-4")}>
        <ol className="steps flex flex-col gap-2 text-muted-foreground text-xs leading-relaxed">
          <Step n={1}>
            In the project, run <code className={cmd}>deck remote</code> (add <code className={cmd}>--lan</code> for another
            computer).
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
            className={cn(fieldClass, "h-9 font-mono text-xs placeholder:font-sans placeholder:text-sm")}
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
        <Button
          variant={primary ? "default" : "outline"}
          className="self-end"
          onClick={() => void pair()}
          disabled={busy || text.trim() === ""}
        >
          {busy ? <Loader2 className="animate-spin" /> : <Link2 />}
          {busy ? "Pairing…" : "Pair"}
        </Button>
      </div>
    </Pane>
  );
}

// Two ways in, after T3 Code's "no projects" hero: a folder on this
// computer, which the app's own engine opens (the default), or a Deck on
// another computer to pair with. Glass on the backdrop, as the Welcome.
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
    <main className="connect flex h-full flex-col overflow-y-auto" data-screen="connect">
      <div className="drag flex h-13 shrink-0 items-center px-3">
        {onBack !== undefined && (
          <Button variant="outline" size="sm" onClick={onBack}>
            <ArrowLeft />
            Back to threads
          </Button>
        )}
      </div>
      <div className="flex flex-1 justify-center px-6">
        <div className="connect-card flex w-full max-w-[32rem] flex-col gap-4 pt-[max(0.5rem,3vh)] pb-16">
          <div className="welcome-halo relative mb-4 flex animate-enter flex-col items-center gap-5 text-center">
            <DeckMark className="size-12 rounded-[14px]" />
            <div className="flex flex-col items-center gap-2">
              <h1 className="text-balance font-semibold text-[1.75rem] leading-tight tracking-[-0.025em]">What should we work on?</h1>
              <p className="max-w-sm text-pretty text-muted-foreground text-sm leading-relaxed">
                {deck === null
                  ? "This install of Deck is missing its engine. Reinstall Deck, or pair with a Deck on another computer."
                  : "Add a project, or pair with a Deck on another computer."}
              </p>
            </div>
            <div className="flex justify-center gap-2">
              <Button
                variant={pairFirst ? "outline" : "default"}
                className="primary"
                disabled={deck === null || opening !== null}
                onClick={() => void addProject()}
              >
                {opening === null ? <Plus /> : <Loader2 className="animate-spin" />}
                {opening === null ? "Add project" : `Starting Deck in ${base(opening)}…`}
              </Button>
              <Button
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
          {deck !== null && <OpenFolder recent={recent} opening={opening} onFolder={onFolder} setError={setError} />}
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
        </div>
      </div>
    </main>
  );
}
