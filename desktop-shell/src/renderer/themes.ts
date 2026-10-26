// The window's themes, after T3 Code's theme catalog. Three are art
// directions (design-glass.md, "Art directions"): Liquid Glass, the
// default, Riso and Stitch. Each prints the backdrop's picture through its
// own process (backdrop/) and has its own material, display face and
// texture. Apple, Tabletop, T3 and the rest of the catalog are colourways
// of Liquid Glass. glass, riso, stitch, apple, tabletop and default follow
// light or dark; every other theme has a mode of its own. Each theme's
// tokens are in styles.css under [data-palette="<id>"].

export interface ThemeInfo {
  id: string;
  label: string;
  mode: "light" | "dark" | null;
  // The swatches the picker shows, as CSS colours: background, sidebar or
  // surface, text, accent. A theme that follows light or dark can add the
  // light ones; the picker then shows both halves.
  swatch: [string, string, string, string];
  light?: [string, string, string, string];
}

// An art direction, as the theme card describes it.
export interface Direction {
  id: "glass" | "riso" | "stitch";
  // How the picture is printed, what the panels are made of, and the
  // display face (its name, and the CSS family that sets it).
  process: string;
  material: string;
  face: string;
  family: string;
  // The colours it prints in.
  inks: string;
}

export const DIRECTIONS: Record<Direction["id"], Direction> = {
  glass: {
    id: "glass",
    process: "Atkinson dither",
    material: "Liquid glass",
    face: "Inter Tight",
    family: '"Inter Tight Variable", sans-serif',
    inks: "Dusk navy and peach",
  },
  riso: {
    id: "riso",
    process: "Halftone, 15°",
    material: "Paper and vellum",
    face: "Space Grotesk",
    family: '"Space Grotesk Variable", sans-serif',
    inks: "Fluorescent pink and black",
  },
  stitch: {
    id: "stitch",
    process: "Cross-stitch",
    material: "Felt on linen or denim",
    face: "Pixelify Sans",
    family: '"Pixelify Sans", sans-serif',
    inks: "Cotton floss",
  },
};

// A colourway prints as Liquid Glass does, in its own colours.
export function directionOf(id: string): Direction {
  return id === "riso" || id === "stitch" ? DIRECTIONS[id] : DIRECTIONS.glass;
}

export const THEMES: readonly ThemeInfo[] = [
  {
    id: "glass",
    label: "Liquid Glass",
    mode: null,
    swatch: ["#19191b", "#2a2a2e", "#e3e3e5", "#0a84ff"],
    light: ["#fbfbfd", "#dcdee5", "#1d1d1f", "#007aff"],
  },
  {
    id: "riso",
    label: "Riso",
    mode: null,
    swatch: ["#121111", "#1c1a1a", "#f3eee3", "#ff48b0"],
    light: ["#f3eee3", "#e9e2d3", "#1d1b1b", "#ff48b0"],
  },
  {
    id: "stitch",
    label: "Stitch",
    mode: null,
    swatch: ["#1b2036", "#232a45", "#efe6d2", "#e4b04a"],
    light: ["#f4ecdc", "#eadfc9", "#2b2420", "#b8262d"],
  },
  {
    id: "apple",
    label: "Apple",
    mode: null,
    swatch: ["#1e1e1e", "#29292b", "#e3e3e5", "#0a84ff"],
    light: ["#ffffff", "#efeff1", "#1d1d1f", "#007aff"],
  },
  {
    id: "tabletop",
    label: "Tabletop",
    mode: null,
    swatch: ["oklch(0.145 0.005 70)", "oklch(0.12 0.005 70)", "oklch(0.93 0.008 80)", "oklch(0.8 0.14 75)"],
    light: ["oklch(0.975 0.006 80)", "oklch(0.95 0.006 80)", "oklch(0.22 0.01 70)", "oklch(0.76 0.15 70)"],
  },
  { id: "default", label: "T3", mode: null, swatch: ["#0a0a0a", "#1a1a1a", "#ededed", "#3b82f6"], light: ["#fcfcfc", "#f7f7f8", "#27272a", "#2563eb"] },
  { id: "tokyo-night", label: "Tokyo Night", mode: "dark", swatch: ["#1a1b26", "#24283b", "#c0caf5", "#7aa2f7"] },
  { id: "catppuccin", label: "Catppuccin Mocha", mode: "dark", swatch: ["#1e1e2e", "#313244", "#cdd6f4", "#cba6f7"] },
  { id: "nord", label: "Nord", mode: "dark", swatch: ["#2e3440", "#3b4252", "#eceff4", "#88c0d0"] },
  { id: "rose-pine", label: "Rosé Pine", mode: "dark", swatch: ["#191724", "#26233a", "#e0def4", "#ebbcba"] },
  { id: "dracula", label: "Dracula", mode: "dark", swatch: ["#282a36", "#343746", "#f8f8f2", "#bd93f9"] },
  { id: "solarized-light", label: "Solarized Light", mode: "light", swatch: ["#fdf6e3", "#eee8d5", "#586e75", "#268bd2"] },
  { id: "github-light", label: "GitHub Light", mode: "light", swatch: ["#ffffff", "#f6f8fa", "#1f2328", "#0969da"] },
];

