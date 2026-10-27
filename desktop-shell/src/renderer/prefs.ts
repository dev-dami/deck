import { useSyncExternalStore } from "react";
import { APP_ICON_IDS, type AppIconPref } from "./appIcon/catalog";
import type { NotifyPref } from "./attention";
import { CUSTOM, PICTURES } from "./backdrop/pictures";
import {
  ACCENTS,
  type Aspects,
  CORNERS,
  DIFF_THEMES,
  DIRECTIONS,
  FACES,
  lookFromPalette,
  MATERIALS,
  PROCESSES,
  TEXTURES,
  THEMES,
  TRANSLUCENCIES,
} from "./themes";

// The window's own settings (the Settings page). They live in this
// renderer's localStorage: nothing here is secret and Deck never sees them.

export type ThemePref = "deck" | "system" | "light" | "dark";
export type RowStyle = "cards" | "compact";
export type Grouping = "projects" | "recent";
export type ClockPref = "12h" | "24h";
export type SendKey = "enter" | "mod-enter";
// "system" follows the operating system's reduced-motion setting.
export type MotionPref = "system" | "full" | "reduced";
export type DiffLayout = "unified" | "split";
// Where the sidebar sits, and where the side panel docks: on the side
// opposite it, beside it (between it and the room), or floating over the
// room on the side opposite it.
export type SidebarSide = "left" | "right";
export type PanelDock = "opposite" | "beside" | "float";
export type Density = "comfortable" | "compact";

export const SIDEBAR_WIDTH = { min: 208, max: 420, default: 256 } as const;

// The look's aspects (themes.ts): palette is the theme whose colours the
// window takes, and the accent goes over it.
export interface Prefs extends Aspects {
  theme: ThemePref;
  accent: string;
  rows: RowStyle;
  grouping: Grouping;
  clock: ClockPref;
  sendKey: SendKey;
  confirmYolo: boolean;
  foldWork: boolean;
  // A plan card approves itself once when it appears.
  autoApprovePlans: boolean;
  // The terminal drawer's height in pixels.
  terminalHeight: number;
  // Native notifications while the window is in the background.
  notifications: NotifyPref;
  sound: boolean;
  // A new thread starts on a branch of its own (a Deck that can isolate).
  isolate: boolean;
  motion: MotionPref;
  // The shockwave when the open room switches to Yolo.
  yoloEffect: boolean;
  // Diffs: the theme from themes.ts, unified or split, and whether long
  // lines wrap or scroll.
  diffTheme: string;
  diffView: DiffLayout;
  diffWrap: boolean;
  // The backdrop's picture: "theme" for the theme's own, a picture from
  // the library (backdrop/pictures.ts), or "custom" for the person's own,
  // whose bytes are in IndexedDB (backdrop/wallpaper.ts).
  wallpaper: string;
  // The app icon: "theme" wears the theme's (appIcon/catalog.ts), an icon
  // id wears that one.
  appIcon: AppIconPref;
  // The layout (Settings > Appearance > Layout): the sidebar's side and
  // width in pixels, where the side panel docks, and how tight the window
  // is spaced.
  sidebarSide: SidebarSide;
  sidebarWidth: number;
  panelDock: PanelDock;
  density: Density;
}

export const defaultPrefs: Prefs = {
  theme: "deck",
  ...DIRECTIONS.glass.look,
  accent: "theme",
  rows: "compact",
  grouping: "projects",
  clock: "12h",
  sendKey: "enter",
  confirmYolo: true,
  foldWork: true,
  autoApprovePlans: false,
  terminalHeight: 280,
  notifications: "all",
  sound: true,
  isolate: true,
  motion: "system",
  yoloEffect: true,
  diffTheme: "match",
  diffView: "unified",
  diffWrap: false,
  wallpaper: "theme",
  appIcon: "theme",
  sidebarSide: "left",
  sidebarWidth: SIDEBAR_WIDTH.default,
  panelDock: "opposite",
  density: "comfortable",
};

const KEY = "deck.desktop.prefs";

function load(): Prefs {
  try {
    const raw = window.localStorage.getItem(KEY);
    if (raw === null) return defaultPrefs;
    const v: unknown = JSON.parse(raw);
    return typeof v === "object" && v !== null ? merge(v as Record<string, unknown>) : defaultPrefs;
  } catch {
    return defaultPrefs;
  }
}

