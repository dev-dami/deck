import { kindColour } from "@web/theme";
import {
  ArrowLeft,
  Check,
  CircleAlert,
  Copy,
  ExternalLink,
  FolderOpen,
  Link2,
  Loader2,
  RefreshCw,
  RotateCcw,
  ScrollText,
} from "./icons";
import { type CSSProperties, type ReactNode, useCallback, useEffect, useState } from "react";
import type { AgentClis, DeckBridge } from "../../shared/bridge";
import { cn } from "../lib/cn";
import { Badge } from "./kit/badge";
import { Button } from "./kit/button";
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

// Things that enter one after another: each rises 4px and fades in, 50ms
// after the one before it.
export const stagger = (i: number): CSSProperties => ({ animationDelay: `${i * 50}ms` });

// Deck's mark, the app icon's prompt: a chevron in the accent and the
// cursor, on a tile of glass. The cursor blinks a few times as the window
// opens, then holds still.
export function DeckMark({ className }: { className?: string }) {
  return (
    <span aria-hidden className={cn("deck-mark liquid-thick grid size-16 shrink-0 place-items-center rounded-[18px]", className)}>
      <svg viewBox="0 0 64 64" className="size-full" fill="none">
        <defs>
          <linearGradient id="deck-mark-chevron" x1="0" y1="0" x2="0" y2="1">
            <stop offset="0" style={{ stopColor: "color-mix(in oklab, var(--primary) 55%, #fff)" }} />
            <stop offset="1" style={{ stopColor: "var(--primary)" }} />
          </linearGradient>
        </defs>
        <path d="M19.5 22.5 31 32l-11.5 9.5" stroke="url(#deck-mark-chevron)" strokeWidth="4.75" strokeLinecap="round" strokeLinejoin="round" />
        <rect className="deck-cursor" x="35" y="37" width="12" height="4.75" rx="2.375" fill="currentColor" />
      </svg>
    </span>
  );
}

