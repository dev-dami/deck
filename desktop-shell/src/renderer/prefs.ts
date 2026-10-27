import { useSyncExternalStore } from "react";
import { APP_ICON_IDS, type AppIconPref } from "./appIcon/catalog";
import { CUSTOM, PICTURES } from "./backdrop/pictures";
import { NOTIFY_KINDS, type NotifyKind } from "./notifications";
import { ACCENTS, DIFF_THEMES, THEMES } from "./themes";

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

export interface Prefs {
  theme: ThemePref;
  // The theme from themes.ts and the accent over it.
  palette: string;
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
  // Notifications (notifications.ts): the master switch, each kind, the
  // sound, and whether they pop up only while the window is in the
  // background.
  notifications: boolean;
  notifyKinds: Record<NotifyKind, boolean>;
  sound: boolean;
  notifyUnfocusedOnly: boolean;
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
  // The team and the named agents (teamGate.ts); off hides them.
  team: boolean;
}

export const defaultPrefs: Prefs = {
  theme: "deck",
  palette: "glass",
  accent: "theme",
  rows: "compact",
  grouping: "projects",
  clock: "12h",
  sendKey: "enter",
  confirmYolo: true,
  foldWork: true,
  autoApprovePlans: false,
  terminalHeight: 280,
  notifications: true,
  notifyKinds: { approval: true, error: true, mention: true, task: true, finished: true },
  sound: true,
  notifyUnfocusedOnly: true,
  isolate: true,
  motion: "system",
  yoloEffect: true,
  diffTheme: "match",
  diffView: "unified",
  diffWrap: false,
  wallpaper: "theme",
  appIcon: "theme",
  team: true,
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

// Only known keys with values of the right kind survive a load.
export function merge(v: Record<string, unknown>): Prefs {
  const pick = <T extends string>(k: string, allowed: readonly T[], d: T): T =>
    allowed.includes(v[k] as T) ? (v[k] as T) : d;
  const bool = (k: string, d: boolean): boolean => (typeof v[k] === "boolean" ? (v[k] as boolean) : d);
  const size = (k: string, d: number): number => {
    const n = v[k];
    return typeof n === "number" && Number.isFinite(n) && n > 0 ? n : d;
  };
  return {
    theme: pick("theme", ["deck", "system", "light", "dark"] as const, defaultPrefs.theme),
    palette: pick("palette", THEMES.map((t) => t.id), defaultPrefs.palette),
    accent: pick("accent", ACCENTS.map((a) => a.id), defaultPrefs.accent),
    rows: pick("rows", ["cards", "compact"] as const, defaultPrefs.rows),
    grouping: pick("grouping", ["projects", "recent"] as const, defaultPrefs.grouping),
    clock: pick("clock", ["12h", "24h"] as const, defaultPrefs.clock),
    sendKey: pick("sendKey", ["enter", "mod-enter"] as const, defaultPrefs.sendKey),
    confirmYolo: bool("confirmYolo", defaultPrefs.confirmYolo),
    foldWork: bool("foldWork", defaultPrefs.foldWork),
    autoApprovePlans: bool("autoApprovePlans", defaultPrefs.autoApprovePlans),
    terminalHeight: size("terminalHeight", defaultPrefs.terminalHeight),
    // Before the kinds, notifications was "all", "needs-you" or "none".
    notifications: v["notifications"] === "none" ? false : bool("notifications", defaultPrefs.notifications),
    notifyKinds: kinds(v["notifyKinds"], v["notifications"] === "needs-you"),
    sound: bool("sound", defaultPrefs.sound),
    notifyUnfocusedOnly: bool("notifyUnfocusedOnly", defaultPrefs.notifyUnfocusedOnly),
    isolate: bool("isolate", defaultPrefs.isolate),
    motion: pick("motion", ["system", "full", "reduced"] as const, defaultPrefs.motion),
    yoloEffect: bool("yoloEffect", defaultPrefs.yoloEffect),
    diffTheme: pick("diffTheme", DIFF_THEMES.map((t) => t.id), defaultPrefs.diffTheme),
    diffView: pick("diffView", ["unified", "split"] as const, defaultPrefs.diffView),
    diffWrap: bool("diffWrap", defaultPrefs.diffWrap),
    wallpaper: pick("wallpaper", ["theme", CUSTOM, ...PICTURES.map((x) => x.id)], defaultPrefs.wallpaper),
    appIcon: pick("appIcon", ["theme", ...APP_ICON_IDS], defaultPrefs.appIcon),
    team: bool("team", defaultPrefs.team),
  };
}

// Each kind's switch; one that is missing keeps its default, and "needs-you
// only" from before turns the finishes off.
function kinds(v: unknown, needsYouOnly: boolean): Record<NotifyKind, boolean> {
  const o = typeof v === "object" && v !== null ? (v as Record<string, unknown>) : {};
  const out = { ...defaultPrefs.notifyKinds };
  if (needsYouOnly) out.finished = out.task = false;
  for (const k of NOTIFY_KINDS) if (typeof o[k.id] === "boolean") out[k.id] = o[k.id] as boolean;
  return out;
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
