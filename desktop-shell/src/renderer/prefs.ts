import { useSyncExternalStore } from "react";
import { ACCENTS, THEMES } from "./themes";

// The window's own settings (the Settings page). They live in this
// renderer's localStorage: nothing here is secret and Deck never sees them.

export type ThemePref = "deck" | "system" | "light" | "dark";
export type RowStyle = "cards" | "compact";
export type Grouping = "projects" | "recent";
export type ClockPref = "12h" | "24h";
export type SendKey = "enter" | "mod-enter";

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
}

export const defaultPrefs: Prefs = {
  theme: "deck",
  palette: "tabletop",
  accent: "theme",
  rows: "compact",
  grouping: "projects",
  clock: "12h",
  sendKey: "enter",
  confirmYolo: true,
  foldWork: true,
  autoApprovePlans: false,
  terminalHeight: 280,
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
function merge(v: Record<string, unknown>): Prefs {
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
  };
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

function subscribe(l: () => void): () => void {
  listeners.add(l);
  return () => listeners.delete(l);
}

export function usePrefs(): Prefs {
  return useSyncExternalStore(subscribe, getPrefs);
}

export function formatClock(t: string, clock: ClockPref): string {
  const d = new Date(t);
  if (Number.isNaN(d.getTime())) return "";
  return d.toLocaleTimeString([], { hour: "numeric", minute: "2-digit", hour12: clock === "12h" });
}
