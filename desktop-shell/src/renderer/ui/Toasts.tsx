import type { Store } from "@web/controller";
import type { AppState } from "@web/state";
import { X } from "lucide-react";
import { useCallback, useEffect, useRef } from "react";
import type { DeckClient } from "../client";
import { cn } from "../lib/cn";

// How long a toast stays.
const TOAST_MS = 6000;

// A toast's button (Undo) and how long it stays. The shared reducer keeps
// text alone, so the button is kept here by store and toast id.
export interface ToastAction {
  label: string;
  run: () => void;
  ms: number;
}

const actions = new WeakMap<Store, Map<number, ToastAction>>();

function actionsOf(store: Store): Map<number, ToastAction> {
  let m = actions.get(store);
  if (m === undefined) {
    m = new Map();
    actions.set(store, m);
  }
  return m;
}

// An info toast with a button. The button closes the toast too.
export function actionToast(store: Store, text: string, action: ToastAction): void {
  actionsOf(store).set(store.state.nextToastId, action);
  store.dispatch({ type: "toast.add", kind: "info", text });
}

// The project's toasts, bottom right. Each has a close button and goes by
// itself after TOAST_MS.
export function Toasts({ c, toasts }: { c: DeckClient; toasts: AppState["toasts"] }) {
  // Each toast gets one timer when it appears; a change to the list does not
  // start the others again.
  const timers = useRef(new Map<number, ReturnType<typeof setTimeout>>());
  const dismiss = useCallback((id: number) => c.store.dispatch({ type: "toast.dismiss", id }), [c]);
  const acts = actionsOf(c.store);
  useEffect(() => {
    const all = timers.current;
    const ids = new Set(toasts.map((t) => t.id));
    for (const [id, t] of all) {
      if (!ids.has(id)) {
        clearTimeout(t);
        all.delete(id);
        acts.delete(id);
      }
    }
    for (const id of ids) {
      if (!all.has(id)) all.set(id, setTimeout(() => dismiss(id), acts.get(id)?.ms ?? TOAST_MS));
    }
  }, [toasts, dismiss, acts]);
  useEffect(() => {
    const all = timers.current;
    return () => {
      for (const t of all.values()) clearTimeout(t);
      all.clear();
    };
  }, []);
  return (
    // Above the composer, so a toast never covers Send.
    <div className="toasts pointer-events-none fixed right-4 bottom-40 z-50 flex w-80 flex-col gap-2" aria-live="polite">
      {toasts.map((t) => (
        <div
          key={t.id}
          className={cn(
            `toast toast-${t.kind} material pointer-events-auto flex animate-enter items-start gap-2 rounded-xl border border-hairline-strong bg-surface-2 py-3 pr-2 pl-4 text-sm shadow-popover inset-shadow-hl`,
            t.kind === "error" && "border-destructive/40",
          )}
        >
          <span className="toast-text min-w-0 flex-1 break-words">{t.text}</span>
          {acts.has(t.id) && (
            <button
              type="button"
              className="toast-action -my-0.5 shrink-0 cursor-pointer rounded px-1.5 font-medium text-foreground text-xs outline-none hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring"
              onClick={() => {
                acts.get(t.id)?.run();
                dismiss(t.id);
              }}
            >
              {acts.get(t.id)?.label}
            </button>
          )}
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
