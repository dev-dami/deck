import { type ReactNode, useEffect, useState } from "react";
import { cn } from "../../lib/cn";

// Small motion (docs/design-glass.md, section 6): an item that arrives
// rises in once, the open thread's card glides to the next row, a fold
// opens and shuts its height, a count ticks when it changes. The CSS is
// "Small motion" in styles.css; reduced motion stops all of it.

const still = () => document.documentElement.getAttribute("data-motion") === "reduced";

// ── Arrivals ──
// The first time a key shows, it rises in; items that arrive in one go
// stagger, 35ms apart, five steps at most. A key seen before never rises
// again, so a re-render, a virtual list's scroll or a return to a thread
// does not replay it.

interface Seen {
  at: number;
  step: number;
}
const seen = new Map<string, Seen>();
// How long a key keeps its step: longer than the rise and its delay, so a
// re-render during the rise keeps it.
const HOLD = 700;
let batchAt = Number.NEGATIVE_INFINITY;
let batchN = 0;
let quietUntil = 0;

// The step to put in data-arrive for an item that shows now, or undefined
// for one that was there before.
export function arrival(key: string): number | undefined {
  const now = performance.now();
  const s = seen.get(key);
  if (s !== undefined) return s.step >= 0 && now - s.at < HOLD ? s.step : undefined;
  if (now < quietUntil) {
    seen.set(key, { at: 0, step: -1 });
    return undefined;
  }
  if (now - batchAt > 48) {
    batchAt = now;
    batchN = 0;
  }
  const step = Math.min(batchN++, 5);
  seen.set(key, { at: now, step });
  return step;
}

export const hasArrived = (key: string): boolean => seen.has(key);

// Marks keys as there all along: a thread's history, loaded in one go.
export function settle(keys: Iterable<string>): void {
  for (const k of keys) if (!seen.has(k)) seen.set(k, { at: 0, step: -1 });
}

// For the next ms, what shows first is taken as there all along.
export function quiet(ms: number): void {
  quietUntil = performance.now() + ms;
}

// A pane whose content changed under it (another thread) cross-fades in.
export function fadeIn(el: HTMLElement | null): void {
  if (el === null || still()) return;
  el.animate([{ opacity: 0 }, { opacity: 1 }], { duration: 180, easing: "cubic-bezier(0.32, 0.72, 0, 1)" });
}

// ── Leaving ──
// A row that goes (an archived thread) folds away first, then done runs.
export function leave(el: Element | null, done: () => void): void {
  if (el === null || still()) {
    done();
    return;
  }
  el.setAttribute("data-leaving", "");
  window.setTimeout(() => {
    done();
    // A row that stays (the call failed) comes back.
    window.setTimeout(() => el.removeAttribute("data-leaving"), 400);
  }, 180);
}

// ── The open card's glide ──
// The open thread's card, in the sidebar and the threads' bar, glides from
// the row that had it to the row that has it now, as the segmented pill
// slides. The row's <li> draws it as ::before for the move, from the old
// row's place and size to the new one's, on the pill's spring.

const SPRING =
  "linear(0, 0.023, 0.084, 0.168, 0.266, 0.369, 0.471, 0.568, 0.657, 0.736, 0.805, 0.863, 0.911, 0.949, 0.979, 1.001, 1.017, 1.028, 1.035, 1.038, 1.038, 1.037, 1.034, 1.031, 1.027, 1.023, 1.019, 1.016, 1.012, 1.009, 1.007, 1.005, 1.003, 1.002, 1.001, 1)";
// Past this the card does not fly: it shows on the new row.
const FAR = 360;

