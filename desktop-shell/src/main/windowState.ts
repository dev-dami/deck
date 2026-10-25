import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";

export interface Rect {
  x: number;
  y: number;
  width: number;
  height: number;
}

// The window as it was when the app last saw it. x and y are null before
// the first save, and the window then opens centred.
export interface WindowState {
  width: number;
  height: number;
  x: number | null;
  y: number | null;
  maximized: boolean;
  // Chromium's zoom level: 0 is 100%, each step of 1 is 20% more.
  zoom: number;
}

export const DEFAULT_WINDOW: WindowState = { width: 1320, height: 840, x: null, y: null, maximized: false, zoom: 0 };
export const MIN_SIZE = { width: 960, height: 560 };

const ZOOM_STEP = 0.5;
const ZOOM_MIN = -3;
const ZOOM_MAX = 5;

const isNum = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);

// The zoom level one step in (1), out (-1) or back to 100% (0).
export function nextZoom(level: number, step: -1 | 0 | 1): number {
  if (step === 0) return 0;
  return Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, level + step * ZOOM_STEP));
}

// Reads what window.json held; anything missing or malformed takes its
// default.
export function parseWindowState(v: unknown): WindowState {
  if (typeof v !== "object" || v === null) return { ...DEFAULT_WINDOW };
  const r = v as Record<string, unknown>;
  const size = (n: unknown, d: number) => (isNum(n) && n >= 100 && n <= 100_000 ? Math.round(n) : d);
  const placed = isNum(r["x"]) && isNum(r["y"]);
  return {
    width: size(r["width"], DEFAULT_WINDOW.width),
    height: size(r["height"], DEFAULT_WINDOW.height),
    x: placed ? Math.round(r["x"] as number) : null,
    y: placed ? Math.round(r["y"] as number) : null,
    maximized: r["maximized"] === true,
    zoom: isNum(r["zoom"]) ? Math.min(ZOOM_MAX, Math.max(ZOOM_MIN, r["zoom"])) : 0,
  };
}

function overlap(a: Rect, b: Rect): { width: number; height: number } {
  return {
    width: Math.max(0, Math.min(a.x + a.width, b.x + b.width) - Math.max(a.x, b.x)),
    height: Math.max(0, Math.min(a.y + a.height, b.y + b.height) - Math.max(a.y, b.y)),
  };
}

// Where the window opens: on the display it overlaps most, made to fit its
// work area. A window that is mostly off every display (one that was
// unplugged, say) opens centred on the first display.
export function placeWindow(s: WindowState, workAreas: Rect[]): { width: number; height: number; x?: number; y?: number } {
  const fit = (area: Rect | undefined) => ({
    width: Math.min(Math.max(s.width, MIN_SIZE.width), area?.width ?? Infinity),
    height: Math.min(Math.max(s.height, MIN_SIZE.height), area?.height ?? Infinity),
  });
  if (s.x === null || s.y === null) return fit(workAreas[0]);
  const rect = { x: s.x, y: s.y, width: s.width, height: s.height };
  let best: Rect | undefined;
  let bestArea = 0;
  for (const area of workAreas) {
    const o = overlap(rect, area);
    if (o.width * o.height > bestArea) {
      best = area;
      bestArea = o.width * o.height;
    }
  }
  // Enough of it to grab the title strip.
  const o = best === undefined ? { width: 0, height: 0 } : overlap(rect, best);
  if (best === undefined || o.width < 100 || o.height < 40) return fit(workAreas[0]);
  const size = fit(best);
  return {
    ...size,
    x: Math.max(best.x, Math.min(s.x, best.x + best.width - size.width)),
    y: Math.max(best.y, Math.min(s.y, best.y + best.height - size.height)),
  };
}

// window.json in the app's data folder. file is null for a state in memory
// only.
export class WindowStateFile {
  state: WindowState;

  constructor(private readonly file: string | null) {
    let raw: unknown = null;
    if (file !== null) {
      try {
        raw = JSON.parse(readFileSync(file, "utf8"));
      } catch {
        // No state yet.
      }
    }
    this.state = parseWindowState(raw);
  }

  set(patch: Partial<WindowState>): void {
    this.state = { ...this.state, ...patch };
    if (this.file === null) return;
    try {
      mkdirSync(dirname(this.file), { recursive: true, mode: 0o700 });
      const tmp = `${this.file}.tmp`;
      writeFileSync(tmp, `${JSON.stringify(this.state, null, 2)}\n`, { mode: 0o600 });
      renameSync(tmp, this.file);
    } catch {
      // A read-only data folder costs the next launch its size, nothing more.
    }
  }
}

// The parts of a BrowserWindow that trackWindow uses.
export interface TrackedWindow {
  getNormalBounds(): Rect;
  isMaximized(): boolean;
  isMinimized(): boolean;
  on(event: "resize" | "move" | "maximize" | "unmaximize" | "close", fn: () => void): unknown;
}

// Saves the window's size, place and maximized state when it moves or
// resizes (once it has been still for delayMs) and when it closes.
export function trackWindow(w: TrackedWindow, store: WindowStateFile, delayMs = 500): void {
  let timer: ReturnType<typeof setTimeout> | null = null;
  const save = () => {
    if (timer !== null) clearTimeout(timer);
    timer = null;
    if (w.isMinimized()) return;
    // The normal bounds, so a maximized window comes back to its old size
    // when it is restored.
    const b = w.getNormalBounds();
    store.set({ x: b.x, y: b.y, width: b.width, height: b.height, maximized: w.isMaximized() });
  };
  const later = () => {
    if (timer !== null) clearTimeout(timer);
    timer = setTimeout(save, delayMs);
  };
  for (const ev of ["resize", "move", "maximize", "unmaximize"] as const) w.on(ev, later);
  w.on("close", save);
}
