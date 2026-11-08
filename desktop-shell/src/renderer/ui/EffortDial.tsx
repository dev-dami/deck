import { type CSSProperties, type KeyboardEvent, type PointerEvent, type RefObject, useEffect, useRef, useState } from "react";
import { arcPath, type Dial as DialState, dialSegments, effortDial, effortName, effortStep } from "../effort";
import { cn } from "../lib/cn";
import { Check } from "./icons";
import { Popover } from "./kit/menu";

// The ring's geometry, in a 20-unit box: segments on a 8.25 radius, a cap
// of 5.25 inside them, the pointer from near the centre to the cap's rim.
const BOX = 20;
const C = BOX / 2;
const RING = 8.25;
const CAP = 5.25;

// The effort dial (effort.ts; design-glass.md, section 12): a ring of
// segments, one per level, lit up to the level in the agent's hue (--hue,
// else the accent), round a flat cap whose pointer turns to it. A new
// level spins the cap on the spring and lights or darkens the segments one
// after another, in the direction it turned, so the light follows the
// pointer. Reduced motion holds it still.
export function Dial({ dial, size = 20, className }: { dial: DialState; size?: number; className?: string }) {
  const prev = useRef(dial.lit);
  const from = prev.current;
  useEffect(() => {
    prev.current = dial.lit;
  }, [dial.lit]);
  const up = dial.lit >= from;
  return (
    <svg
      aria-hidden
      width={size}
      height={size}
      viewBox={`0 0 ${BOX} ${BOX}`}
      className={cn("effort-dial shrink-0", className)}
      data-auto={dial.auto ? "" : undefined}
      data-lit={dial.lit}
      data-count={dial.count}
      style={{ width: size, height: size }}
    >
      {dialSegments(dial.count).map(([a, b], i) => (
        <path
          key={`${dial.count}-${i}`}
          className={cn("dial-seg", i < dial.lit && "on")}
          d={arcPath(C, RING, a, b)}
          style={{ "--d": up ? Math.max(0, i - from) : Math.max(0, from - 1 - i) } as CSSProperties}
        />
      ))}
      <g className="dial-cap" style={{ rotate: `${dial.angle}deg` }}>
        <circle className="dial-face" cx={C} cy={C} r={CAP} />
        <line className="dial-pointer" x1={C} y1={C - 1.6} x2={C} y2={C - CAP + 1.4} />
      </g>
    </svg>
  );
}

// How far the pointer travels for one level when the dial is dragged, and
// how much wheel makes one level (a mouse's notch is about 100).
const DRAG_STEP = 14;
const WHEEL_STEP = 40;
// A turn by wheel or keys applies once the dial has rested this long.
const SETTLE = 450;

// Turning a dial: dragging it (right or up turns it up), the wheel over it,
// and the arrows while it has the focus (Home rests it at the default, End
// turns it all the way). The level shows at once; it applies when the drag
// ends, or once the wheel and the keys have rested. turned says whether the
// pointer moved enough to be a turn, so the click that ends a drag opens
// nothing.
export function useTurn(target: RefObject<HTMLElement | null>, levels: readonly string[], effort: string, onPick: (effort: string) => void, enabled = true) {
  const [turn, setTurn] = useState<string | null>(null);
  const shown = turn ?? effort;
  const live = useRef({ levels, effort, shown, onPick });
  live.current = { levels, effort, shown, onPick };
  const timer = useRef<number | undefined>(undefined);
  const drag = useRef<{ x: number; y: number; from: string; moved: boolean } | null>(null);
  const turned = useRef(false);

  // Once the agent runs at the level turned to, the dial follows it again;
  // if the Deck never says so, it falls back after a while.
  useEffect(() => {
    if (turn !== null && turn === effort) setTurn(null);
  }, [turn, effort]);
  useEffect(() => () => window.clearTimeout(timer.current), []);

  const commit = (wait: number) => {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => {
      const { shown: next, effort: now, onPick: pick } = live.current;
      if (next !== now) pick(next);
      timer.current = window.setTimeout(() => setTurn(null), 4000);
    }, wait);
  };
  const show = (next: string) => {
    if (next === live.current.shown) return false;
    live.current.shown = next;
    setTurn(next);
    return true;
  };
  const step = (n: number) => {
    if (show(effortStep(live.current.levels, live.current.shown, n))) commit(SETTLE);
  };

  useEffect(() => {
    const el = target.current;
    if (el === null || !enabled) return;
    let acc = 0;
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      acc += Math.abs(e.deltaY) >= Math.abs(e.deltaX) ? e.deltaY : -e.deltaX;
      if (Math.abs(acc) < WHEEL_STEP) return;
      step(acc < 0 ? 1 : -1);
      acc = 0;
    };
    el.addEventListener("wheel", wheel, { passive: false });
    return () => el.removeEventListener("wheel", wheel);
    // step reads the live values; the listener is bound once per element.
  }, [target, enabled]);

  return {
    shown,
    handlers: {
      onPointerDown: (e: PointerEvent<HTMLElement>) => {
        if (e.button !== 0 || !enabled) return;
        drag.current = { x: e.clientX, y: e.clientY, from: live.current.shown, moved: false };
      },
      onPointerMove: (e: PointerEvent<HTMLElement>) => {
        const d = drag.current;
        if (d === null) return;
        const travel = e.clientX - d.x - (e.clientY - d.y);
        if (!d.moved && Math.abs(travel) < 4) return;
        if (!d.moved) {
          d.moved = true;
          e.currentTarget.setPointerCapture(e.pointerId);
        }
        show(effortStep(live.current.levels, d.from, Math.trunc(travel / DRAG_STEP)));
      },
      onPointerUp: (e: PointerEvent<HTMLElement>) => {
        const d = drag.current;
        drag.current = null;
        if (d?.moved !== true) return;
        turned.current = true;
        if (e.currentTarget.hasPointerCapture(e.pointerId)) e.currentTarget.releasePointerCapture(e.pointerId);
        commit(0);
      },
      onPointerCancel: () => {
        drag.current = null;
      },
      onKeyDown: (e: KeyboardEvent<HTMLElement>) => {
        if (!enabled || e.altKey || e.ctrlKey || e.metaKey) return;
        const { levels: ls, shown: at } = live.current;
        const moves: Record<string, number> = { ArrowUp: 1, ArrowRight: 1, ArrowDown: -1, ArrowLeft: -1, Home: -ls.length - 1, End: ls.length + 1 };
        const n = moves[e.key];
        if (n === undefined) return;
        e.preventDefault();
        if (effortStep(ls, at, n) !== at) step(n);
      },
    },
    // Whether the click now ending is the end of a drag (and so opens nothing).
    endsTurn: () => {
      const t = turned.current;
      turned.current = false;
      return t;
    },
  };
}