// A message on the backdrop: a pane of glass with a tinted rim.
export function Notice({ tone, className, children }: { tone: "error" | "warning"; className?: string; children: ReactNode }) {
  return (
    <p
      className={cn(
        "liquid flex animate-enter items-start gap-2.5 rounded-2xl px-4 py-3 text-pretty text-sm",
        tone === "error" ? "error ring-1 ring-destructive/35" : "notice ring-1 ring-warning/30",
        className,
      )}
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
    <section
      aria-label="Deck did not start"
      className="start-failed liquid flex animate-enter flex-col gap-3 rounded-(--panel-radius) p-4 ring-1 ring-destructive/30"
      role="alert"
    >
      <p className="flex items-center gap-2.5 text-sm">
        <CircleAlert aria-hidden weight="fill" className="size-4 shrink-0 text-destructive-foreground" />
        <span>
          Deck did not start in <span className="font-mono">{base(failure.dir)}</span>.
        </span>
      </p>
      <pre className="max-h-48 overflow-auto whitespace-pre-wrap break-words rounded-[10px] bg-code/80 px-3 py-2 font-mono text-2xs text-muted-foreground ring-1 ring-hairline">
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

// One of the two ways in: a duotone glyph in clear glass, where it leads,
// what happens there, and its button.
function Path({
  label,
  title,
  icon,
  index,
  children,
  action,
}: {
  label: string;
  title: string;
  icon: ReactNode;
  index: number;
  children: ReactNode;
  action: ReactNode;
}) {
  return (
    <section aria-label={label} className="liquid flex animate-enter gap-3.5 rounded-(--panel-radius) p-4 pr-5" style={stagger(index)}>
      <span aria-hidden className="liquid-clear grid size-10 shrink-0 place-items-center rounded-full text-foreground/85 [&_svg]:size-5">
        {icon}
      </span>
      <div className="flex min-w-0 flex-1 flex-col gap-3 pt-0.5">
        <div className="flex flex-1 flex-col gap-1">
          <h2 className="text-balance font-semibold text-sm tracking-tight">{title}</h2>
          <p className="text-pretty text-muted-foreground text-xs leading-relaxed">{children}</p>
        </div>
        {action}
      </div>
    </section>
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
    <span className="flex min-w-0 items-center gap-0.5 rounded-lg bg-hover py-0.5 pr-0.5 pl-2 ring-1 ring-hairline">
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

// An agent kind's glyph in its hue, in a bead of glass tinted with it: the
// same kind wears the same bead on every page.
export function AgentOrb({ kind, mark, className }: { kind: string; mark: string; className?: string }) {
  const hue = kindColour(kind);
  return (
    <span
      aria-hidden
      className={cn("grid size-8 shrink-0 place-items-center rounded-full font-mono text-sm leading-none", className)}
      style={{
        color: hue,
        background: `color-mix(in srgb, ${hue} 14%, transparent)`,
        boxShadow: `inset 0 0 0 1px color-mix(in srgb, ${hue} 26%, transparent), inset 0 1px 0 0 var(--glass-spec)`,
      }}
    >
      {mark || kind.slice(0, 1)}
    </span>
  );
}

// One agent CLI on the roster: its glyph in its hue, its name, and where
// Deck found it, or how to get it.
function AgentRow({ a, path, looking }: { a: AgentHelp; path: string | null; looking: boolean }) {
  const found = path !== null;
  return (
    <li className="flex min-w-0 flex-wrap items-center gap-x-3 gap-y-1.5 px-4 py-3" data-kind={a.kind} data-found={found}>
      <AgentOrb kind={a.kind} mark={a.mark} className={cn(!found && !looking && "opacity-55")} />
      <span className={cn("w-28 shrink-0 font-medium text-sm", !found && !looking && "text-muted-foreground")}>{a.name}</span>
      {looking ? (
        <span className="text-faint text-xs">Looking…</span>
      ) : found ? (
        <>
          <Badge variant="success" className="gap-1">
            <span aria-hidden className="size-1.5 rounded-full bg-success" />
            <span className="first-letter:uppercase">found</span>
          </Badge>
          <code className="min-w-0 flex-1 truncate font-mono text-2xs text-faint" title={path}>
            {path}
          </code>
        </>
      ) : (
        <>
          <Badge variant="muted">
            <span className="first-letter:uppercase">not found</span>
          </Badge>
          <span className="flex min-w-0 flex-1 items-center gap-3">
            {a.install !== null && <Install command={a.install} />}
            {a.url !== null && (
              <a
                href={a.url}
                target="_blank"
                rel="noopener noreferrer"
                className="ml-auto flex shrink-0 items-center gap-1 rounded-full px-1.5 text-muted-foreground text-xs outline-none transition-colors hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              >
                Install guide
                <ExternalLink aria-hidden className="size-3" />
              </a>
            )}
          </span>
        </>
      )}
    </li>
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
  const found = AGENT_HELP.filter((a) => clis?.clis.some((c) => c.kind === a.kind && c.path !== null) === true).length;
  return (
    <section aria-label="Your agents" className="liquid flex animate-enter flex-col rounded-(--panel-radius) sm:col-span-2" style={stagger(2)}>
      <header className="flex items-center gap-3 px-5 pt-4 pb-3">
        <div className="flex min-w-0 flex-1 flex-col gap-0.5">
          <h2 className="font-semibold text-[15px] tracking-tight">Your agents</h2>
          <p className="text-pretty text-muted-foreground text-xs">
            {clis === null
              ? "Deck drives the agent CLIs it finds on your PATH."
              : `${found} of ${AGENT_HELP.length} found on your PATH. Deck runs each one as it is.`}
          </p>
        </div>
        <Button variant="ghost-muted" size="xs" disabled={checking} onClick={onCheck}>
          {checking ? <Loader2 className="animate-spin" /> : <RefreshCw />}
          Check again
        </Button>
      </header>
      {failure?.noAgents === true && (
        <div className="mx-3 mb-2 flex items-center gap-3 rounded-xl bg-warning/10 px-3 py-2.5 ring-1 ring-warning/25">
          <p className="flex-1 text-pretty text-xs leading-relaxed">
            Deck found no agent CLI, so it did not open <span className="font-mono">{base(failure.dir)}</span>. Install one,
            then try again.
          </p>
          <Button variant="outline" size="sm" onClick={onRetry}>
            <RotateCcw />
            Try again
          </Button>
        </div>
      )}
      <ul
        className="agent-clis mx-1.5 mb-1.5 flex flex-col rounded-[calc(var(--panel-radius)-6px)] bg-hover/60 [&>li+li]:border-hairline [&>li+li]:border-t"
        aria-label="Agent CLIs"
      >
        {AGENT_HELP.map((a) => (
          <AgentRow key={a.kind} a={a} path={clis?.clis.find((c) => c.kind === a.kind)?.path ?? null} looking={clis === null} />
        ))}
      </ul>
    </section>
  );
}

// The first screen of a new install, and the palette's "Welcome": what Deck
// is, the two ways in, and the agent CLIs it can drive. No sheet: the cards
// are glass straight on the backdrop.
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
    <main className="welcome-screen flex h-full flex-col overflow-y-auto" data-screen="welcome">
      <div className="drag flex h-13 shrink-0 items-center px-3">
        {onBack !== undefined && (
          <Button variant="outline" size="sm" onClick={onBack}>
            <ArrowLeft />
            Back to threads
          </Button>
        )}
      </div>
      <div className="flex flex-1 justify-center px-6">
        <div className="flex w-full max-w-[40rem] flex-col gap-7 pt-[max(0.5rem,4vh)] pb-16">
          <header className="welcome-halo relative flex animate-enter flex-col items-center gap-5 text-center">
            <DeckMark />
            <div className="flex flex-col items-center gap-2">
              <h1 className="text-balance font-semibold text-[2rem] leading-tight tracking-[-0.025em]">One deck for all your agents</h1>
              <p className="max-w-lg text-pretty text-[15px] text-muted-foreground leading-relaxed">
                Open a project and run your agent CLIs side by side, in one window.
              </p>
            </div>
          </header>
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
          <div className="grid gap-3 sm:grid-cols-2">
            <Path
              label="Open a folder"
              title="A folder on this computer"
              icon={<FolderOpen weight="duotone" />}
              index={0}
              action={
                <Button
                  className="primary self-start"
                  disabled={!engine || opening !== null}
                  onClick={() => {
                    setError(null);
                    void onAdd().then(setError);
                  }}
                >
                  {opening === null ? <FolderOpen /> : <Loader2 className="animate-spin" />}
                  {opening === null ? "Open a folder" : `Starting Deck in ${base(opening)}…`}
                </Button>
              }
            >
              Deck starts its engine in the folder. Agents work there until you close it.
            </Path>
            <Path
              label="Pair with a Deck"
              title="A Deck that already runs"
              icon={<Link2 weight="duotone" />}
              index={1}
              action={
                <Button variant="outline" className="self-start" onClick={onPair}>
                  <Link2 />
                  Pair with a Deck
                </Button>
              }
            >
              Watch a Deck that runs in a terminal or on another computer.
            </Path>
            <Agents clis={clis} checking={checking} failure={failure} onCheck={() => void check(true)} onRetry={retry} />
          </div>
        </div>
      </div>
    </main>
  );
}
