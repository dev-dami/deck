import { availableModes, type ModeInfo, needsConfirm } from "@web/modes";
import type { Mode } from "@web/protocol";
import { type CSSProperties, type KeyboardEvent, type PointerEvent, useRef, useState } from "react";
import { cn } from "../lib/cn";
import { Flame, ListChecks, MessageCircleQuestion } from "./icons";
import { Popover } from "./kit/menu";

const ICONS = { plan: ListChecks, ask: MessageCircleQuestion, yolo: Flame } as const;

// Each mode's hue: Plan the accent, Ask the safe green, Yolo the warning red.
const HUE: Record<Mode, string> = {
  plan: "var(--primary)",
  ask: "var(--success)",
  yolo: "var(--destructive)",
};

// The dial's sweep: the modes sit evenly on an arc this many degrees each
// side of the top.
const SWEEP = 70;

// Where each mode sits on the dial, in degrees from the top.
export function modeAngles(n: number): number[] {
  if (n <= 1) return [0];
  return Array.from({ length: n }, (_, i) => -SWEEP + (2 * SWEEP * i) / (n - 1));
}

// The mode nearest an angle on the dial.
export function nearestMode(angle: number, n: number): number {
  const a = modeAngles(n);
  let best = 0;
  for (let i = 1; i < a.length; i++) if (Math.abs((a[i] as number) - angle) < Math.abs((a[best] as number) - angle)) best = i;
  return best;
}

// The session's mode as a dial, not a word (design-glass.md, section 12).
// In the composer it is a small knob: its pointer and colour say the mode,
// and the arrow keys or the wheel turn it a step. A click opens the dial
// itself: Plan, Ask and Yolo set round it, a lit arc from the first to the
// one chosen, and a knob to turn by dragging, by a click on a mode, or by
// the keys. Yolo asks first.
export function ModePicker({
  mode,
  allowYolo,
  confirmYolo,
  onSet,
}: {
  mode: Mode | null;
  allowYolo: boolean;
  confirmYolo: boolean;
  onSet: (m: Mode) => void;
}) {
  const [open, setOpen] = useState(false);
  const modes = availableModes(allowYolo);
  const angles = modeAngles(modes.length);
  const at = Math.max(0, modes.findIndex((m) => m.mode === mode));
  const current = modes.find((m) => m.mode === mode);
  const angle = current === undefined ? 0 : (angles[at] ?? 0);
  const hue = current === undefined ? "var(--text-faint)" : HUE[current.mode];

  const choose = (m: ModeInfo) => {
    if (mode === m.mode) return;
    if (confirmYolo && needsConfirm(m.mode) && !window.confirm(`${m.blurb} Switch to ${m.label}?`)) return;
    onSet(m.mode);
  };
  const step = (by: number) => {
    const next = modes[Math.min(modes.length - 1, Math.max(0, at + by))];
    if (next !== undefined) choose(next);
  };
  const keys = (e: KeyboardEvent) => {
    if (e.key === "ArrowRight" || e.key === "ArrowUp") {
      e.preventDefault();
      step(1);
    } else if (e.key === "ArrowLeft" || e.key === "ArrowDown") {
      e.preventDefault();
      step(-1);
    }
  };

  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      label="Mode"
      className="w-60"
      trigger={
        <button
          type="button"
          className="mode-picker group grid size-8 shrink-0 cursor-pointer place-items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={open}
          aria-label={`Mode: ${current?.label ?? "none"}. ${current?.blurb ?? ""} Arrow keys turn it.`}
          data-mode={mode ?? undefined}
          title={current === undefined ? "Mode" : `${current.label}: ${current.blurb}`}
          style={{ "--dial-hue": hue, "--dial-angle": `${angle}deg` } as CSSProperties}
          onClick={() => setOpen((o) => !o)}
          onKeyDown={keys}
          onWheel={(e) => step(e.deltaY < 0 ? 1 : -1)}
        >
          <Knob size={24} />
          <span className="sr-only">{current?.label ?? "Mode"}</span>
        </button>
      }
    >
      <Dial modes={modes} at={at} angles={angles} onChoose={(m) => choose(m)} onKey={keys} />
    </Popover>
  );
}

// The knob: a domed cap with a pointer in the mode's hue, turned to the
// mode (--dial-angle) with a spring, ringed by a fine bezel.
function Knob({ size }: { size: number }) {
  return (
    <span className="dial-knob relative block shrink-0 rounded-full" style={{ width: size, height: size }} aria-hidden>
      <span className="dial-bezel absolute inset-0 rounded-full" />
      <span className="dial-cap absolute inset-[14%] rounded-full" />
      <span className="dial-pointer absolute inset-0">
        <span className="dial-tick absolute top-[18%] left-1/2 h-[30%] w-[2px] -translate-x-1/2 rounded-full" />
      </span>
    </span>
  );
}

const R = 64; // where the modes sit, px from the centre
const BOX = 176;

function point(deg: number, r: number): { x: number; y: number } {
  const t = ((deg - 90) * Math.PI) / 180;
  return { x: BOX / 2 + r * Math.cos(t), y: BOX / 2 + r * Math.sin(t) };
}

