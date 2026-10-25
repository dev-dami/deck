import type { MenuCommand } from "../shared/bridge";
import { type FoundPort, portLabel } from "./ports";
import { PANELS, type Panel, SETTINGS_SECTIONS, type SettingsSection } from "./ui";
import { modKey } from "./ui/keys";

// The command palette (Ctrl/Cmd+K): its actions, the menu commands they
// run, and the search over actions and the rooms of every open project.

export interface Command {
  id: string;
  label: string;
  hint?: string;
  run: () => void;
}

// The palette command each menu command runs ("palette" opens the palette).
export const MENU_PALETTE: Record<Exclude<MenuCommand, "palette">, string> = {
  "new-thread": "new",
  "open-folder": "add-project",
  settings: "settings-general",
  "toggle-sidebar": "sidebar",
  "toggle-panel": "panel",
  "toggle-terminal": "terminal",
  about: "settings-connection",
  inbox: "inbox",
  "next-attention": "next-attention",
  "room-1": "room-1",
  "room-2": "room-2",
  "room-3": "room-3",
  "room-4": "room-4",
  "room-5": "room-5",
  "room-6": "room-6",
  "room-7": "room-7",
  "room-8": "room-8",
  "room-9": "room-9",
};

export interface CommandDeps {
  mac: boolean;
  // A local project has a terminal.
  terminal: boolean;
  newThread: () => void;
  togglePanel: (p: Panel) => void;
  toggleSidePanel: () => void;
  toggleSidebar: () => void;
  settings: (s: SettingsSection) => void;
  usage: () => void;
  team: () => void;
  inbox: () => void;
  nextAttention: () => void;
  room: (n: number) => void;
  toggleTerminal: () => void;
  theme: (t: "light" | "dark" | "deck") => void;
  addProject: () => void;
  switchDeck: () => void;
  welcome: () => void;
}

export function paletteCommands(d: CommandDeps): Command[] {
  const mod = modKey(d.mac);
  return [
    { id: "new", label: "New thread", hint: `${mod}N`, run: d.newThread },
    // The Preview tab opens on a port (previewCommands).
    ...PANELS.filter((p) => p.panel !== "preview").map((p) => ({
      id: `panel-${p.panel}`,
      label: `Toggle ${p.panel === "changes" ? "diff" : p.label.toLowerCase()}`,
      run: () => d.togglePanel(p.panel),
    })),
    { id: "sidebar", label: "Toggle sidebar", hint: `${mod}B`, run: d.toggleSidebar },
    { id: "panel", label: "Toggle side panel", run: d.toggleSidePanel },
    ...SETTINGS_SECTIONS.map((x) => ({
      id: `settings-${x.section}`,
      label: `Settings: ${x.label}`,
      ...(x.section === "general" ? { hint: `${mod},` } : {}),
      run: () => d.settings(x.section),
    })),
    { id: "usage", label: "Open usage", run: d.usage },
    { id: "team", label: "Open team", run: d.team },
    { id: "inbox", label: "Open inbox", run: d.inbox },
    { id: "next-attention", label: "Next room that needs you", hint: `${mod}${d.mac ? "⌥" : "Alt "}L`, run: d.nextAttention },
    ...[1, 2, 3, 4, 5, 6, 7, 8, 9].map((n) => ({
      id: `room-${n}`,
      label: `Open room ${n} of the sidebar`,
      hint: `${mod}${n}`,
      run: () => d.room(n),
    })),
    ...(d.terminal ? [{ id: "terminal", label: "Toggle terminal", hint: `${mod}J`, run: d.toggleTerminal }] : []),
    { id: "theme-light", label: "Theme: light", run: () => d.theme("light") },
    { id: "theme-dark", label: "Theme: dark", run: () => d.theme("dark") },
    { id: "theme-deck", label: "Theme: follow Deck", run: () => d.theme("deck") },
    { id: "add-project", label: "Add project…", hint: `${mod}O`, run: d.addProject },
    { id: "switch", label: "Switch Deck", run: d.switchDeck },
    { id: "welcome", label: "Welcome", run: d.welcome },
  ];
}

// "Preview :3000" for each open http port.
export function previewCommands(ports: readonly FoundPort[], preview: (url: string) => void): Command[] {
  return ports
    .filter((p) => p.url.startsWith("http:"))
    .map((p) => ({ id: `preview-${p.port}`, label: `Preview ${portLabel(p.port)}`, run: () => preview(p.url) }));
}

// A room the palette can find.
export interface PaletteRoom {
  // The project's key, for openRoom, and its name, shown faint.
  project: string;
  name: string;
  id: string;
  title: string;
  updated: string;
  // The branches its agents and tasks work on.
  branches: string[];
  // The text of its loaded messages, newest first; null when none are loaded.
  texts: string[] | null;
}

export type PaletteItem = { type: "action"; c: Command } | { type: "room"; room: PaletteRoom; line: string | null };

// Message text is searched from this many letters on, so a single letter
// does not match every room.
const TEXT_MIN = 3;
const RECENT = 5;
const ROOMS_MAX = 8;

const newest = (a: PaletteRoom, b: PaletteRoom) => b.updated.localeCompare(a.updated);

// The first line of the room's messages that holds phrase.
function lineWith(texts: readonly string[], phrase: string): string | null {
  for (const t of texts) {
    if (!t.toLowerCase().includes(phrase)) continue;
    for (const line of t.split("\n")) if (line.toLowerCase().includes(phrase)) return line.trim();
  }
  return null;
}

// Rooms first (by title and branch, then by message text), then actions.
// "> query" lists actions alone. An empty query shows the newest rooms.
export function searchPalette(query: string, commands: readonly Command[], rooms: readonly PaletteRoom[], limit = 12): PaletteItem[] {
  const q = query.trim();
  const actionsOnly = q.startsWith(">");
  const text = (actionsOnly ? q.slice(1) : q).trim().toLowerCase();
  const words = text.split(/\s+/).filter(Boolean);
  const has = (s: string) => words.every((w) => s.toLowerCase().includes(w));
  const actions: PaletteItem[] = commands.filter((c) => has(c.label)).map((c) => ({ type: "action", c }));
  if (actionsOnly) return actions.slice(0, limit);

  const sorted = [...rooms].sort(newest);
  let found: PaletteItem[];
  if (words.length === 0) {
    found = sorted.slice(0, RECENT).map((room) => ({ type: "room", room, line: null }));
  } else {
    const byName = sorted.filter((r) => has(`${r.title} ${r.branches.join(" ")}`));
    const byText: PaletteItem[] = [];
    if (text.length >= TEXT_MIN) {
      for (const r of sorted) {
        if (r.texts === null || byName.includes(r)) continue;
        const line = lineWith(r.texts, text);
        if (line !== null) byText.push({ type: "room", room: r, line });
      }
    }
    found = [...byName.map((room): PaletteItem => ({ type: "room", room, line: null })), ...byText].slice(0, ROOMS_MAX);
  }
  return [...found, ...actions].slice(0, limit);
}