export const ACCENTS: readonly { id: string; label: string; color: string }[] = [
  { id: "theme", label: "Theme", color: "" },
  { id: "amber", label: "Amber", color: "oklch(0.8 0.14 75)" },
  { id: "blue", label: "Blue", color: "oklch(0.571 0.21 264)" },
  { id: "violet", label: "Violet", color: "oklch(0.58 0.22 295)" },
  { id: "green", label: "Green", color: "oklch(0.62 0.16 155)" },
  { id: "orange", label: "Orange", color: "oklch(0.68 0.17 50)" },
  { id: "pink", label: "Pink", color: "oklch(0.64 0.2 350)" },
  { id: "graphite", label: "Graphite", color: "#8c8c91" },
];

// Diff themes: the colours of added and removed lines and of the syntax in
// diffs and code blocks. "match" derives them from the window's theme;
// each other one is a block under [data-diff-theme="<id>"] in styles.css.
// The swatch: background, added, removed, keyword.
export interface DiffThemeInfo {
  id: string;
  label: string;
  swatch: [string, string, string, string];
}

export const DIFF_THEMES: readonly DiffThemeInfo[] = [
  { id: "match", label: "Match app theme", swatch: ["var(--canvas)", "var(--success)", "var(--destructive)", "var(--syn-keyword)"] },
  { id: "github-dark", label: "GitHub Dark", swatch: ["#0d1117", "#3fb950", "#f85149", "#ff7b72"] },
  { id: "github-light", label: "GitHub Light", swatch: ["#ffffff", "#1a7f37", "#cf222e", "#cf222e"] },
  { id: "one-dark", label: "One Dark", swatch: ["#282c34", "#98c379", "#e06c75", "#c678dd"] },
  { id: "dracula", label: "Dracula", swatch: ["#282a36", "#50fa7b", "#ff5555", "#ff79c6"] },
  { id: "tokyo-night", label: "Tokyo Night", swatch: ["#1a1b26", "#9ece6a", "#f7768e", "#bb9af7"] },
  { id: "catppuccin-mocha", label: "Catppuccin Mocha", swatch: ["#1e1e2e", "#a6e3a1", "#f38ba8", "#cba6f7"] },
  { id: "solarized", label: "Solarized", swatch: ["#002b36", "#859900", "#dc322f", "#859900"] },
  { id: "monochrome", label: "Monochrome", swatch: ["var(--canvas)", "var(--success)", "var(--destructive)", "var(--foreground)"] },
];

export function themeInfo(id: string): ThemeInfo {
  return THEMES.find((t) => t.id === id) ?? (THEMES[0] as ThemeInfo);
}