function arc(from: number, to: number, r: number): string {
  if (Math.abs(to - from) < 0.5) return "";
  const a = point(from, r);
  const b = point(to, r);
  return `M ${a.x} ${a.y} A ${r} ${r} 0 ${Math.abs(to - from) > 180 ? 1 : 0} 1 ${b.x} ${b.y}`;
}

function Dial({
  modes,
  at,
  angles,
  onChoose,
  onKey,
}: {
  modes: ModeInfo[];
  at: number;
  angles: number[];
  onChoose: (m: ModeInfo) => void;
  onKey: (e: KeyboardEvent) => void;
}) {
  const face = useRef<HTMLDivElement>(null);
  // While the knob is dragged, the angle under the pointer; it snaps to a
  // mode on release.
  const [drag, setDrag] = useState<number | null>(null);
  const current = modes[at];
  const shown = drag === null ? (angles[at] ?? 0) : drag;
  const near = modes[drag === null ? at : nearestMode(drag, modes.length)] ?? current;
  const hue = near === undefined ? "var(--text-faint)" : HUE[near.mode];
  const first = angles[0] ?? 0;
  const last = angles[angles.length - 1] ?? 0;
  const lit = last === first ? 0 : Math.max(0.01, Math.min(100, ((shown - first) / (last - first)) * 100));

  const angleAt = (e: PointerEvent) => {
    const r = face.current?.getBoundingClientRect();
    if (r === undefined) return 0;
    const deg = (Math.atan2(e.clientY - (r.top + r.height / 2), e.clientX - (r.left + r.width / 2)) * 180) / Math.PI + 90;
    const norm = deg > 180 ? deg - 360 : deg;
    return Math.max(first - 12, Math.min(last + 12, norm));
  };

  return (
    <div className="mode-dial flex flex-col items-center gap-1 px-1 pt-1 pb-2" role="group" aria-label="Mode">
      <div className="relative" style={{ width: BOX, height: BOX * 0.78 }} data-dragging={drag === null ? undefined : ""}>
        <svg aria-hidden className="absolute inset-0 overflow-visible" width={BOX} height={BOX} viewBox={`0 0 ${BOX} ${BOX}`}>
          {/* The scale: fine ticks along the sweep, the track, and the lit arc. */}
          {Array.from({ length: 29 }, (_, i) => {
            const d = first - 6 + ((last - first + 12) * i) / 28;
            const a = point(d, R - 22);
            const b = point(d, R - (i % 7 === 0 ? 28 : 25));
            return <line key={i} x1={a.x} y1={a.y} x2={b.x} y2={b.y} className="dial-scale" />;
          })}
          <path d={arc(first, last, R - 15)} className="dial-track" />
          {/* The lit arc is the whole track drawn to the chosen mode, so it
              springs along with the pointer. */}
          <path d={arc(first, last, R - 15)} pathLength={100} className="dial-lit" style={{ stroke: hue, strokeDasharray: `${lit} 100` }} />
        </svg>
        {/* The knob in the middle: drag it round to turn. */}
        <div
          ref={face}
          className={cn("dial-face absolute left-1/2 grid -translate-x-1/2 cursor-grab touch-none place-items-center active:cursor-grabbing", drag !== null && "dragging")}
          style={{ top: BOX / 2 - 30, width: 60, height: 60, "--dial-hue": hue, "--dial-angle": `${shown}deg` } as CSSProperties}
          tabIndex={0}
          role="slider"
          aria-label="Mode"
          aria-valuemin={0}
          aria-valuemax={modes.length - 1}
          aria-valuenow={at}
          aria-valuetext={current?.label ?? ""}
          onKeyDown={onKey}
          onPointerDown={(e) => {
            e.currentTarget.setPointerCapture(e.pointerId);
            setDrag(angleAt(e));
          }}
          onPointerMove={(e) => drag !== null && setDrag(angleAt(e))}
          onPointerUp={(e) => {
            if (drag === null) return;
            const m = modes[nearestMode(angleAt(e), modes.length)];
            setDrag(null);
            if (m !== undefined) onChoose(m);
          }}
          onPointerCancel={() => setDrag(null)}
        >
          <Knob size={60} />
        </div>
        {/* The modes round it, each a stop to click. */}
        {modes.map((m, i) => {
          const p = point(angles[i] ?? 0, R + 14);
          const Icon = ICONS[m.mode];
          const on = i === at;
          return (
            <button
              key={m.mode}
              type="button"
              role="menuitemradio"
              aria-checked={on}
              aria-label={`${m.label}: ${m.blurb}`}
              title={`${m.label}: ${m.blurb}`}
              className={cn("dial-stop mode absolute grid size-8 -translate-x-1/2 -translate-y-1/2 cursor-pointer place-items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring", `mode-${m.mode}`, on && "on")}
              style={{ left: p.x, top: p.y, "--stop-hue": HUE[m.mode] } as CSSProperties}
              onClick={() => onChoose(m)}
            >
              <Icon className="size-4" weight={on ? "fill" : "regular"} />
            </button>
          );
        })}
      </div>
      {/* What the chosen mode does, quietly, under the dial. */}
      <p className="dial-readout min-h-8 max-w-52 text-balance text-center text-2xs text-muted-foreground leading-snug" aria-live="polite">
        {near?.blurb}
      </p>
    </div>
  );
}
