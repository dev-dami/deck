import { activeAgents, toggleAgent } from "@web/composer";
import type { Theme } from "@web/protocol";
import { kindColour, kindMark } from "@web/theme";
import type { CSSProperties, ReactNode } from "react";
import { cn } from "../lib/cn";
import { setPrefs, usePrefs } from "../prefs";
import { GitBranch } from "./icons";
import { Switch } from "./kit/switch";
import { AGENT_HELP, startCards, withStart } from "./landing";

// A kind's glyph: the theme's, else the CLI's own mark.
const markOf = (theme: Theme | null, kind: string) => kindMark(theme, kind) || AGENT_HELP.find((h) => h.kind === kind)?.mark || "●";

// Deck's new-thread screen, set in type. Deck is a crew you address by
// name, so the screen is the crew: the question, the composer, then the
// roster of agents this Deck runs, each as its glyph in its hue and the
// $name that reaches it. A name lights up while the draft addresses it, and
// a click adds it or takes it out. Beside the roster, the ways to start a
// crew on one task (a start puts its prefix in the draft), and whether the
// thread gets a branch of its own.
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
  const addressed = activeAgents(draft);
  const row =
    "flex min-h-8 w-full cursor-pointer items-baseline gap-3 rounded-lg px-2 py-1.5 text-left outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring";
  return (
    <div className="welcome flex min-h-0 flex-1 flex-col overflow-y-auto px-6">
      <div className="mx-auto my-auto flex w-full max-w-(--chat-max) flex-col gap-6 py-10">
        <h2 className="welcome-title text-balance px-1 font-semibold text-[28px] text-foreground leading-tight tracking-tight">
          {loaded ? (
            <>
              What should we build{project === "" ? "" : " in "}
              {project !== "" && <span className="font-mono font-medium text-[0.82em] text-muted-foreground">{project}</span>}?
            </>
          ) : (
            "Connecting to Deck…"
          )}
        </h2>
        {children}
        <div className="grid grid-cols-1 gap-x-8 gap-y-5 min-[720px]:grid-cols-[1fr_1fr]">
          <section className="flex flex-col gap-1">
            <h3 className="px-2 pb-1 font-medium text-faint text-xs">The crew · write a name to reach an agent</h3>
            <ul className="crew flex flex-col" aria-label="Agents on this Deck">
              {kinds.map((k, i) => {
                const on = addressed.includes(k);
                return (
                  <li key={k} className="animate-enter" style={{ animationDelay: `${i * 40}ms` }}>
                    <button
                      type="button"
                      className={row}
                      aria-pressed={on}
                      style={{ "--hue": kindColour(k) } as CSSProperties}
                      onClick={() => {
                        setDraft(toggleAgent(draft, k));
                        onFocus();
                      }}
                    >
                      <span aria-hidden className={cn("w-5 shrink-0 font-mono text-(--hue) text-[13px] transition-opacity", !on && "opacity-60")}>
                        {markOf(theme, k)}
                      </span>
                      <span className={cn("font-mono text-[13px] transition-colors", on ? "text-(--hue)" : "text-foreground")}>${k}</span>
                      <span className="ml-auto truncate text-faint text-xs">{AGENT_HELP.find((h) => h.kind === k)?.name ?? k}</span>
                    </button>
                  </li>
                );
              })}
              {kinds.length === 0 && <li className="px-2 py-1.5 text-faint text-sm">This Deck found no agent CLIs yet.</li>}
            </ul>
          </section>
          <section className="flex flex-col gap-1">
            <h3 className="px-2 pb-1 font-medium text-faint text-xs">Ways to start</h3>
            <ul className="start-cards flex flex-col" aria-label="Ways to start">
              {startCards(kinds).map((s, i) => (
                <li key={s.id} className="animate-enter" style={{ animationDelay: `${80 + i * 40}ms` }}>
                  <button
                    type="button"
                    className={cn(row, "flex-col items-stretch gap-0.5")}
                    onClick={() => {
                      setDraft(withStart(s.prefix, draft));
                      onFocus();
                    }}
                  >
                    <span className="flex items-baseline gap-3">
                      <span className="text-foreground text-sm">{s.title}</span>
                      <span className="ml-auto shrink-0 font-mono text-muted-foreground text-xs">{s.prefix.trim()}</span>
                    </span>
                    <span className="text-pretty text-faint text-xs">{s.line}</span>
                  </button>
                </li>
              ))}
            </ul>
          </section>
        </div>
        {canIsolate && (
          <div className="own-branch flex items-center gap-2.5 px-2 text-muted-foreground text-xs">
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
