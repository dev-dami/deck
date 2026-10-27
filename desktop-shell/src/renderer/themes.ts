// The window's look, as aspects that mix (design-glass.md, "Art
// directions"): the colours (a theme of THEMES, after T3 Code's catalog),
// the material the panels are made of, the process the backdrop's picture
// is printed through (backdrop/), the display face, the texture in the
// panels, and two knobs, the corners and the glass's translucency. Each
// aspect is an attribute on <html> (appearance.ts) that styles.css reads:
// data-palette (a theme's colours, under [data-palette="<id>"]),
// data-material, data-process, data-face, data-texture, data-corners and
// data-translucency.
//
// The three art directions, Liquid Glass (the default), Riso and Stitch,
// are presets that set every aspect at once; a look that matches none of
// them is "Custom". glass, riso, stitch, apple, tabletop and default follow
// light or dark; every other theme has a mode of its own.

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

export type Material = "glass" | "paper" | "felt" | "plain";
// "none" prints no picture: the window behind the panels is plain.
export type PictureProcess = "dither" | "halftone" | "stitch" | "none";
export type Face = "inter-tight" | "space-grotesk" | "pixelify" | "system";
export type Texture = "none" | "fibre" | "felt";
// "auto" keeps the material's own.
export type Corners = "auto" | "square" | "soft" | "round";
// The glass's alone: how much of what is behind shows through it.
export type Translucency = "auto" | "clear" | "frosted" | "solid";

export interface Aspects {
  palette: string;
  material: Material;
  process: PictureProcess;
  face: Face;
  texture: Texture;
  corners: Corners;
  translucency: Translucency;
}

// Each aspect's choices, in the editor's order, with a line on each.
export interface Choice<T extends string> {
  id: T;
  label: string;
  line: string;
}

export const MATERIALS: readonly Choice<Material>[] = [
  { id: "glass", label: "Liquid glass", line: "Tinted and blurred, with a bright rim" },
  { id: "paper", label: "Paper", line: "Square, ruled, a shadow out of register" },
  { id: "felt", label: "Felt", line: "Opaque, with a running stitch" },
  { id: "plain", label: "Plain", line: "Opaque and flat" },
];

export const PROCESSES: readonly Choice<PictureProcess>[] = [
  { id: "dither", label: "Dither", line: "Atkinson, in two inks" },
  { id: "halftone", label: "Halftone", line: "One spot ink at 15°" },
  { id: "stitch", label: "Cross-stitch", line: "Floss on even-weave cloth" },
  { id: "none", label: "None", line: "No picture" },
];

// A face's CSS family, for the editor's specimens; styles.css sets it.
export const FACES: readonly (Choice<Face> & { family: string })[] = [
  { id: "inter-tight", label: "Inter Tight", line: "Tight and modern", family: '"Inter Tight Variable", sans-serif' },
  { id: "space-grotesk", label: "Space Grotesk", line: "With Martian Mono for code", family: '"Space Grotesk Variable", sans-serif' },
  { id: "pixelify", label: "Pixelify Sans", line: "Pixels, for headings", family: '"Pixelify Sans", sans-serif' },
  { id: "system", label: "System", line: "The UI's own face", family: "var(--font-sans)" },
];

export const TEXTURES: readonly Choice<Texture>[] = [
  { id: "none", label: "None", line: "Smooth" },
  { id: "fibre", label: "Paper fibre", line: "Specks in the stock" },
  { id: "felt", label: "Felt", line: "Matted fibres" },
];

export const CORNERS: readonly Choice<Corners>[] = [
  { id: "auto", label: "Auto", line: "The material's own" },
  { id: "square", label: "Square", line: "Cut square" },
  { id: "soft", label: "Soft", line: "As Liquid Glass" },
  { id: "round", label: "Round", line: "Rounder still" },
];

export const TRANSLUCENCIES: readonly Choice<Translucency>[] = [
  { id: "clear", label: "Clear", line: "More of the picture" },
  { id: "auto", label: "Auto", line: "The theme's own" },
  { id: "frosted", label: "Frosted", line: "More blur and tint" },
  { id: "solid", label: "Solid", line: "Nothing shows through" },
];

// An art direction: a preset of every aspect, and how its card describes
// it.
export type PresetId = "glass" | "riso" | "stitch";
export interface Direction {
  id: PresetId;
  // How the picture is printed, what the panels are made of, and the
  // display face (its name, and the CSS family that sets it).
  process: string;
  material: string;
  face: string;
  family: string;
  // The colours it prints in.
  inks: string;
  look: Aspects;
}

const KNOBS = { corners: "auto", translucency: "auto" } as const;

export const DIRECTIONS: Record<PresetId, Direction> = {
  glass: {
    id: "glass",
    process: "Atkinson dither",
    material: "Liquid glass",
    face: "Inter Tight",
    family: '"Inter Tight Variable", sans-serif',
    inks: "Dusk navy and peach",
    look: { palette: "glass", material: "glass", process: "dither", face: "inter-tight", texture: "none", ...KNOBS },
  },
  riso: {
    id: "riso",
    process: "Halftone, 15°",
    material: "Paper and vellum",
    face: "Space Grotesk",
    family: '"Space Grotesk Variable", sans-serif',
    inks: "Fluorescent pink and black",
    look: { palette: "riso", material: "paper", process: "halftone", face: "space-grotesk", texture: "fibre", ...KNOBS },
  },
  stitch: {
    id: "stitch",
    process: "Cross-stitch",
    material: "Felt on linen or denim",
    face: "Pixelify Sans",
    family: '"Pixelify Sans", sans-serif',
    inks: "Cotton floss",
    look: { palette: "stitch", material: "felt", process: "stitch", face: "pixelify", texture: "felt", ...KNOBS },
  },
};

export const PRESET_IDS: readonly PresetId[] = ["glass", "riso", "stitch"];

export const ASPECT_KEYS: readonly (keyof Aspects)[] = ["palette", "material", "process", "face", "texture", "corners", "translucency"];

// The preset a look is, or "custom" once any aspect differs from all three.
export function presetOf(a: Aspects): PresetId | "custom" {
  return PRESET_IDS.find((id) => ASPECT_KEYS.every((k) => DIRECTIONS[id].look[k] === a[k])) ?? "custom";
}

// The preset a look shares most aspects with, for "Custom, from Riso". The
// material breaks a tie, and Liquid Glass wins what is left.
export function nearestPreset(a: Aspects): PresetId {
  const score = (id: PresetId) =>
    ASPECT_KEYS.filter((k) => DIRECTIONS[id].look[k] === a[k]).length + (DIRECTIONS[id].look.material === a.material ? 0.5 : 0);
  return PRESET_IDS.reduce<PresetId>((best, id) => (score(id) > score(best) ? id : best), "glass");
}

// A window from before the aspects saved one theme. An art direction
// becomes its preset; any other theme was a colourway of Liquid Glass, and
// stays one.
export function lookFromPalette(palette: string): Aspects {
  if (palette === "riso" || palette === "stitch") return { ...DIRECTIONS[palette].look };
  return { ...DIRECTIONS.glass.look, palette: THEMES.some((t) => t.id === palette) ? palette : "glass" };
}

// The backdrop's set of pictures for a process (backdrop/pictures.ts SETS).
export function setOf(process: PictureProcess): PresetId {
  return process === "halftone" ? "riso" : process === "stitch" ? "stitch" : "glass";
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
