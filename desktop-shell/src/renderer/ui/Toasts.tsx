import type { AppState } from "@web/state";
import { X } from "lucide-react";
import { useCallback, useEffect, useRef } from "react";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";

// How long a toast stays.
const TOAST_MS = 6000;

// The project's toasts, bottom right. Each has a close button and goes by
// itself after TOAST_MS.
export function Toasts({ c, toasts }: { c: DeckClient; toasts: AppState["toasts"] }) {
  // Each toast gets one timer when it appears; a change to the list does not
  // start the others again.
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());
  const dismiss = useCallback((id: number) => c.store.dispatch({ type: "toast.dismiss", id }), [c]);
  useEffect(() => {
    const all = timers.current;
    const ids = new Set(toasts.map((t) => t.id));
    for (const [id, t] of all) {
      if (!ids.has(id)) {
        clearTimeout(t);
        all.delete(id);
      }
    }
    for (const id of ids) {
      if (!all.has(id)) all.set(id, setTimeout(() => dismiss(id), TOAST_MS));
    }
  }, [toasts, dismiss]);
  useEffect(() => {
    const all = timers.current;
    return () => {
      for (const t of all.values()) clearTimeout(t);
      all.clear();
    };
  }, []);
  return (
    <div className="toasts pointer-events-none fixed right-4 bottom-4 z-50 flex w-80 flex-col gap-2" aria-live="polite">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={cn(
            `toast toast-${t.kind} pointer-events-auto flex animate-enter items-start gap-2 rounded-xl border border-hairline-strong bg-surface-2 py-3 pr-2 pl-4 text-sm shadow-popover inset-shadow-hl`,
            t.kind === "error" && "border-destructive/40",
          )}
        >
          <span className="toast-text min-w-0 flex-1 break-words">{t.text}</span>
          <button
            type="button"
            aria-label="Dismiss"
            title="Dismiss"
            className="-my-0.5 grid size-5 shrink-0 cursor-pointer place-items-center rounded text-muted-foreground outline-none hover:bg-hover hover:text-foreground focus-visible:ring-2 focus-visible:ring-ring"
            onClick={() => dismiss(t.id)}
          >
            <X className="size-3.5" />
          </button>
        </div>
      ))}
    </div>
  );
}
