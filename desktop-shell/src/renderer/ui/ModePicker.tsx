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

// A point on a circle round (c, c), deg clockwise from the top.
function polar(c: number, deg: number, r: number): { x: number; y: number } {
  const t = (deg * Math.PI) / 180;
  return { x: c + r * Math.sin(t), y: c - r * Math.cos(t) };
}

// One radial mark from r0 out to r1, at deg.
function Mark({ c, deg, r0, r1, className, style }: { c: number; deg: number; r0: number; r1: number; className: string; style?: CSSProperties }) {
  const a = polar(c, deg, r0);
  const b = polar(c, deg, r1);
  return <line x1={a.x} y1={a.y} x2={b.x} y2={b.y} className={className} style={style} />;
}

// The session's mode as a dial, with no words on it (design-glass.md,
// section 12). In the composer it is a small knob: a mark for each mode
// round its top, the chosen one lit in the mode's hue, and a notch turned
// to it; the arrow keys or the wheel turn it a step. A click opens the
// dial itself: a larger knob in a machined seat, a fine scale, and the
// modes' icons as stops to click; drag the knob and it snaps to the
// nearest. The names are only for assistive tech and the tooltip. Yolo
// asks first.
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
  const found = modes.findIndex((m) => m.mode === mode);
  const at = Math.max(0, found);
  const current = modes[found];
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
    if (e.key === "ArrowRight" || e.key === "ArrowUp") step(1);
    else if (e.key === "ArrowLeft" || e.key === "ArrowDown") step(-1);
    else return;
    e.preventDefault();
    e.stopPropagation();
  };

  const S = 32; // the composer knob's box
  return (
    <Popover
      open={open}
      onClose={() => setOpen(false)}
      side="top"
      label="Mode"
      className="min-w-0 p-2"
      trigger={
        <button
          type="button"
          className="mode-picker relative grid size-8 shrink-0 cursor-pointer place-items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring"
          aria-expanded={open}
          aria-label={`Mode: ${current?.label ?? "none"}. ${current?.blurb ?? ""} Arrow keys turn it.`}
          data-mode={mode ?? undefined}
          title={current === undefined ? "Mode" : `${current.label}: ${current.blurb}`}
          style={{ "--dial-hue": hue, "--dial-angle": `${angle}deg` } as CSSProperties}
          onClick={() => setOpen((o) => !o)}
          onKeyDown={keys}
          onWheel={(e) => step(e.deltaY < 0 ? 1 : -1)}
        >
          <svg aria-hidden className="dial-marks absolute inset-0" width={S} height={S} viewBox={`0 0 ${S} ${S}`}>
            {angles.map((d, i) => (
              <Mark key={d} c={S / 2} deg={d} r0={12.6} r1={14.6} className={cn("dial-mark", i === at && current !== undefined && "on")} />
            ))}
          </svg>
          <Knob size={20} />
          <span className="sr-only">{current?.label ?? "Mode"}</span>
        </button>
      }
    >
      <Dial modes={modes} at={at} angles={angles} onChoose={(m) => choose(m)} onKey={keys} />
    </Popover>
  );
}

// The knob: a knurled rim and a brushed face, lit from above, with a notch
// in the mode's hue. The rim and the notch turn to --dial-angle on a
// spring; the light on it stays put, as it would on a real one.
function Knob({ size, seat = false }: { size: number; seat?: boolean }) {
  return (
    <span className={cn("dial-knob", size >= 48 && "large")} style={{ "--k": `${size}px` } as CSSProperties} aria-hidden>
      {seat && <span className="dial-seat" />}
      <span className="dial-cap">
        <span className="dial-knurl" />
        <span className="dial-face" />
        <span className="dial-arm">
          <span className="dial-notch" />
        </span>
      </span>
    </span>
  );
}

const BOX = 208; // the dial's width
const HEIGHT = 172;
const CX = BOX / 2;
const CY = 110; // the knob's centre
const KNOB = 84;
const STOP_R = 80; // where the modes' stops sit, from the centre

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
  const nearAt = drag === null ? at : nearestMode(drag, modes.length);
  const near = modes[nearAt] ?? current;
  const hue = near === undefined ? "var(--text-faint)" : HUE[near.mode];
  const first = angles[0] ?? 0;
  const last = angles[angles.length - 1] ?? 0;

  const angleAt = (e: PointerEvent) => {
    const r = face.current?.getBoundingClientRect();
    if (r === undefined) return 0;
    const deg = (Math.atan2(e.clientY - (r.top + r.height / 2), e.clientX - (r.left + r.width / 2)) * 180) / Math.PI + 90;
    const norm = deg > 180 ? deg - 360 : deg;
    return Math.max(first - 12, Math.min(last + 12, norm));
  };

  // The scale: fine graduations a little past the first and last modes.
  const span = last - first + 16;
  const fine = Math.round(span / 4);

  return (
    <div className="mode-dial relative" role="group" aria-label="Mode" style={{ width: BOX, height: HEIGHT }} data-dragging={drag === null ? undefined : ""}>
      <svg aria-hidden className="absolute top-0 left-0 overflow-visible" width={BOX} height={HEIGHT} viewBox={`0 0 ${BOX} ${HEIGHT}`}>
        <g transform={`translate(0 ${CY - CX})`}>
          {Array.from({ length: fine + 1 }, (_, i) => {
            const d = first - 8 + (span * i) / fine;
            return <Mark key={i} c={CX} deg={d} r0={KNOB / 2 + 11} r1={KNOB / 2 + 14} className="dial-scale" />;
          })}
          {angles.map((d, i) => (
            <Mark
              key={d}
              c={CX}
              deg={d}
              r0={KNOB / 2 + 10}
              r1={KNOB / 2 + 17}
              className={cn("dial-major", i === nearAt && "on")}
              style={{ "--mark-hue": HUE[modes[i]?.mode ?? "ask"] } as CSSProperties}
            />
          ))}
        </g>
      </svg>
      {/* The knob in the middle: drag it round to turn. */}
      <div
        ref={face}
        className={cn("dial-turn absolute grid cursor-grab touch-none place-items-center rounded-full outline-none active:cursor-grabbing", drag !== null && "dragging")}
        style={{ left: CX - KNOB / 2, top: CY - KNOB / 2, width: KNOB, height: KNOB, "--dial-hue": hue, "--dial-angle": `${shown}deg` } as CSSProperties}
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
        <Knob size={KNOB} seat />
      </div>
      {/* The modes round it, each a stop to click, named only for assistive
          tech and the tooltip. */}
      {modes.map((m, i) => {
        const p = polar(CX, angles[i] ?? 0, STOP_R);
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
            className={cn("dial-stop mode absolute grid size-8 cursor-pointer place-items-center rounded-full outline-none focus-visible:ring-2 focus-visible:ring-ring", `mode-${m.mode}`, on && "on", i === nearAt && "near")}
            style={{ left: p.x - 16, top: p.y + (CY - CX) - 16, "--stop-hue": HUE[m.mode] } as CSSProperties}
            onClick={() => onChoose(m)}
          >
            <Icon className="size-4" weight={on ? "fill" : "regular"} />
          </button>
        );
      })}
    </div>
  );
}
