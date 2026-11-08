import { kindColour } from "@web/theme";
import { ArrowLeft, ArrowRight, Check, CircleAlert, Copy, ExternalLink, FolderPlus, Laptop, Loader2, RefreshCw, RotateCcw, ScrollText } from "./icons";
import { type ComponentProps, type CSSProperties, type ReactNode, useCallback, useEffect, useMemo, useState } from "react";
import type { AgentClis, DeckBridge } from "../../shared/bridge";
import { cn } from "../lib/cn";
import { Button } from "./kit/button";
import { DeckMark } from "./kit/mark";
import { ArtFigure } from "./ArtFigure";
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

// An install command in mono, with a button that copies it.
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

// One agent CLI on the roster, on one line: its glyph in its hue, its
// name, found or not in plain words, then the path Deck found or the
// command that installs it, and its guide.
function AgentRow({ a, path, looking }: { a: AgentHelp; path: string | null; looking: boolean }) {
  const found = path !== null;
  const missing = !found && !looking;
  return (
    <li
      className="grid min-h-9 grid-cols-[1.25rem_minmax(0,8rem)_4.5rem_minmax(0,1fr)_auto] items-center gap-x-3 py-1.5 [@media(max-height:680px)]:min-h-8 [@media(max-height:680px)]:py-1"
      data-kind={a.kind}
      data-found={found}
    >
      <AgentGlyph kind={a.kind} mark={a.mark} className={cn("text-[15px]", missing && "opacity-40")} />
      <span className={cn("truncate font-medium text-[13px]", missing && "text-muted-foreground")}>{a.name}</span>
      <span className={cn("text-xs", found ? "text-foreground/80" : "text-faint")}>{looking ? "looking…" : found ? "found" : "not found"}</span>
      {found ? (
        <code className="min-w-0 truncate font-mono text-2xs text-faint" title={path}>
          {path}
        </code>
      ) : (
        <span className="min-w-0">{!looking && a.install !== null && <Install command={a.install} />}</span>
      )}
      {missing && a.url !== null ? (
        <a
          href={a.url}
          target="_blank"
          rel="noopener noreferrer"
          className="flex shrink-0 items-center gap-1 rounded text-2xs text-muted-foreground underline-offset-2 outline-none hover:text-foreground hover:underline focus-visible:ring-2 focus-visible:ring-ring"
        >
          Install guide
          <ExternalLink aria-hidden className="size-3" />
        </a>
      ) : (
        <span />
      )}
    </li>
  );
}

// How a crew is addressed in Deck: one message that names two agents, as
// the composer would hold it, each mention in its agent's hue.
function Example() {
  return (
    <p className="first-example liquid flex max-w-full items-center gap-2 truncate rounded-full px-3.5 py-1.5 font-mono text-xs">
      <span className="text-faint">›</span>
      <span>
        <span style={{ color: kindColour("claude") }}>$claude</span> <span style={{ color: kindColour("codex") }}>$codex</span>{" "}
        <span className="text-muted-foreground">split the lockout fix</span>
      </span>
    </p>
  );
}

// The crew: the agent CLIs on the engine's PATH, found with their path or
// with how to install them. A table in type on a card of glass, parted
// by hairlines.
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
    <section aria-label="Your agents" className="first-card liquid flex animate-enter flex-col rounded-(--radius) px-4 pt-2 pb-2.5" style={stagger(2)}>
      <div className="flex h-8 items-center gap-3 border-hairline border-b">
        <h2 className="font-medium text-[13px]">Your agents</h2>
        <span className="text-2xs text-faint tabular-nums">{clis === null ? "" : `${found} of ${AGENT_HELP.length} on your PATH`}</span>
        <Button variant="ghost-muted" size="xs" className="-mr-2.5 ml-auto" disabled={checking} onClick={onCheck}>
          {checking ? <Loader2 className="animate-spin" /> : <RefreshCw />}
          Check again
        </Button>
      </div>
      {failure?.noAgents === true && (
        <div className="flex items-center gap-3 border-hairline border-b py-2.5">
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
      <ul className="agent-clis flex flex-col [&>li+li]:border-hairline [&>li+li]:border-t" aria-label="Agent CLIs">
        {AGENT_HELP.map((a) => (
          <AgentRow key={a.kind} a={a} path={clis?.clis.find((c) => c.kind === a.kind)?.path ?? null} looking={clis === null} />
        ))}
      </ul>
    </section>
  );
}

// The window's top strip on the first screens: Deck's mark and name where
// a title bar would put them (after the traffic lights on a Mac), and the
// way back to the open projects.
export function FirstStrip({ onBack, className }: { onBack?: (() => void) | undefined; className?: string }) {
  const mac = useMemo(isMac, []);
  return (
    <div className={cn("drag flex shrink-0 items-center gap-2 pr-3", mac ? "h-9 pl-[4.75rem]" : "h-11 pl-5", className)}>
      <span className="flex items-center gap-2 font-semibold text-[13px] tracking-tight">
        <DeckMark className="h-3" />
        Deck
      </span>
      {onBack !== undefined && (
        <Button variant="ghost-muted" size="sm" className="ml-auto" onClick={onBack}>
          <ArrowLeft />
          Back to threads
        </Button>
      )}
    </div>
  );
}

