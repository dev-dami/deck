import { kindColour } from "@web/theme";
import { ArrowLeft, Check, CircleAlert, Copy, ExternalLink, Loader2, RefreshCw, RotateCcw, ScrollText } from "./icons";
import { type ComponentProps, type CSSProperties, type ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import type { AgentClis, DeckBridge } from "../../shared/bridge";
import { cn } from "../lib/cn";
import { Button } from "./kit/button";
import { isMac } from "./keys";
import { AGENT_HELP, type AgentHelp } from "./landing";

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

// Things that enter one after another: each rises 4px and fades in, 60ms
// after the one before it.
export const stagger = (i: number): CSSProperties => ({ animationDelay: `${i * 60}ms` });

// Deck's mark, the app icon's prompt: a chevron in the accent and the
// cursor in the text colour. Sized by its height.
export function DeckMark({ className }: { className?: string }) {
  return (
    <svg aria-hidden viewBox="16 19 34 26" className={cn("h-3.5 w-auto shrink-0", className)} fill="none">
      <path d="M19.5 22.5 31 32l-11.5 9.5" stroke="var(--primary)" strokeWidth="4.75" strokeLinecap="round" strokeLinejoin="round" />
      <rect x="35" y="37" width="12" height="4.75" rx="2.375" fill="currentColor" />
    </svg>
  );
}

// The window's name in the drag strip, where a title bar would put it.
export function Wordmark() {
  return (
    <span className="flex items-center gap-2 font-semibold text-[13px] tracking-tight">
      <DeckMark />
      Deck
    </span>
  );
}

// An agent kind's glyph in its hue, as the room's lanes draw it: the same
// kind wears the same glyph and hue on every page.
export function AgentGlyph({ kind, mark, className }: { kind: string; mark: string; className?: string }) {
  return (
    <span aria-hidden className={cn("inline-grid w-5 shrink-0 place-items-center font-mono text-sm leading-none", className)} style={{ color: kindColour(kind) }}>
      {mark || kind.slice(0, 1)}
    </span>
  );
}

// A line the person should read before going on: an icon in the tone's
// colour and the text, straight on the page.
export function Notice({ tone, className, children }: { tone: "error" | "warning"; className?: string; children: ReactNode }) {
  return (
    <p
      className={cn("flex animate-enter items-start gap-2.5 text-pretty text-sm", tone === "error" ? "error" : "notice", className)}
      role={tone === "error" ? "alert" : undefined}
    >
      <CircleAlert
        aria-hidden
        weight="fill"
        className={cn("mt-0.5 size-4 shrink-0", tone === "error" ? "text-destructive-foreground" : "text-warning-foreground")}
      />
      <span className="min-w-0 flex-1">{children}</span>
    </p>
  );
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
    <section aria-label="Deck did not start" className="start-failed flex animate-enter flex-col gap-3" role="alert">
      <p className="flex items-center gap-2.5 text-sm">
        <CircleAlert aria-hidden weight="fill" className="size-4 shrink-0 text-destructive-foreground" />
        <span>
          Deck did not start in <span className="font-mono">{base(failure.dir)}</span>.
        </span>
      </p>
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-[10px] bg-code px-3 py-2 font-mono text-2xs text-muted-foreground">
        {failure.lines.length > 0 ? failure.lines.join("\n") : failure.message}
      </pre>
      <div className="flex gap-2">
        <Button variant="outline" size="sm" onClick={onRetry}>
          <RotateCcw />
          Try again
        </Button>
        <Button variant="ghost-muted" size="sm" onClick={onLogs}>
          <ScrollText />
          Open logs
        </Button>
      </div>
    </section>
  );
}

// A row of the command list, after Raycast's and Linear's first runs: the
// command, a word on where it leads, its keys. The one prominent row is the
// accent as glass; the rest are plain until hovered.
export function Command({
  label,
  hint,
  keys,
  primary = false,
  className,
  ...props
}: { label: ReactNode; hint?: ReactNode | undefined; keys?: string[] | undefined; primary?: boolean } & ComponentProps<"button">) {
  return (
    <button
      type="button"
      className={cn(
        "flex h-11 w-full cursor-pointer items-center gap-3 rounded-[12px] px-4 text-left text-sm outline-none transition-[background-color,scale] duration-150 active:scale-[0.99] focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:opacity-50",
        primary ? "liquid-tint font-medium hover:brightness-110" : "hover:bg-hover",
        className,
      )}
      {...props}
    >
      <span className="min-w-0 flex-1 truncate">{label}</span>
      {hint !== undefined && <span className={cn("truncate text-xs", primary ? "text-primary-foreground/75" : "text-faint")}>{hint}</span>}
      {keys !== undefined && (
        <span className={cn("flex shrink-0 gap-1 font-sans text-xs", primary ? "text-primary-foreground/75" : "text-faint")}>
          {keys.map((k) => (
            <kbd key={k} className="min-w-3 text-center font-sans">
              {k}
            </kbd>
          ))}
        </span>
      )}
    </button>
  );
}

