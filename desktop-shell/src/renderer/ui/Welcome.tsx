import { kindColour } from "@web/theme";
import { ArrowLeft, Folder, Link2, Loader2, RefreshCw, RotateCcw, ScrollText, TerminalIcon } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import type { AgentClis, DeckBridge } from "../../shared/bridge";
import { cn } from "../lib/cn";
import { Button } from "./kit/button";
import { AGENT_HELP } from "./landing";

// The last part of a folder path.
export function base(dir: string): string {
  return dir.replace(/[\\/]+$/, "").split(/[\\/]/).pop() || dir;
}

// An engine that did not start in a folder: its message, the last lines it
// wrote to stderr, and whether it found no agent CLI.
export interface StartFailure {
  dir: string;
  message: string;
  lines: string[];
  noAgents: boolean;
}

// Any start error but a missing agent CLI: what the engine said last, the
// logs, and another try.
export function StartFailed({
  failure,
  onLogs,
  onRetry,
}: {
  failure: StartFailure;
  onLogs: () => void;
  onRetry: () => void;
}) {
  return (
    <section
      aria-label="Deck did not start"
      className="start-failed flex animate-enter flex-col gap-3 rounded-xl border border-hairline bg-surface-1 p-4"
      role="alert"
    >
      <p className="text-sm">
        Deck did not start in <span className="font-mono">{base(failure.dir)}</span>.
      </p>
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-lg bg-code px-3 py-2 font-mono text-2xs text-muted-foreground">
        {failure.lines.length > 0 ? failure.lines.join("\n") : failure.message}
      </pre>
      <div className="flex justify-end gap-2">
        <Button variant="ghost-muted" size="sm" onClick={onLogs}>
          <ScrollText />
          Open logs
        </Button>
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RotateCcw />
          Try again
        </Button>
      </div>
    </section>
  );
}

function Card({ title, icon, className, children }: { title: string; icon: React.ReactNode; className?: string; children: React.ReactNode }) {
  return (
    <section
      aria-label={title}
      className={cn("flex animate-enter flex-col gap-3 rounded-xl border border-hairline bg-surface-1 p-4", className)}
    >
      <h2 className="flex items-center gap-2 font-medium text-sm">
        {icon}
        {title}
      </h2>
      {children}
    </section>
  );
}

// The agent CLIs on the engine's PATH: found with its path, or how to
// install it.
function Agents({
  clis,
  checking,
  failure,
  onCheck,
  onRetry,
}: {
  clis: AgentClis | null;
  checking: boolean;
  failure: StartFailure | null;
  onCheck: () => void;
  onRetry: () => void;
}) {
  return (
    <Card title="Your agents" icon={<TerminalIcon aria-hidden className="size-4 text-muted-foreground" />} className="sm:col-span-2">
      {failure?.noAgents === true && (
        <div className="flex items-center gap-3">
          <p className="flex-1 text-muted-foreground text-xs leading-relaxed">
            Deck found no agent CLI, so it did not open <span className="font-mono">{base(failure.dir)}</span>. Install one,
            then try again.
          </p>
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RotateCcw />
            Try again
          </Button>
        </div>
      )}
      <ul className="agent-clis flex flex-col divide-y divide-hairline" aria-label="Agent CLIs">
        {AGENT_HELP.map((a) => {
          const path = clis?.clis.find((c) => c.kind === a.kind)?.path ?? null;
          return (
            <li key={a.kind} className="flex min-w-0 items-center gap-3 py-2" data-kind={a.kind} data-found={path !== null}>
              <span aria-hidden className="w-5 shrink-0 text-center font-mono text-sm" style={{ color: kindColour(a.kind) }}>
                {a.mark}
              </span>
              <span className="w-28 shrink-0 text-sm">{a.name}</span>
              {clis === null ? (
                <span className="text-faint text-xs">Looking…</span>
              ) : path !== null ? (
                <>
                  <span className="shrink-0 text-muted-foreground text-xs">found</span>
                  <code className="min-w-0 truncate font-mono text-2xs text-faint" title={path}>
                    {path}
                  </code>
                </>
              ) : (
                <>
                  <span className="shrink-0 text-faint text-xs">not found</span>
                  {a.install !== null && (
                    <code className="min-w-0 truncate font-mono text-2xs text-muted-foreground" title={a.install}>
                      {a.install}
                    </code>
                  )}
                  {a.url !== null && (
                    <a
                      href={a.url}
                      target="_blank"
                      rel="noopener noreferrer"
                      className="ml-auto shrink-0 text-muted-foreground text-xs underline-offset-4 hover:text-foreground hover:underline"
                    >
                      Install guide
                    </a>
                  )}
                </>
              )}
            </li>
          );
        })}
      </ul>
      <Button variant="ghost-muted" size="xs" className="self-start" disabled={checking} onClick={onCheck}>
        {checking ? <Loader2 className="animate-spin" /> : <RefreshCw />}
        Check again
      </Button>
    </Card>
  );
}

