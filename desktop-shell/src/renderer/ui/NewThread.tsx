import type { ReactNode } from "react";
import { startCards, withStart } from "./landing";

// The new-thread screen: the question, three ways to start, and the
// composer (children). A start card puts its prefix in the draft.
export function NewThread({
  loaded,
  project,
  kinds,
  draft,
  setDraft,
  onFocus,
  children,
}: {
  // The thread list arrived; before that, the screen says it connects.
  loaded: boolean;
  project: string;
  kinds: string[];
  draft: string;
  setDraft: (s: string) => void;
  onFocus: () => void;
  children: ReactNode;
}) {
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
      {children}
    </div>
  );
}
