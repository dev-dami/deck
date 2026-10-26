// The app icons Deck can wear, and which one a theme picks. Every icon is
// Deck's mark (the prompt chevron and the cursor bar) in the material of
// one of the window's art directions, or in the colours of a colourway.

// A colourway: the theme's canvas as the tile (top and bottom of a quiet
// vertical shade), its accent as the chevron (top and bottom) and a second
// colour of the theme as the cursor.
export interface FlatStyle {
  kind: "flat";
  tile: [string, string];
  chevron: [string, string];
  cursor: string;
  // A light tile: its edge is drawn darker, not lighter.
  light: boolean;
}

export type IconStyle =
  | FlatStyle
  // Liquid Glass: a frosted tile over a dusk sky.
  | { kind: "glass" }
  // One fluorescent pink ink, in halftone, on black or on paper.
  | { kind: "riso"; ground: "black" | "paper" }
  // Cross-stitch on linen or denim, the tile edged with a running stitch.
  | { kind: "stitch"; cloth: "linen" | "denim" };

export interface AppIconInfo {
  id: string;
  label: string;
  style: IconStyle;
}

const flat = (tile: [string, string], chevron: [string, string], cursor: string, light = false): FlatStyle => ({
  kind: "flat",
  tile,
  chevron,
  cursor,
  light,
});

export const APP_ICONS: readonly AppIconInfo[] = [
  // The icon Deck ships with (build/icon.png): amber on warm ink.
  { id: "classic", label: "Classic", style: flat(["#2b2822", "#151411"], ["#f7bb5c", "#ea972f"], "#f4eee4") },
  { id: "glass", label: "Liquid Glass", style: { kind: "glass" } },
  { id: "riso", label: "Riso", style: { kind: "riso", ground: "black" } },
  { id: "riso-paper", label: "Riso on paper", style: { kind: "riso", ground: "paper" } },
  { id: "stitch", label: "Cross-stitch", style: { kind: "stitch", cloth: "linen" } },
  { id: "stitch-denim", label: "Cross-stitch on denim", style: { kind: "stitch", cloth: "denim" } },
  { id: "apple", label: "Apple", style: flat(["#ffffff", "#e9ebf0"], ["#3d97ff", "#0a6cf0"], "#1d1d1f", true) },
  { id: "t3", label: "T3", style: flat(["#1c1c1e", "#0a0a0a"], ["#6aa3ff", "#3b78ee"], "#ededed") },
  { id: "tokyo-night", label: "Tokyo Night", style: flat(["#2a2f48", "#1a1b26"], ["#9ab8ff", "#6f96f0"], "#c0caf5") },
  { id: "catppuccin", label: "Catppuccin", style: flat(["#2b2b3d", "#1e1e2e"], ["#d7b9fa", "#b58ef0"], "#f5c2e7") },
  { id: "nord", label: "Nord", style: flat(["#3b4252", "#2e3440"], ["#9fd0dd", "#78b1c4"], "#eceff4") },
  { id: "rose-pine", label: "Rosé Pine", style: flat(["#26233a", "#191724"], ["#f0cbc9", "#e2a8a5"], "#9ccfd8") },
  { id: "dracula", label: "Dracula", style: flat(["#343746", "#21222c"], ["#cbaafb", "#a87ef5"], "#ff79c6") },
  { id: "solarized-light", label: "Solarized Light", style: flat(["#fdf6e3", "#efe7cf"], ["#3a9ae0", "#1f7fc2"], "#cb4b16", true) },
  { id: "github-light", label: "GitHub Light", style: flat(["#ffffff", "#eef1f4"], ["#2a84ef", "#0858c4"], "#1f2328", true) },
];

export const APP_ICON_IDS: readonly string[] = APP_ICONS.map((i) => i.id);

// The pref: "theme" follows the window's theme; anything else names an icon.
export type AppIconPref = "theme" | (string & {});

export function appIconInfo(id: string): AppIconInfo {
  return APP_ICONS.find((i) => i.id === id) ?? (APP_ICONS[0] as AppIconInfo);
}

// The icon of each theme in themes.ts. Riso and Cross-stitch have a ground
// for each mode: Riso prints on black in the dark and on paper in the
// light, Cross-stitch is linen in the light and denim in the dark.
const BY_THEME: Record<string, string | { light: string; dark: string }> = {
  glass: "glass",
  riso: { dark: "riso", light: "riso-paper" },
  stitch: { light: "stitch", dark: "stitch-denim" },
  tabletop: "classic",
  apple: "apple",
  default: "t3",
  "tokyo-night": "tokyo-night",
  catppuccin: "catppuccin",
  nord: "nord",
  "rose-pine": "rose-pine",
  dracula: "dracula",
  "solarized-light": "solarized-light",
  "github-light": "github-light",
};

// The icon a theme wears; a theme without one wears the classic icon.
export function iconForTheme(palette: string, mode: "light" | "dark"): string {
  const pick = Object.hasOwn(BY_THEME, palette) ? BY_THEME[palette] : undefined;
  if (pick === undefined) return "classic";
  return typeof pick === "string" ? pick : pick[mode];
}

// The icon the app shows: the pref's own when it names a known icon,
// otherwise the theme's.
export function resolveAppIcon(pref: string, palette: string, mode: "light" | "dark"): string {
  return pref !== "theme" && APP_ICON_IDS.includes(pref) ? pref : iconForTheme(palette, mode);
}
