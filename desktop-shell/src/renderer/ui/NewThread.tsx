import { type ReactNode, useState } from "react";
import { cn } from "../lib/cn";
import { setPrefs, usePrefs } from "../prefs";
import type { Workflow, WorkflowId } from "../workflows";
import { ChevronDown, GitBranch, Users, X } from "./icons";
import { CARD } from "./kit/card";
import { Switch } from "./kit/switch";

// Deck's new-thread screen, a work surface set in type: the question and
// the composer. A thread is one agent, as in its own CLI; a crew is a
// choice: "Start a multi-agent workflow" opens the workflows (workflows.ts)
// as a ruled table, and the one picked shows as a card over the composer,
// with how it ends, until it is sent or put back. The agent the thread
// starts with is the composer's agent menu (LeadPicker). Last, whether the
// thread gets a branch of its own.
export function NewThread({
  loaded,
  project,
  onFocus,
  canIsolate,
  workflows,
  picked,
  onPick,
  children,
}: {
  // The thread list arrived; before that, the screen says it connects.
  loaded: boolean;
  project: string;
  onFocus: () => void;
  // The Deck can put a thread on a worktree and branch of its own.
  canIsolate: boolean;
  // The multi-agent workflows this Deck can start (none for a paired Deck),
  // and the one picked for the next send.
  workflows: Workflow[];
  picked: WorkflowId | null;
  onPick: (id: WorkflowId | null) => void;
  children: ReactNode;
}) {
  const { isolate } = usePrefs();
  const [menu, setMenu] = useState(false);
  const chosen = workflows.find((w) => w.id === picked) ?? null;
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
        {chosen !== null && (
          <div className={cn(CARD, "workflow-chip flex items-start gap-2.5 px-3 py-2 text-xs")} data-workflow={chosen.id} role="status">
            <Users aria-hidden className="mt-0.5 size-3.5 shrink-0 text-primary" weight="fill" />
            <span className="flex min-w-0 flex-1 flex-col gap-0.5">
              <span className="text-foreground">
                <span className="font-medium">{chosen.title}</span>
                <span className="text-muted-foreground"> · {chosen.line}</span>
              </span>
              <span className="text-faint">{chosen.race ? "Send opens the race builder with your goal; it " + chosen.ends : `Send starts it; it ${chosen.ends}.`}</span>
            </span>
            <button
              type="button"
              className="grid size-5 shrink-0 cursor-pointer place-items-center rounded-md text-faint outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              aria-label="Back to one agent"
              title="Back to one agent"
              onClick={() => {
                onPick(null);
                onFocus();
              }}
            >
              <X className="size-3" />
            </button>
          </div>
        )}
        {children}
        {workflows.length > 0 && (
          <section className="flex flex-col pt-1" aria-label="Multi-agent workflows">
            <button
              type="button"
              className="workflows-toggle inline-flex cursor-pointer items-center gap-1.5 self-start rounded-md py-1 text-muted-foreground text-xs outline-none hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
              aria-expanded={menu}
              onClick={() => setMenu((m) => !m)}
            >
              <Users aria-hidden className="size-3.5" />
              Start a multi-agent workflow
              <ChevronDown aria-hidden className={cn("size-3 transition-transform", menu && "rotate-180")} />
            </button>
            {menu && (
              <ul className="start-cards mt-1.5 flex flex-col border-hairline border-t" aria-label="Workflows">
                {workflows.map((w, i) => (
                  <li key={w.id} className="animate-enter border-hairline border-b" style={{ animationDelay: `${i * 40}ms` }}>
                    <button
                      type="button"
                      className="grid min-h-9 w-full cursor-pointer grid-cols-[8.5rem_minmax(0,1fr)] items-baseline gap-x-4 py-2 text-left outline-none transition-colors hover:bg-hover focus-visible:bg-hover aria-pressed:bg-selected"
                      aria-pressed={picked === w.id}
                      data-workflow={w.id}
                      onClick={() => {
                        onPick(picked === w.id ? null : w.id);
                        setMenu(false);
                        onFocus();
                      }}
                    >
                      <span className="pl-1 text-foreground text-sm">{w.title}</span>
                      <span className="min-w-0 truncate text-faint text-xs" title={w.line}>
                        {w.line}
                      </span>
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </section>
        )}
        <div className="flex flex-wrap items-center gap-x-6 gap-y-3">
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