// An install command, with a button that copies it.
function Install({ command }: { command: string }) {
  const [copied, setCopied] = useState(false);
  useEffect(() => {
    if (!copied) return;
    const t = setTimeout(() => setCopied(false), 1600);
    return () => clearTimeout(t);
  }, [copied]);
  return (
    <span className="flex min-w-0 items-center gap-1">
      <code className="min-w-0 truncate font-mono text-2xs text-muted-foreground" title={command}>
        {command}
      </code>
      <Button
        variant="ghost-muted"
        size="icon-xs"
        className="size-5 [&_svg]:size-3"
        aria-label={copied ? "Copied" : `Copy ${command}`}
        title={copied ? "Copied" : "Copy"}
        onClick={() => void navigator.clipboard?.writeText(command).then(() => setCopied(true))}
      >
        {copied ? <Check className="text-success-foreground" /> : <Copy />}
      </Button>
    </span>
  );
}

// One agent CLI on the roster: its glyph, bright when Deck found it; its
// name; then the path it found, or the command that installs it.
function AgentRow({ a, path, looking }: { a: AgentHelp; path: string | null; looking: boolean }) {
  const found = path !== null;
  const missing = !found && !looking;
  return (
    <li className="flex min-w-0 gap-3 py-2.5" data-kind={a.kind} data-found={found}>
      <AgentGlyph kind={a.kind} mark={a.mark} className={cn("mt-0.5 text-base", missing && "opacity-35")} />
      <div className="flex min-w-0 flex-1 flex-col gap-0.5">
        <div className="flex items-baseline gap-2">
          <span className={cn("font-medium text-[13px]", missing && "text-muted-foreground")}>{a.name}</span>
          <span className="ml-auto shrink-0 text-2xs text-faint">{looking ? "looking…" : found ? "found" : "not found"}</span>
        </div>
        {found ? (
          <code className="min-w-0 truncate font-mono text-2xs text-faint" title={path}>
            {path}
          </code>
        ) : (
          !looking &&
          (a.install !== null || a.url !== null) && (
            <div className="flex min-w-0 items-center gap-3">
              {a.install !== null && <Install command={a.install} />}
              {a.url !== null && (
                <a
                  href={a.url}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="ml-auto flex shrink-0 items-center gap-1 rounded text-2xs text-muted-foreground underline-offset-2 outline-none hover:text-foreground hover:underline focus-visible:ring-2 focus-visible:ring-ring"
                >
                  Install guide
                  <ExternalLink aria-hidden className="size-3" />
                </a>
              )}
            </div>
          )
        )}
      </div>
    </li>
  );
}

// How a crew is addressed in Deck: one message that names two agents, in
// the composer's mono, each mention in its agent's hue.
function Example() {
  return (
    <p className="font-mono text-xs">
      <span style={{ color: kindColour("claude") }}>$claude</span> <span style={{ color: kindColour("codex") }}>$codex+</span>{" "}
      <span className="text-foreground/85">split the lockout fix</span>
    </p>
  );
}