// Only known keys with values of the right kind survive a load. Prefs saved
// before the look had aspects name one theme; it becomes the aspects it
// stood for (themes.ts lookFromPalette).
export function merge(v: Record<string, unknown>): Prefs {
  const pick = <T extends string>(k: string, allowed: readonly T[], d: T): T =>
    allowed.includes(v[k] as T) ? (v[k] as T) : d;
  const ids = <T extends string>(xs: readonly { id: T }[]) => xs.map((x) => x.id);
  const bool = (k: string, d: boolean): boolean => (typeof v[k] === "boolean" ? (v[k] as boolean) : d);
  const size = (k: string, d: number): number => {
    const n = v[k];
    return typeof n === "number" && Number.isFinite(n) && n > 0 ? n : d;
  };
  const palette = pick("palette", ids(THEMES), defaultPrefs.palette);
  const look = "material" in v ? { ...defaultPrefs, palette } : lookFromPalette(palette);
  return {
    theme: pick("theme", ["deck", "system", "light", "dark"] as const, defaultPrefs.theme),
    palette,
    material: pick("material", ids(MATERIALS), look.material),
    process: pick("process", ids(PROCESSES), look.process),
    face: pick("face", ids(FACES), look.face),
    texture: pick("texture", ids(TEXTURES), look.texture),
    corners: pick("corners", ids(CORNERS), look.corners),
    translucency: pick("translucency", ids(TRANSLUCENCIES), look.translucency),
    accent: pick("accent", ACCENTS.map((a) => a.id), defaultPrefs.accent),
    rows: pick("rows", ["cards", "compact"] as const, defaultPrefs.rows),
    grouping: pick("grouping", ["projects", "recent"] as const, defaultPrefs.grouping),
    clock: pick("clock", ["12h", "24h"] as const, defaultPrefs.clock),
    sendKey: pick("sendKey", ["enter", "mod-enter"] as const, defaultPrefs.sendKey),
    confirmYolo: bool("confirmYolo", defaultPrefs.confirmYolo),
    foldWork: bool("foldWork", defaultPrefs.foldWork),
    autoApprovePlans: bool("autoApprovePlans", defaultPrefs.autoApprovePlans),
    terminalHeight: size("terminalHeight", defaultPrefs.terminalHeight),
    notifications: pick("notifications", ["all", "needs-you", "none"] as const, defaultPrefs.notifications),
    sound: bool("sound", defaultPrefs.sound),
    isolate: bool("isolate", defaultPrefs.isolate),
    motion: pick("motion", ["system", "full", "reduced"] as const, defaultPrefs.motion),
    yoloEffect: bool("yoloEffect", defaultPrefs.yoloEffect),
    diffTheme: pick("diffTheme", DIFF_THEMES.map((t) => t.id), defaultPrefs.diffTheme),
    diffView: pick("diffView", ["unified", "split"] as const, defaultPrefs.diffView),
    diffWrap: bool("diffWrap", defaultPrefs.diffWrap),
    wallpaper: pick("wallpaper", ["theme", CUSTOM, ...PICTURES.map((x) => x.id)], defaultPrefs.wallpaper),
    appIcon: pick("appIcon", ["theme", ...APP_ICON_IDS], defaultPrefs.appIcon),
    sidebarSide: pick("sidebarSide", ["left", "right"] as const, defaultPrefs.sidebarSide),
    sidebarWidth: clampSidebar(size("sidebarWidth", defaultPrefs.sidebarWidth)),
    panelDock: pick("panelDock", ["opposite", "beside", "float"] as const, defaultPrefs.panelDock),
    density: pick("density", ["comfortable", "compact"] as const, defaultPrefs.density),
  };
}

export function clampSidebar(w: number): number {
  return Math.round(Math.min(SIDEBAR_WIDTH.max, Math.max(SIDEBAR_WIDTH.min, w)));
}

let current: Prefs | null = null;
const listeners = new Set<() => void>();

export function getPrefs(): Prefs {
  current ??= load();
  return current;
}

export function setPrefs(patch: Partial<Prefs>): void {
  current = { ...getPrefs(), ...patch };
  try {
    window.localStorage.setItem(KEY, JSON.stringify(current));
  } catch {
    // A private or locked storage keeps the change for this window only.
  }
  for (const l of listeners) l();
}

export function subscribePrefs(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function usePrefs(): Prefs {
  return useSyncExternalStore(subscribePrefs, getPrefs);
}

export function formatClock(t: string, clock: ClockPref): string {
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", hour12: clock === "12h" });
}
