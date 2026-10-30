import { useCallback, useSyncExternalStore } from "react";
import { type Layout, preset, validate } from "./tree";

// The room's layout, kept in this renderer's localStorage like the prefs:
// one for every project, and a project's own when the person gives it one
// (the Layout menu's "Only this project"). Nothing saved is Default, which
// follows Settings > Appearance > Layout for the side panel's side. A
// saved layout that does not check out (validate) is dropped, so a broken
// or older one gives Default, never a crash.

const KEY = "deck.desktop.layout";

interface Saved {
  global: Layout | null;
  projects: Record<string, Layout>;
}

const EMPTY: Saved = { global: null, projects: {} };

export function parseSaved(raw: string | null): Saved {
  if (raw === null) return EMPTY;
  try {
    const v: unknown = JSON.parse(raw);
    if (typeof v !== "object" || v === null) return EMPTY;
    const o = v as Record<string, unknown>;
    const projects: Record<string, Layout> = {};
    const p = o["projects"];
    if (typeof p === "object" && p !== null) {
      for (const [k, x] of Object.entries(p as Record<string, unknown>)) {
        const l = validate(x);
        if (l !== null) projects[k] = l;
      }
    }
    return { global: validate(o["global"]), projects };
  } catch {
    return EMPTY;
  }
}

let current: Saved | null = null;
const listeners = new Set<() => void>();

function saved(): Saved {
  if (current === null) {
    let raw: string | null = null;
    try {
      raw = window.localStorage.getItem(KEY);
    } catch {
      // Storage refused: Default, for this window.
    }
    current = parseSaved(raw);
  }
  return current;
}

function write(next: Saved): void {
  current = next;
  try {
    window.localStorage.setItem(KEY, JSON.stringify(next));
  } catch {
    // A private or locked storage keeps the layout for this window only.
  }
  for (const l of listeners) l();
}

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

// Default for a side panel on that side; made once per side so a render
// does not see a new tree each time.
const defaults = new Map<string, Layout>();
export function defaultLayout(side: "left" | "right"): Layout {
  let d = defaults.get(side);
  if (d === undefined) defaults.set(side, (d = preset("default", side)));
  return d;
}

export interface LayoutControl {
  layout: Layout;
  // The project has a layout of its own.
  own: boolean;
  edit: (f: (l: Layout) => Layout) => void;
  setOwn: (on: boolean) => void;
  reset: () => void;
}

// The layout a project's room shows, and how to change it.
export function useLayout(project: string, side: "left" | "right"): LayoutControl {
  const s = useSyncExternalStore(subscribe, saved);
  const own = s.projects[project] !== undefined;
  const layout = s.projects[project] ?? s.global ?? defaultLayout(side);
  const edit = useCallback(
    (f: (l: Layout) => Layout) => {
      const now = saved();
      const mine = now.projects[project];
      const from = mine ?? now.global ?? defaultLayout(side);
      const next = f(from);
      if (next === from) return;
      write(mine !== undefined ? { ...now, projects: { ...now.projects, [project]: next } } : { ...now, global: next });
    },
    [project, side],
  );
  const setOwn = useCallback(
    (on: boolean) => {
      const now = saved();
      const { [project]: mine, ...rest } = now.projects;
      if (on && mine === undefined) write({ ...now, projects: { ...now.projects, [project]: now.global ?? defaultLayout(side) } });
      else if (!on && mine !== undefined) write({ ...now, projects: rest });
    },
    [project, side],
  );
  const reset = useCallback(() => {
    const now = saved();
    if (now.projects[project] !== undefined) write({ ...now, projects: { ...now.projects, [project]: defaultLayout(side) } });
    else write({ ...now, global: null });
  }, [project, side]);
  return { layout, own, edit, setOwn, reset };
}

// Back to Default for every project without one of its own (Settings >
// Appearance > Layout moves the side panel).
export function resetGlobalLayout(): void {
  const now = saved();
  if (now.global !== null) write({ ...now, global: null });
}

// For the tests: forget what was read.
export function resetLayoutStore(): void {
  current = null;
}
