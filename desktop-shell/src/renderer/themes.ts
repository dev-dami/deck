// The window's themes, after T3 Code's theme catalog. "tabletop" is Deck's
// own look and the default; "default" is the T3 look. Both follow light or
// dark; every other theme has a mode of its own. Each theme's tokens are in
// styles.css under [data-palette="<id>"].

export interface ThemeInfo {
  id: string;
  label: string;
  mode: "light" | "dark" | null;
  // The swatches the picker shows, as CSS colours: background, sidebar or
  // surface, text, accent.
  swatch: [string, string, string, string];
}

export const THEMES: readonly ThemeInfo[] = [
  {
    id: "tabletop",
    label: "Tabletop",
    mode: null,
    swatch: ["oklch(0.145 0.005 70)", "oklch(0.12 0.005 70)", "oklch(0.93 0.008 80)", "oklch(0.8 0.14 75)"],
  },
  { id: "default", label: "T3", mode: null, swatch: ["#0a0a0a", "#1a1a1a", "#ededed", "#3b82f6"] },
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
];

export function themeInfo(id: string): ThemeInfo {
  return THEMES.find((t) => t.id === id) ?? (THEMES[0] as ThemeInfo);
}