function startGlide(): () => void {
  let stop: (() => void) | null = null;
  const glide = (from: HTMLElement, to: HTMLElement) => {
    const li = to.parentElement;
    if (li === null) return;
    stop?.();
    // Both rows drop their colour transitions first, so the card reads at
    // its end and the old row lets it go at once.
    from.setAttribute("data-glide-from", "");
    to.setAttribute("data-glide-to", "");
    const end = () => {
      li.removeAttribute("data-glide");
      from.removeAttribute("data-glide-from");
      to.removeAttribute("data-glide-to");
      if (stop === cancel) stop = null;
    };
    let anim: Animation | null = null;
    const cancel = () => {
      anim?.cancel();
      end();
    };
    const a = from.getBoundingClientRect();
    const b = to.getBoundingClientRect();
    const dx = a.left - b.left;
    const dy = a.top - b.top;
    if (b.width === 0 || b.height === 0 || a.width === 0 || Math.hypot(dx, dy) > FAR || (dx === 0 && dy === 0)) {
      end();
      return;
    }
    const card = getComputedStyle(to);
    const set = (k: string, v: string) => li.style.setProperty(k, v);
    set("--gl-l", `${to.offsetLeft}px`);
    set("--gl-t", `${to.offsetTop}px`);
    set("--gl-w", `${b.width}px`);
    set("--gl-h", `${b.height}px`);
    set("--gl-r", card.borderTopLeftRadius);
    set("--gl-bg", card.backgroundColor);
    set("--gl-shadow", card.boxShadow);
    li.setAttribute("data-glide", "");
    anim = li.animate(
      [{ transform: `translate(${dx}px, ${dy}px) scale(${a.width / b.width}, ${a.height / b.height})` }, { transform: "none" }],
      { duration: 340, easing: SPRING, pseudoElement: "::before" },
    );
    anim.onfinish = end;
    stop = cancel;
  };
  const mo = new MutationObserver((records) => {
    if (still()) return;
    let from: HTMLElement | null = null;
    let to: HTMLElement | null = null;
    for (const r of records) {
      const el = r.target;
      if (!(el instanceof HTMLElement) || !el.matches(".session > [role='button']")) continue;
      if (el.getAttribute("aria-current") === "page") to = el;
      else if (r.oldValue === "page") from = el;
    }
    if (from === null || to === null || !from.isConnected) return;
    const nav = to.closest("nav");
    if (nav === null || from.closest("nav") !== nav) return;
    glide(from, to);
  });
  mo.observe(document.body, { subtree: true, attributes: true, attributeFilter: ["aria-current"], attributeOldValue: true });
  return () => {
    mo.disconnect();
    stop?.();
  };
}

export function useGlide(): void {
  useEffect(() => startGlide(), []);
}

// ── Folds ──
// What a disclosure shows: it opens from no height to its own, fading in,
// and shuts the same way before it goes (grid-template-rows 0fr to 1fr).
// The first render shows it as it is, with no move.
export function Fold({ open, className, children }: { open: boolean; className?: string; children: ReactNode }) {
  const [shown, setShown] = useState(open);
  const [was, setWas] = useState(open);
  const [phase, setPhase] = useState<"still" | "opening" | "closing">("still");
  if (was !== open) {
    setWas(open);
    if (still()) {
      setShown(open);
      setPhase("still");
    } else {
      setShown(true);
      setPhase(open ? "opening" : "closing");
    }
  }
  const finish = () => {
    if (phase === "closing") setShown(false);
    setPhase("still");
  };
  // The fold's own animation ends the move. The timer is a backstop, for a
  // fold that never draws (a hidden pane).
  useEffect(() => {
    if (phase === "still") return;
    const t = window.setTimeout(() => {
      if (phase === "closing") setShown(false);
      setPhase("still");
    }, 1000);
    return () => window.clearTimeout(t);
  }, [phase]);
  if (!shown) return null;
  return (
    <div
      className="fold-body"
      data-fold={phase === "still" ? undefined : phase}
      onAnimationEnd={(e) => {
        if (e.target === e.currentTarget) finish();
      }}
    >
      <div className="fold-clip">
        <div className={className}>{children}</div>
      </div>
    </div>
  );
}

// ── Counts ──
// A number that ticks up into place when it changes (not when it first
// shows).
export function Tick({ value, className }: { value: string | number; className?: string }) {
  const [first] = useState(value);
  const [moved, setMoved] = useState(false);
  if (!moved && value !== first) setMoved(true);
  return (
    <span key={String(value)} className={cn("inline-block", moved && "tick", className)}>
      {value}
    </span>
  );
}
