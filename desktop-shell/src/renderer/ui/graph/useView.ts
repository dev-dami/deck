import { type RefObject, useCallback, useEffect, useLayoutEffect, useRef, useState } from "react";
import { type Bounds, fitView, type View, ZOOM_MIN, zoomAt } from "../../graph";

// The Work Graph's view: pan by dragging, zoom with the wheel or a pinch
// around the pointer, fit, and glide. The view lives in a ref and goes to
// the DOM directly (one transform on the layer, the dot grid's custom
// properties on the canvas), so a frame of a pan renders nothing in React.
// Only where the view settles is state: the part of the graph in sight,
// which pauses the live motion outside it.

export interface Pad {
  top: number;
  right: number;
  bottom: number;
  left: number;
}

// What the canvas fits: the graph's bounds, and the room to leave around
// them, read from the canvas when it fits (the composer's height).
export interface FitTarget {
  bounds: Bounds;
  pad: (canvas: HTMLElement) => Pad;
}

const GLIDE_MS = 240;
// A press that moves less than this is a click, not a pan.
const DRAG_PX = 4;
const SETTLE_MS = 140;
// Elements that are not the canvas: a press or a wheel there is theirs.
export const CONTROL = "[data-graph-control]";

const ease = (t: number) => 1 - (1 - t) ** 4;
const reduced = () => document.documentElement.dataset["motion"] === "reduced";