// One of the first screens' actions as a card of glass (design-glass.md,
// "The first screens"): an icon, what it does, a line on when, and on the
// right its keys over the arrow. The primary one wears the accent on its
// rim and in its icon.
export function ActionCard({
  icon,
  label,
  hint,
  keys,
  primary = false,
  className,
  ...props
}: { icon: ReactNode; label: ReactNode; hint?: ReactNode | undefined; keys?: string[] | undefined; primary?: boolean } & ComponentProps<"button">) {
  return (
    <button
      type="button"
      className={cn(
        "first-action liquid group/act relative flex w-full cursor-pointer items-start gap-3 rounded-(--radius) p-3.5 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-default disabled:opacity-50",
        primary && "first-primary",
        className,
      )}
      {...props}
    >
      <span
        aria-hidden
        className={cn(
          "grid size-8 shrink-0 place-items-center rounded-full [&>svg]:size-4",
          primary ? "bg-primary/15 text-primary" : "bg-hover text-muted-foreground",
        )}
      >
        {icon}
      </span>
      <span className="flex min-w-0 flex-1 flex-col gap-0.5 pt-1">
        <span className="truncate font-medium text-[14px] text-foreground">{label}</span>
        {hint !== undefined && <span className="text-pretty text-muted-foreground text-xs leading-relaxed">{hint}</span>}
      </span>
      <span className="flex shrink-0 flex-col items-end justify-between gap-2 self-stretch pt-1.5">
        {keys !== undefined ? (
          <span className="flex gap-1 font-normal text-faint text-xs">
            {keys.map((k) => (
              <kbd key={k} className="min-w-3 text-center font-sans">
                {k}
              </kbd>
            ))}
          </span>
        ) : (
          <span />
        )}
        <ArrowRight
          aria-hidden
          className="size-4 text-faint transition-[translate,color] duration-150 group-hover/act:translate-x-0.5 group-hover/act:text-foreground group-disabled/act:translate-x-0"
        />
      </span>
    </button>
  );
}

// The first screens' frame (design-glass.md, "The first screens"): the
// theme's picture full bleed behind everything, a scrim that pools the
// canvas behind the column so the type reads and leaves the picture whole
// at the edges, the strip along the top, and one centred column that
// scrolls when the window is short.
export function FirstFrame({ name, onBack, children }: { name: "welcome" | "connect"; onBack?: (() => void) | undefined; children: ReactNode }) {
  return (
    <main
      className={cn("first-screen relative isolate flex h-full flex-col overflow-hidden", name === "welcome" ? "welcome-screen" : "connect")}
      data-screen={name}
    >
      <ArtFigure className="first-art absolute inset-0 -z-10" creditClassName="first-credit absolute right-3 bottom-3">
        <div aria-hidden className="first-scrim pointer-events-none absolute inset-0" />
      </ArtFigure>
      <FirstStrip onBack={onBack} className="relative" />
      <div className="relative flex min-h-0 flex-1 overflow-y-auto">
        <div className="mx-auto my-auto flex w-full max-w-[44rem] flex-col gap-4 px-6 pt-4 pb-14 [@media(max-height:760px)]:pb-10">{children}</div>
      </div>
    </main>
  );
}

// The head of a first screen, centred: Deck's mark, the question in two
// tones (the claim, then quieter, the rest of the line), a line on what
// it is, and what goes under it.
export function FirstHero({ loud, quiet, lede, children }: { loud: string; quiet: string; lede: ReactNode; children?: ReactNode }) {
  return (
    <div className="first-hero flex animate-enter flex-col items-center gap-3 pb-3 text-center [@media(max-height:760px)]:gap-2.5 [@media(max-height:760px)]:pb-1">
      <DeckMark className="h-7 w-auto [@media(max-height:760px)]:h-5" />
      <h1 className="headline text-balance font-medium text-[clamp(1.875rem,3.4vw,2.5rem)] leading-[1.08] tracking-[-0.035em] [font-family:var(--font-display,inherit)]">
        {loud} <span className="headline-quiet">{quiet}</span>
      </h1>
      <p className="max-w-[34rem] text-pretty text-[14px] text-muted-foreground leading-relaxed">{lede}</p>
      {children}
    </div>
  );
}

// What went wrong, on a card of glass so it reads over the picture.
export function FirstNotes({ children }: { children: ReactNode }) {
  return <div className="first-card liquid flex animate-enter flex-col gap-3 rounded-(--radius) p-4">{children}</div>;
}

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
    <FirstFrame name="welcome" onBack={onBack}>
      <FirstHero
        loud="One deck"
        quiet="for all your agents"
        lede="Claude Code, Codex, OpenCode, Antigravity and Pi in one thread. Hand a task to one, split it between two, or race three on the same bug."
      >
        <Example />
      </FirstHero>
      {(!engine || error !== null || (failure !== null && !failure.noAgents)) && (
        <FirstNotes>
          {!engine && (
            <Notice tone="warning">
              Deck did not find its engine. Run <code className="font-mono text-foreground/80">npm run build:deck</code>, or set{" "}
              <code className="font-mono text-foreground/80">DECK_BIN</code>.
            </Notice>
          )}
          {error !== null && <Notice tone="error">{error}</Notice>}
          {failure !== null && !failure.noAgents && <StartFailed failure={failure} onLogs={() => void bridge.openLogs()} onRetry={retry} />}
        </FirstNotes>
      )}
      <div className="grid animate-enter gap-3 sm:grid-cols-2" style={stagger(1)}>
        <ActionCard
          primary
          className="primary"
          icon={<FolderPlus />}
          label={opening === null ? "Open a folder…" : `Starting Deck in ${base(opening)}…`}
          hint="Work on a project on this computer. Deck runs its own engine there."
          keys={opening === null ? [mac ? "⌘" : "Ctrl", "O"] : undefined}
          disabled={!engine || opening !== null}
          onClick={() => {
            setError(null);
            void onAdd().then(setError);
          }}
        />
        <ActionCard icon={<Laptop />} label="Pair with a Deck…" hint="One you run in a terminal, or on another computer." onClick={onPair} />
      </div>
      <Crew clis={clis} checking={checking} failure={failure} onCheck={() => void check(true)} onRetry={retry} />
    </FirstFrame>
  );
}
