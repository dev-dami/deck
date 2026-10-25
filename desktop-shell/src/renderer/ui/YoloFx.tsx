import type { Mode } from "@web/protocol";
import { useEffect, useLayoutEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { getPrefs } from "../prefs";
import {
  bandColour,
  CELL_PX,
  clamp01,
  done,
  easeOut,
  envelope,
  frontRadius,
  HUE_SPREAD,
  hueAt,
  intensity,
  LETTER_H,
  POP_FOR,
  playsOn,
  regionShift,
  TIER_NONE,
  tier,
  tierEnds,
  WORD,
  WORD_COLS,
  WORD_ON,
  wordColour,
  wordShowing,
} from "../yoloFx";

// Counts the times the open room switched to yolo: from the mode picker,
// Ctrl+Shift+Y, /yolo, a plan's Approve or another device. They all land
// as the room's mode in the store, so this watches that alone.
export function useYoloTrigger(projectKey: string, sessionId: string | null, mode: Mode | null): number {
  const last = useRef<{ room: string; mode: Mode | null } | null>(null);
  const [plays, setPlays] = useState(0);
  useEffect(() => {
    const room = sessionId === null ? "" : `${projectKey}\n${sessionId}`;
    const prev = last.current;
    // A room that has not said its mode yet keeps the last one it said.
    const next = { room, mode: mode ?? (prev?.room === room ? prev.mode : null) };
    last.current = next;
    if (playsOn(prev, next)) setPlays((n) => n + 1);
  }, [projectKey, sessionId, mode]);
  return plays;
}

// A region of the window the wave moves, and the styles to give back.
interface Region {
  x: number;
  y: number;
  transform: string;
  filter: string;
  willChange: string;
}

const reducedMotion = () => document.documentElement.getAttribute("data-motion") === "reduced";

// In place of the shockwave, the mode chip flashes amber once.
function flashChip(): void {
  for (const el of document.querySelectorAll<HTMLElement>(".mode-picker")) {
    el.removeAttribute("data-yolo-flash");
    // A reflow, so the animation starts again.
    void el.offsetWidth;
    el.setAttribute("data-yolo-flash", "");
    setTimeout(() => el.removeAttribute("data-yolo-flash"), 800);
  }
}

// The band of colour behind the front: one radial gradient over the ring
// it covers, its tiers as hard steps and its hue sampled every 30 degrees.
function drawBand(ctx: CanvasRenderingContext2D, t: number, w: number, h: number, dark: boolean): void {
  const cx = w / 2;
  const cy = h / 2;
  const reach = Math.hypot(cx, cy);
  const env = envelope(t);
  const ends = tierEnds(env);
  const rOut = frontRadius(t, reach);
  const lastEnd = ends.at(-1)?.[1];
  if (lastEnd === undefined || rOut < 1) return;
  const rIn = frontRadius(t - lastEnd * 1000, reach);
  if (rOut - rIn < 0.5) return;
  const g = ctx.createRadialGradient(cx, cy, rIn, cx, cy, rOut);
  const tSec = t / 1000;
  // Inner tiers first, so at a shared offset the outer tier's stop comes
  // after the inner one's and the step is sharp.
  for (let i = ends.length - 1; i >= 0; i--) {
    const [tr, end] = ends[i]!;
    const start = i === 0 ? 0 : ends[i - 1]![1];
    const outer = frontRadius(t - start * 1000, reach);
    const inner = frontRadius(t - end * 1000, reach);
    if (outer - inner < 0.01) continue;
    const n = Math.max(2, Math.ceil((((outer - inner) / CELL_PX) * HUE_SPREAD) / 30) + 1);
    for (let k = 0; k < n; k++) {
      const r = inner + ((outer - inner) * k) / (n - 1);
      g.addColorStop(clamp01((r - rIn) / (rOut - rIn)), bandColour(tr, hueAt(r / CELL_PX, tSec), dark));
    }
  }
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.arc(cx, cy, rOut, 0, 2 * Math.PI);
  ctx.arc(cx, cy, rIn, 0, 2 * Math.PI, true);
  ctx.fill();
}

interface WordShape {
  w: number;
  h: number;
  front: Path2D;
  back: Path2D;
}

// YOLO in the block font, about 30% of the window wide, in square pixels
// drawn as rounded squares, with a halo behind so it reads over anything.
function wordShape(w: number, h: number): WordShape {
  const p = Math.max(4, Math.min((0.3 * w) / WORD_COLS, (0.4 * h) / LETTER_H));
  const x0 = (w - WORD_COLS * p) / 2;
  const y0 = (h - LETTER_H * p) / 2;
  const front = new Path2D();
  const back = new Path2D();
  const inset = p * 0.07;
  const halo = p * 0.16;
  const radius = p * 0.22;
  WORD.forEach((row, y) =>
    row.forEach((on, x) => {
      if (!on) return;
      const px = x0 + x * p;
      const py = y0 + y * p;
      front.roundRect(px + inset, py + inset, p - 2 * inset, p - 2 * inset, radius);
      back.roundRect(px - halo, py - halo, p + 2 * halo, p + 2 * halo, radius + halo);
    }),
  );
  return { w, h, front, back };
}

function drawWord(ctx: CanvasRenderingContext2D, shape: WordShape, t: number, dark: boolean): void {
  if (!wordShowing(t)) return;
  // The pop: the word lands from a little bigger.
  const s = 1 + 0.12 * (1 - easeOut((t - WORD_ON) / POP_FOR));
  ctx.save();
  ctx.translate(shape.w / 2, shape.h / 2);
  ctx.scale(s, s);
  ctx.translate(-shape.w / 2, -shape.h / 2);
  ctx.fillStyle = dark ? "rgb(0 0 0 / 0.42)" : "rgb(255 255 255 / 0.6)";
  ctx.fill(shape.back);
  ctx.fillStyle = wordColour(t, dark);
  ctx.fill(shape.front);
  ctx.restore();
}

// The regions of the window on their springs, with a flash while the band
// crosses them. Each is measured once, before the wave moves it.
function moveRegions(regions: Map<HTMLElement, Region>, t: number, w: number, h: number, dark: boolean): void {
  for (const el of document.querySelectorAll<HTMLElement>("[data-yolo-region]")) {
    if (regions.has(el)) continue;
    const b = el.getBoundingClientRect();
    regions.set(el, {
      x: b.left + b.width / 2,
      y: b.top + b.height / 2,
      transform: el.style.transform,
      filter: el.style.filter,
      willChange: el.style.willChange,
    });
    el.style.willChange = "transform, filter";
  }
  for (const [el, r] of regions) {
    const s = regionShift(t, w / 2, h / 2, r.x, r.y);
    el.style.transform = s.dx === 0 && s.dy === 0 ? r.transform : `${r.transform} translate3d(${s.dx.toFixed(2)}px, ${s.dy.toFixed(2)}px, 0)`;
    const i = intensity(s.since, s.env);
    el.style.filter =
      tier(s.since, s.env) === TIER_NONE
        ? r.filter
        : dark
          ? `brightness(${(1 + 0.45 * i).toFixed(3)}) saturate(${(1 + 1.2 * i).toFixed(3)}) hue-rotate(${Math.round(90 * i)}deg)`
          : `brightness(${(1 - 0.1 * i).toFixed(3)}) saturate(${(1 + 1.2 * i).toFixed(3)}) hue-rotate(${Math.round(90 * i)}deg)`;
  }
}

function restoreRegions(regions: Map<HTMLElement, Region>): void {
  for (const [el, r] of regions) {
    el.style.transform = r.transform;
    el.style.filter = r.filter;
    el.style.willChange = r.willChange;
  }
  regions.clear();
}

// The shockwave when the open room switches to yolo (useYoloTrigger counts
// the switches into play). It plays once per switch over the whole window
// and takes no focus or input; a click or Escape ends it. With reduced
// motion, or the effect off in Settings, the mode chip flashes instead.
export function YoloFx({ play }: { play: number }) {
  const [run, setRun] = useState(0);
  const canvas = useRef<HTMLCanvasElement>(null);

  useEffect(() => {
    if (play === 0) return;
    if (!getPrefs().yoloEffect || reducedMotion()) flashChip();
    else setRun(play);
  }, [play]);

  useLayoutEffect(() => {
    if (run === 0) return;
    const el = canvas.current;
    const ctx = el?.getContext("2d");
    if (el === null || el === undefined || ctx === null || ctx === undefined) {
      setRun(0);
      return;
    }
    const dark = document.documentElement.getAttribute("data-mode") !== "light";
    const regions = new Map<HTMLElement, Region>();
    const start = performance.now();
    let frame = 0;
    let shape: WordShape | null = null;
    let dpr = 0;

    const finish = () => {
      cancelAnimationFrame(frame);
      restoreRegions(regions);
      setRun(0);
    };
    const draw = (now: number) => {
      const t = Math.max(0, now - start);
      if (done(t)) return finish();
      const w = window.innerWidth;
      const h = window.innerHeight;
      const ratio = window.devicePixelRatio || 1;
      if (shape === null || shape.w !== w || shape.h !== h || dpr !== ratio) {
        el.width = Math.round(w * ratio);
        el.height = Math.round(h * ratio);
        shape = wordShape(w, h);
        dpr = ratio;
      }
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
      ctx.clearRect(0, 0, w, h);
      drawBand(ctx, t, w, h, dark);
      drawWord(ctx, shape, t, dark);
      moveRegions(regions, t, w, h, dark);
      frame = requestAnimationFrame(draw);
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === "Escape") finish();
    };
    window.addEventListener("pointerdown", finish, true);
    window.addEventListener("keydown", onKey, true);
    draw(start);
    return () => {
      window.removeEventListener("pointerdown", finish, true);
      window.removeEventListener("keydown", onKey, true);
      cancelAnimationFrame(frame);
      restoreRegions(regions);
    };
  }, [run]);

  return run === 0 ? null : createPortal(<canvas ref={canvas} className="yolo-fx" aria-hidden="true" />, document.body);
}