// minZoom: the least zoom (graph.ts ZOOM_MIN by default).
export function useView(target: RefObject<FitTarget | null>, onBackground: () => void, minZoom = ZOOM_MIN) {
  const root = useRef<HTMLDivElement>(null);
  const layer = useRef<HTMLDivElement>(null);
  const view = useRef<View>({ x: 0, y: 0, k: 1 });
  // The person moved the view: a new graph or a resize keeps it.
  const moved = useRef(false);
  const listeners = useRef(new Set<(v: View) => void>());
  const glide = useRef(0);
  const settle = useRef<ReturnType<typeof setTimeout> | null>(null);
  const [seen, setSeen] = useState<Bounds | null>(null);
  const background = useRef(onBackground);
  background.current = onBackground;

  const apply = useCallback((v: View) => {
    view.current = v;
    const el = root.current;
    if (layer.current !== null) layer.current.style.transform = `translate(${v.x}px, ${v.y}px) scale(${v.k})`;
    if (el !== null) {
      el.style.setProperty("--gx", `${v.x}px`);
      el.style.setProperty("--gy", `${v.y}px`);
      el.style.setProperty("--gk", String(v.k));
    }
    for (const l of listeners.current) l(v);
    if (settle.current !== null) clearTimeout(settle.current);
    settle.current = setTimeout(() => {
      if (el === null) return;
      setSeen({ x: -v.x / v.k, y: -v.y / v.k, w: el.clientWidth / v.k, h: el.clientHeight / v.k });
    }, SETTLE_MS);
  }, []);

  // Sets the view, at once or gliding there (never under reduced motion).
  const set = useCallback(
    (v: View, how: { glide?: boolean; user?: boolean } = {}) => {
      cancelAnimationFrame(glide.current);
      if (how.user === true) moved.current = true;
      if (how.glide !== true || reduced()) {
        apply(v);
        return;
      }
      const from = view.current;
      const start = performance.now();
      const step = (now: number) => {
        const t = Math.min(1, (now - start) / GLIDE_MS);
        const e = ease(t);
        apply({ x: from.x + (v.x - from.x) * e, y: from.y + (v.y - from.y) * e, k: from.k + (v.k - from.k) * e });
        if (t < 1) glide.current = requestAnimationFrame(step);
      };
      glide.current = requestAnimationFrame(step);
    },
    [apply],
  );

  const fit = useCallback(
    (smooth = false) => {
      const el = root.current;
      const t = target.current;
      if (el === null || t === null || el.clientWidth === 0) return;
      moved.current = false;
      set(fitView(t.bounds, el.clientWidth, el.clientHeight, t.pad(el), minZoom), { glide: smooth });
    },
    [set, target, minZoom],
  );

  // Zooms by a factor around a point of the canvas, its middle by default.
  const zoomBy = useCallback(
    (f: number, at?: { x: number; y: number }, smooth = false) => {
      const el = root.current;
      if (el === null) return;
      const v = view.current;
      const p = at ?? { x: el.clientWidth / 2, y: el.clientHeight / 2 };
      set(zoomAt(v, v.k * f, p.x, p.y, minZoom), { user: true, glide: smooth });
    },
    [set, minZoom],
  );

  // Brings a box of the graph into sight, when it is not, with a margin.
  const reveal = useCallback(
    (b: Bounds) => {
      const el = root.current;
      if (el === null) return;
      const v = view.current;
      const m = 32;
      const sx = b.x * v.k + v.x;
      const sy = b.y * v.k + v.y;
      const inside = sx >= m && sy >= m && sx + b.w * v.k <= el.clientWidth - m && sy + b.h * v.k <= el.clientHeight - m;
      if (inside) return;
      const x = el.clientWidth / 2 - (b.x + b.w / 2) * v.k;
      const y = el.clientHeight / 2 - (b.y + b.h / 2) * v.k;
      set({ ...v, x, y }, { glide: true, user: true });
    },
    [set],
  );

  const subscribe = useCallback((l: (v: View) => void) => {
    listeners.current.add(l);
    l(view.current);
    return () => {
      listeners.current.delete(l);
    };
  }, []);

  // The gestures.
  useEffect(() => {
    const el = root.current;
    if (el === null) return;
    let press: { x: number; y: number; v: View; id: number } | null = null;
    let dragging = false;
    let swallow = false;
    let wheelEnd: ReturnType<typeof setTimeout> | null = null;
    const moving = (on: boolean) => {
      if (on) el.dataset["moving"] = "";
      else delete el.dataset["moving"];
    };
    const down = (e: PointerEvent) => {
      if (e.button !== 0 || (e.target as Element).closest(CONTROL) !== null) return;
      press = { x: e.clientX, y: e.clientY, v: view.current, id: e.pointerId };
      dragging = false;
    };
    const move = (e: PointerEvent) => {
      if (press === null || e.pointerId !== press.id) return;
      const dx = e.clientX - press.x;
      const dy = e.clientY - press.y;
      if (!dragging) {
        if (Math.hypot(dx, dy) < DRAG_PX) return;
        dragging = true;
        el.setPointerCapture(e.pointerId);
        moving(true);
      }
      set({ ...press.v, x: press.v.x + dx, y: press.v.y + dy }, { user: true });
    };
    const up = (e: PointerEvent) => {
      if (press === null || e.pointerId !== press.id) return;
      if (dragging) {
        // The click that ends a pan is not a click on what lies under it.
        swallow = true;
        setTimeout(() => {
          swallow = false;
        }, 0);
        moving(false);
      } else if ((e.target as Element).closest("[data-node]") === null) background.current();
      press = null;
      dragging = false;
    };
    const click = (e: MouseEvent) => {
      if (!swallow) return;
      e.stopPropagation();
      e.preventDefault();
    };
    const wheel = (e: WheelEvent) => {
      if ((e.target as Element).closest(CONTROL) !== null) return;
      e.preventDefault();
      cancelAnimationFrame(glide.current);
      const r = el.getBoundingClientRect();
      const v = view.current;
      // A pinch (ctrl) or a mouse's wheel zooms; a trackpad's two fingers pan.
      const notch = e.deltaMode !== 0 || (e.deltaX === 0 && Number.isInteger(e.deltaY) && Math.abs(e.deltaY) >= 40);
      if (e.ctrlKey || e.metaKey || notch) {
        const f = Math.exp(-e.deltaY * (e.ctrlKey ? 0.01 : 0.0015) * (e.deltaMode === 1 ? 16 : 1));
        set(zoomAt(v, v.k * f, e.clientX - r.left, e.clientY - r.top, minZoom), { user: true });
      } else set({ ...v, x: v.x - e.deltaX, y: v.y - e.deltaY }, { user: true });
      moving(true);
      if (wheelEnd !== null) clearTimeout(wheelEnd);
      wheelEnd = setTimeout(() => moving(false), SETTLE_MS);
    };
    el.addEventListener("pointerdown", down);
    el.addEventListener("pointermove", move);
    el.addEventListener("pointerup", up);
    el.addEventListener("pointercancel", up);
    el.addEventListener("click", click, true);
    el.addEventListener("wheel", wheel, { passive: false });
    return () => {
      el.removeEventListener("pointerdown", down);
      el.removeEventListener("pointermove", move);
      el.removeEventListener("pointerup", up);
      el.removeEventListener("pointercancel", up);
      el.removeEventListener("click", click, true);
      el.removeEventListener("wheel", wheel);
      if (wheelEnd !== null) clearTimeout(wheelEnd);
    };
  }, [set, minZoom]);

  // Fit first, before the first paint; again on a resize until the person
  // moves the view.
  useLayoutEffect(() => {
    fit();
    const el = root.current;
    if (el === null) return;
    const ro = new ResizeObserver(() => {
      if (!moved.current) fit();
    });
    ro.observe(el);
    return () => ro.disconnect();
  }, [fit]);

  useEffect(
    () => () => {
      cancelAnimationFrame(glide.current);
      if (settle.current !== null) clearTimeout(settle.current);
    },
    [],
  );

  return { root, layer, view, moved, set, fit, zoomBy, reveal, subscribe, seen };
}

export type ViewApi = ReturnType<typeof useView>;
