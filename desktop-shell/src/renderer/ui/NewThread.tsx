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

// A start's prefix, its agents in their hues: "$claude $codex+".
function Prefix({ text }: { text: string }) {
  return (
    <span className="font-mono text-[12.5px]">
      {text
        .trim()
        .split(/(\$[a-z]+)/)
        .map((part, i) =>
          part.startsWith("$") ? (
            <span key={i} style={{ color: kindColour(part.slice(1)) }}>
              {part}
            </span>
          ) : (
            <span key={i} className="text-muted-foreground">
              {part}
            </span>
          ),
        )}
    </span>
  );
}

// Deck's new-thread screen, a work surface set in type: the question, the
// composer, and the ways to set the crew on a task, set as a ruled table
// rather than cards. Under it, the crew this Deck runs, each
// agent as its glyph in its hue and the $name that reaches it: a name
// lights while the draft addresses it, and a click adds it or takes it out.
// Last, whether the thread gets a branch of its own.
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
  return (
    <div className="welcome flex min-h-0 flex-1 flex-col overflow-y-auto">
      {/* The block sits a little above the middle, where the eye lands, and
          scrolls when the window is too short for it. */}
      <div className="@container/new mx-auto my-auto flex w-full max-w-(--chat-max) flex-col gap-5 px-6 pt-8 pb-[max(2rem,10vh)]">
        <h2 className="welcome-title text-balance font-semibold text-[30px] text-foreground leading-[1.15] tracking-[-0.02em]">
          {loaded ? (
            <>
              What should we build{project === "" ? "" : " in "}
              {project !== "" && <span className="font-mono font-medium text-[0.8em] text-muted-foreground tracking-normal">{project}</span>}?
            </>
          ) : (
            "Connecting to Deck…"
          )}
        </h2>
        {children}
        <section className="flex flex-col pt-1" aria-labelledby="ways-to-start">
          <h3 id="ways-to-start" className="pb-1.5 text-faint text-xs">
            Ways to start
          </h3>
          <ul className="start-cards flex flex-col border-hairline border-t" aria-label="Ways to start">
            {startCards(kinds).map((s, i) => (
              <li key={s.id} className="animate-enter border-hairline border-b" style={{ animationDelay: `${i * 40}ms` }}>
                <button
                  type="button"
                  className="grid min-h-9 w-full cursor-pointer grid-cols-[7.5rem_minmax(0,1fr)] items-baseline gap-x-4 py-2 text-left outline-none transition-colors hover:bg-hover focus-visible:bg-hover @min-[34rem]/new:grid-cols-[7.5rem_10rem_minmax(0,1fr)]"
                  onClick={() => {
                    setDraft(withStart(s.prefix, draft));
                    onFocus();
                  }}
                >
                  <span className="pl-1 text-foreground text-sm">{s.title}</span>
                  <Prefix text={s.prefix} />
                  <span className="col-span-2 truncate pl-1 text-faint text-xs @min-[34rem]/new:col-span-1 @min-[34rem]/new:pl-0">{s.line}</span>
                </button>
              </li>
            ))}
          </ul>
        </section>
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
          <span className="-mr-2 text-faint text-xs">Write to</span>
          <ul className="crew flex flex-wrap items-baseline gap-x-4 gap-y-1" aria-label="Agents on this Deck">
            {kinds.map((k) => {
              const on = addressed.includes(k);
              return (
                <li key={k}>
                  <button
                    type="button"
                    className={cn(
                      "inline-flex cursor-pointer items-baseline gap-1.5 border-b py-0.5 font-mono text-[12.5px] outline-none transition-colors focus-visible:text-foreground",
                      on ? "border-(--hue) text-(--hue)" : "border-transparent text-muted-foreground hover:text-foreground",
                    )}
                    aria-pressed={on}
                    title={`${AGENT_HELP.find((h) => h.kind === k)?.name ?? k}: ${on ? "take out of" : "add to"} the message`}
                    style={{ "--hue": kindColour(k) } as CSSProperties}
                    onClick={() => {
                      setDraft(toggleAgent(draft, k));
                      onFocus();
                    }}
                  >
                    <span aria-hidden className="text-(--hue)">
                      {markOf(theme, k)}
                    </span>
                    ${k}
                  </button>
                </li>
              );
            })}
            {kinds.length === 0 && <li className="text-faint text-sm">This Deck found no agent CLIs yet.</li>}
          </ul>
          {canIsolate && (
            <div className="own-branch ml-auto flex items-center gap-2 text-muted-foreground text-xs">
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
    </div>
  );
}