// The lead's reasoning effort in the composer's bar: the dial, attached to
// the end of the agent's chip, with no word. Drag it, scroll over it or
// use the arrows to turn it; a click opens the levels as a short column,
// each with its dial and name, applied at once. The level's name is for
// the tooltip and assistive tech.
export function EffortDial({
  levels,
  effort,
  model,
  onPick,
}: {
  // The levels the model takes, lightest first (effort.ts effortLevels).
  levels: readonly string[];
  // The level it runs at; "" for the agent's own.
  effort: string;
  // The model's name, for the column's header.
  model: string;
  onPick: (effort: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLButtonElement>(null);
  const t = useTurn(ref, levels, effort, onPick, !open);
  const name = effortName(t.shown);
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      align="end"
      label="Reasoning effort"
      className="effort-menu w-52 p-1"
      trigger={
        <button
          ref={ref}
          type="button"
          className="effort-picker grid h-[26px] w-[30px] shrink-0 cursor-pointer touch-none select-none place-items-center rounded-full outline-none transition-colors hover:bg-hover focus-visible:ring-2 focus-visible:ring-ring aria-expanded:bg-selected"
          aria-haspopup="menu"
          aria-expanded={open}
          aria-label={`Reasoning effort: ${t.shown === "" ? "the agent's default" : name}. Turn the dial or open the levels`}
          title={`Reasoning effort: ${name}`}
          data-effort={t.shown}
          {...t.handlers}
          onClick={() => {
            if (t.endsTurn()) return;
            setOpen((o) => !o);
          }}
        >
          <Dial dial={effortDial(levels, t.shown)} />
          <span className="sr-only">{name}</span>
        </button>
      }
    >
      <div className="flex items-baseline gap-2 border-hairline border-b px-2 pt-1 pb-1.5">
        <span className="text-muted-foreground text-xs">Effort</span>
        {model !== "" && <span className="ml-auto min-w-0 truncate text-2xs text-faint">{model}</span>}
      </div>
      <div className="effort-levels flex flex-col gap-px pt-1" role="group" aria-label="Reasoning effort">
        {["", ...levels].map((l) => {
          const on = l === effort;
          return (
            <button
              key={l}
              type="button"
              role="menuitemradio"
              aria-checked={on}
              className={cn(
                "effort-level flex h-7 w-full cursor-pointer items-center gap-2.5 rounded-[7px] px-2 text-left text-[13px] outline-none transition-colors hover:bg-hover focus-visible:bg-selected",
                on && "bg-selected hover:bg-selected",
              )}
              data-level={l}
              onClick={() => {
                setOpen(false);
                if (!on) onPick(l);
              }}
            >
              <Dial dial={effortDial(levels, l)} size={16} />
              <span className="min-w-0 flex-1 truncate">{effortName(l)}</span>
              {on && <Check className="size-3.5 shrink-0 text-foreground" weight="bold" />}
            </button>
          );
        })}
      </div>
    </Popover>
  );
}