// The first screen of a new install, and the palette's "Welcome": what Deck
// is, the two ways in, and the agent CLIs it can drive.
export function Welcome({
  bridge,
  opening,
  failure,
  onAdd,
  onPair,
  onRetry,
  onBack,
}: {
  bridge: DeckBridge;
  opening: string | null;
  failure: StartFailure | null;
  // Picks a folder and opens it: the error text, or null.
  onAdd: () => Promise<string | null>;
  // To the pairing form of the connect screen.
  onPair: () => void;
  // Opens the folder that failed again: the error text, or null.
  onRetry: () => Promise<string | null>;
  // Back to the open projects, when there are some.
  onBack?: () => void;
}) {
  const [clis, setClis] = useState<AgentClis | null>(null);
  const [checking, setChecking] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const check = useCallback(
    async (refresh: boolean) => {
      setChecking(true);
      setClis(await bridge.agentClis(refresh));
      setChecking(false);
    },
    [bridge],
  );
  useEffect(() => {
    void check(false);
  }, [check]);
  const engine = clis?.engine !== false;
  const retry = () => {
    setError(null);
    void onRetry().then(setError);
  };
  return (
    <main className="welcome-screen flex h-full flex-col overflow-y-auto bg-canvas" data-screen="welcome">
      <div className="drag flex h-12 shrink-0 items-center px-3">
        {onBack !== undefined && (
          <Button variant="ghost-muted" size="sm" onClick={onBack}>
            <ArrowLeft />
            Back to threads
          </Button>
        )}
      </div>
      <div className="flex flex-1 justify-center px-6">
        <div className="flex w-full max-w-2xl flex-col gap-8 pt-[8vh] pb-16">
          <header className="flex flex-col items-center gap-4 text-center">
            {/* The terminal prompt is the app's own suit. */}
            <span className="grid size-11 place-items-center rounded-xl border border-hairline bg-surface-1 font-mono text-base">
              {">_"}
            </span>
            <div className="flex flex-col gap-1.5">
              <h1 className="text-balance font-medium text-2xl tracking-tight">One deck for all your agents</h1>
              <p className="text-muted-foreground text-sm">
                Open a project and run your agent CLIs side by side, in one window.
              </p>
            </div>
          </header>
          {!engine && (
            <p className="text-center text-muted-foreground text-xs">
              Deck did not find its engine. Run <code className="font-mono text-foreground/80">npm run build:deck</code>, or set{" "}
              <code className="font-mono text-foreground/80">DECK_BIN</code>.
            </p>
          )}
          {error !== null && (
            <p className="error rounded-xl border border-destructive/40 bg-destructive/8 px-4 py-3 text-sm" role="alert">
              {error}
            </p>
          )}
          {failure !== null && !failure.noAgents && (
            <StartFailed failure={failure} onLogs={() => void bridge.openLogs()} onRetry={retry} />
          )}
          <div className="grid gap-3 sm:grid-cols-2">
            <Card title="Open a folder" icon={<Folder aria-hidden className="size-4 text-muted-foreground" />}>
              <p className="flex-1 text-muted-foreground text-xs leading-relaxed">
                Deck starts its engine in the folder. Agents work there until you close it.
              </p>
              <Button
                size="sm"
                className="primary self-start"
                disabled={!engine || opening !== null}
                onClick={() => {
                  setError(null);
                  void onAdd().then(setError);
                }}
              >
                {opening === null ? <Folder /> : <Loader2 className="animate-spin" />}
                {opening === null ? "Open a folder" : `Starting Deck in ${base(opening)}…`}
              </Button>
            </Card>
            <Card title="Pair with a Deck" icon={<Link2 aria-hidden className="size-4 text-muted-foreground" />}>
              <p className="flex-1 text-muted-foreground text-xs leading-relaxed">
                Watch a Deck that runs in a terminal or on another computer.
              </p>
              <Button size="sm" variant="outline" className="self-start" onClick={onPair}>
                <Link2 />
                Pair with a Deck
              </Button>
            </Card>
            <Agents clis={clis} checking={checking} failure={failure} onCheck={() => void check(true)} onRetry={retry} />
          </div>
        </div>
      </div>
    </main>
  );
}
