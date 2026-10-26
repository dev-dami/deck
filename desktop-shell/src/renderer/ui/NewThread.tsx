import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import type { CSSProperties, ReactNode } from "react";
import { setPrefs, usePrefs } from "../prefs";
import { ArrowsSplit, FlagCheckered, GitBranch, type IconType, Sparkle } from "./icons";
import { Switch } from "./kit/switch";
import { AGENT_HELP, type StartCard, startCards, withStart } from "./landing";

const START_ICONS: Record<StartCard["id"], IconType> = { one: Sparkle, split: ArrowsSplit, race: FlagCheckered };

// A kind's glyph: the theme's, else the CLI's own mark.
const markOf = (theme: Theme | null, kind: string) => kindMark(theme, kind) || AGENT_HELP.find((h) => h.kind === kind)?.mark || "●";

// The new-thread screen, the room's first impression: the crew the Deck
// found, each glyph in a disc of its hue over a halo of their light; the
// question; the composer (children); then three ways to start and whether
// the thread gets a branch of its own. A start card puts its prefix in the
// draft.
export function NewThread({
  loaded,
  project,
  kinds,
  theme,
  draft,
  setDraft,
  onFocus,
  canIsolate,
  children,
}: {
  // The thread list arrived; before that, the screen says it connects.
  loaded: boolean;
  project: string;
  kinds: string[];
  theme: Theme | null;
  draft: string;
  setDraft: (s: string) => void;
  onFocus: () => void;
  // The Deck can put a thread on a worktree and branch of its own.
  canIsolate: boolean;
  children: ReactNode;
}) {
  const { isolate } = usePrefs();
  return (
    <div className="welcome relative flex min-h-0 flex-1 flex-col items-center overflow-y-auto px-6">
      <div aria-hidden className="new-thread-halo pointer-events-none absolute inset-x-0 top-0 h-[70%]" />
      <div className="relative my-auto flex w-full max-w-(--chat-max) flex-col items-center gap-7 py-10">
        <div className="flex flex-col items-center gap-5">
          {kinds.length > 0 && (
            <ul className="crew flex items-center gap-2.5" aria-label="Agents on this Deck">
              {kinds.map((k, i) => (
                <li
                  key={k}
                  className="liquid-clear grid size-11 animate-enter place-items-center rounded-full font-mono text-[15px]"
                  style={{ color: kindColour(k), animationDelay: `${i * 50}ms` } as CSSProperties}
                  title={AGENT_HELP.find((h) => h.kind === k)?.name ?? k}
                >
                  <span aria-hidden>{markOf(theme, k)}</span>
                  <span className="sr-only">{k}</span>
                </li>
              ))}
            </ul>
          )}
          <div className="flex flex-col items-center gap-2 text-center">
            <h2 className="welcome-title text-balance font-semibold text-[26px] text-foreground leading-tight tracking-tight">
              {loaded ? (
                <>
                  What should we build{project === "" ? "" : " in "}
                  {project !== "" && <span className="font-mono text-[0.86em] text-muted-foreground">{project}</span>}?
                </>
              ) : (
                "Connecting to Deck…"
              )}
            </h2>
            <p className="text-pretty text-faint text-sm">
              One thread, a crew of agents. Write below, or pick a way to start.
            </p>
          </div>
        </div>
        {children}
        <ul className="start-cards grid w-full grid-cols-3 gap-2.5" aria-label="Ways to start">
          {startCards(kinds).map((s, i) => {
            const Icon = START_ICONS[s.id];
            return (
              <li key={s.id} className="flex">
                <button
                  type="button"
                  className="group/start flex w-full animate-enter cursor-pointer flex-col gap-2.5 rounded-xl bg-surface-1 p-3.5 text-left outline-none ring-1 ring-hairline transition-[translate,box-shadow] hover:-translate-y-px hover:ring-hairline-strong focus-visible:ring-2 focus-visible:ring-ring"
                  style={{ animationDelay: `${120 + i * 50}ms` }}
                  onClick={() => {
                    setDraft(withStart(s.prefix, draft));
                    onFocus();
                  }}
                >
                  <span aria-hidden className="grid size-7 place-items-center rounded-lg bg-hover text-muted-foreground transition-colors group-hover/start:text-foreground">
                    <Icon className="size-4" weight="duotone" />
                  </span>
                  <span className="flex min-w-0 flex-col gap-0.5">
                    <span className="font-medium text-foreground text-sm">{s.title}</span>
                    <span className="text-pretty text-muted-foreground text-xs leading-snug">{s.line}</span>
                  </span>
                  <span className="mt-auto truncate font-mono text-2xs text-faint">{s.prefix.trim()}</span>
                </button>
              </li>
            );
          })}
        </ul>
        {canIsolate && (
          <div className="own-branch flex w-full items-center gap-2.5 px-1 text-muted-foreground text-xs">
            <Switch label="On its own branch" checked={isolate} onChange={(v) => setPrefs({ isolate: v })} />
            <span>On its own branch</span>
            <span className="inline-flex items-center gap-1 font-mono text-2xs text-faint">
              <GitBranch className="size-3" />
              deck/&lt;id&gt;
            </span>
          </div>
        )}
      </div>
    </div>
  );
}