// The crew: the agent CLIs on the engine's PATH, found with their path or
// with how to install them. Type on the backdrop, parted by hairlines.
function Crew({
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
  const found = AGENT_HELP.filter((a) => clis?.clis.some((c) => c.kind === a.kind && c.path !== null) === true).length;
  return (
    <section aria-label="Your agents" className="flex animate-enter flex-col" style={stagger(2)}>
      <div className="flex items-baseline gap-3 pb-2">
        <h2 className="font-semibold text-[13px]">Your agents</h2>
        <span className="text-2xs text-faint tabular-nums">{clis === null ? "" : `${found} of ${AGENT_HELP.length} on your PATH`}</span>
        <Button variant="ghost-muted" size="xs" className="-mr-2.5 ml-auto self-center" disabled={checking} onClick={onCheck}>
          {checking ? <Loader2 className="animate-spin" /> : <RefreshCw />}
          Check again
        </Button>
      </div>
      {failure?.noAgents === true && (
        <div className="flex items-center gap-3 border-hairline border-t py-2.5">
          <p className="flex-1 text-pretty text-warning-foreground text-xs leading-relaxed">
            Deck found no agent CLI, so it did not open <span className="font-mono">{base(failure.dir)}</span>. Install one, then try
            again.
          </p>
          <Button variant="outline" size="xs" onClick={onRetry}>
            <RotateCcw />
            Try again
          </Button>
        </div>
      )}
      <ul className="agent-clis flex flex-col [&>li]:border-hairline [&>li]:border-t" aria-label="Agent CLIs">
        {AGENT_HELP.map((a) => (
          <AgentRow key={a.kind} a={a} path={clis?.clis.find((c) => c.kind === a.kind)?.path ?? null} looking={clis === null} />
        ))}
      </ul>
    </section>
  );
}

// The grid the first screens are set on: twelve columns across the window.
// The headline holds the lower left from the second column; the actions
// and the crew hold the right from the eighth.
export const firstGrid = "grid grid-cols-1 gap-x-6 px-6 md:grid-cols-12 md:px-8";
export const leftCols = "md:col-span-6 md:col-start-2";
export const rightCols = "md:col-span-5 md:col-start-8 lg:col-span-4 lg:col-start-8";

// A headline in the theme's display face, in two tones: what it is, then,
// quieter, what it is for. `.headline` and `.headline-quiet` let a theme
// restyle the pair.
export function Headline({ loud, quiet, className }: { loud: string; quiet: string; className?: string }) {
  return (
    <h1
      className={cn(
        "headline font-display text-balance font-semibold text-[clamp(2.75rem,5.6vw,5.25rem)] leading-[0.95] tracking-[-0.045em] [font-family:var(--font-display,inherit)]",
        className,
      )}
    >
      {loud}{" "}
      <span className="headline-quiet block font-normal text-muted-foreground">{quiet}</span>
    </h1>
  );
}

// The window's top strip on the first screens: the wordmark over the
// headline's column, and the way back to the open projects.
export function FirstStrip({ onBack }: { onBack?: (() => void) | undefined }) {
  return (
    <div className={cn("drag h-13 shrink-0 items-center", firstGrid)}>
      <div className={cn("hidden md:block", leftCols)}>
        <Wordmark />
      </div>
      <div className="flex justify-end md:col-span-5 md:col-start-8">
        {onBack !== undefined && (
          <Button variant="ghost-muted" size="sm" onClick={onBack}>
            <ArrowLeft />
            Back to threads
          </Button>
        )}
      </div>
    </div>
  );
}

// The first screen of a new install, and the palette's "Welcome": what Deck
// is, the ways in, and the crew it can drive. No sheet and no cards: the
// theme's backdrop is the scene, and the words sit on it, editorial and
// asymmetric, the headline low on the left and the actions high on the
// right.
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
  const mac = useMemo(isMac, []);
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
    <main className="welcome-screen flex h-full flex-col overflow-y-auto" data-screen="welcome">
      <FirstStrip onBack={onBack} />
      <div className={cn("flex-1 gap-y-12 pt-[4vh] pb-[max(2.5rem,7vh)]", firstGrid)}>
        <div className={cn("flex animate-enter flex-col gap-6 self-end md:row-start-1", leftCols)}>
          <Headline loud="One deck" quiet="for all your agents" />
          <div className="flex max-w-[30rem] flex-col gap-3">
            <p className="text-pretty text-[15px] text-muted-foreground leading-relaxed">
              Claude Code, Codex, OpenCode and Antigravity in one thread. Hand a task to one, split it between two, race three on the
              same bug, and answer what they ask from one window.
            </p>
            <Example />
          </div>
        </div>
        <div className={cn("flex flex-col gap-10 md:row-start-1 md:self-center", rightCols)}>
          <div className="flex animate-enter flex-col gap-3" style={stagger(1)}>
            {!engine && (
              <Notice tone="warning">
                Deck did not find its engine. Run <code className="font-mono text-foreground/80">npm run build:deck</code>, or set{" "}
                <code className="font-mono text-foreground/80">DECK_BIN</code>.
              </Notice>
            )}
            {error !== null && <Notice tone="error">{error}</Notice>}
            {failure !== null && !failure.noAgents && (
              <StartFailed failure={failure} onLogs={() => void bridge.openLogs()} onRetry={retry} />
            )}
            <div className="-mx-4 flex flex-col gap-0.5">
              <Command
                primary
                className="primary"
                label={opening === null ? "Open a folder…" : `Starting Deck in ${base(opening)}…`}
                keys={opening === null ? [mac ? "⌘" : "Ctrl", "O"] : undefined}
                disabled={!engine || opening !== null}
                onClick={() => {
                  setError(null);
                  void onAdd().then(setError);
                }}
              />
              <Command label="Pair with a Deck…" hint="in a terminal, or on another computer" onClick={onPair} />
            </div>
            <p className="text-pretty text-faint text-xs leading-relaxed">
              Deck starts its engine in the folder you open. The agents work there until you close it.
            </p>
          </div>
          <Crew clis={clis} checking={checking} failure={failure} onCheck={() => void check(true)} onRetry={retry} />
        </div>
      </div>
    </main>
  );
}
