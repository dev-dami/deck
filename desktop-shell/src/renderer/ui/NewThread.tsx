import type { ReactNode } from "react";
import { setPrefs, usePrefs } from "../prefs";
import { Switch } from "./kit/switch";
import { startCards, withStart } from "./landing";

// The new-thread screen: the question, three ways to start, whether the
// thread gets a branch of its own, and the composer (children). A start
// card puts its prefix in the draft.
export function NewThread({
  loaded,
  project,
  kinds,
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
  draft: string;
  setDraft: (s: string) => void;
  onFocus: () => void;
  // The Deck can put a thread on a worktree and branch of its own.
  canIsolate: boolean;
  children: ReactNode;
}) {
  const { isolate } = usePrefs();
  return (
    <div className="welcome flex min-h-0 flex-1 flex-col items-center justify-center gap-6 overflow-y-auto px-5 pb-[14vh]">
      <h2 className="welcome-title text-balance text-center font-medium text-[20px] tracking-tight">
        {loaded ? (
          <>
            What should we build{project === "" ? "" : " in "}
            {project !== "" && <span className="font-mono">{project}</span>}?
          </>
        ) : (
          "Connecting to Deck…"
        )}
      </h2>
      <ul className="start-cards grid w-full max-w-3xl grid-cols-3 gap-2" aria-label="Ways to start">
        {startCards(kinds).map((s) => (
          <li key={s.id} className="flex">
            <button
              type="button"
              className="flex w-full animate-enter cursor-pointer flex-col gap-1 rounded-xl border border-hairline bg-surface-1 px-3.5 py-3 text-left hover:bg-hover"
              onClick={() => {
                setDraft(withStart(s.prefix, draft));
                onFocus();
              }}
            >
              <span className="font-medium text-sm">{s.title}</span>
              <span className="text-muted-foreground text-xs">{s.line}</span>
              <span className="font-mono text-2xs text-faint">{s.prefix.trim()}</span>
            </button>
          </li>
        ))}
      </ul>
      {canIsolate && (
        <div className="own-branch flex w-full max-w-3xl items-center gap-2 px-1 text-muted-foreground text-xs">
          <Switch label="On its own branch" checked={isolate} onChange={(v) => setPrefs({ isolate: v })} />
          <span>On its own branch</span>
          <span className="font-mono text-2xs text-faint">deck/&lt;id&gt;</span>
        </div>
      )}
      {children}
    </div>
  );
}
