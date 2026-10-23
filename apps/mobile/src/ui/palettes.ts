// The desktop app's theme catalog (desktop-shell/src/renderer/themes.ts and
// the [data-palette] blocks of styles.css), resolved to the plain colours
// React Native needs. Keep the values in step with those files.

export type Mode = "light" | "dark";

export interface Tokens {
  mode: Mode;
  background: string;
  surface: string;
  elevated: string;
  popover: string;
  border: string;
  input: string;
  text: string;
  secondary: string;
  muted: string;
  accent: string;
  accentInk: string;
  warning: string;
  danger: string;
  success: string;
  info: string;
  code: string;
  diffAdd: string;
  diffAddFg: string;
  diffDel: string;
  diffDelFg: string;
}

export interface ThemeInfo {
  id: string;
  label: string;
  // null follows the phone's light or dark setting, as the desktop default does.
  mode: Mode | null;
}

export const THEMES: readonly ThemeInfo[] = [
  { id: "default", label: "Deck (default)", mode: null },
  { id: "tokyo-night", label: "Tokyo Night", mode: "dark" },
  { id: "catppuccin", label: "Catppuccin Mocha", mode: "dark" },
  { id: "nord", label: "Nord", mode: "dark" },
  { id: "rose-pine", label: "Rosé Pine", mode: "dark" },
  { id: "dracula", label: "Dracula", mode: "dark" },
  { id: "solarized-light", label: "Solarized Light", mode: "light" },
  { id: "github-light", label: "GitHub Light", mode: "light" },
];

export interface AccentInfo {
  id: string;
  label: string;
  // [lightness, chroma, hue] as the desktop writes them in oklch().
  oklch: readonly [number, number, number] | null;
}

export const ACCENTS: readonly AccentInfo[] = [
  { id: "theme", label: "Theme", oklch: null },
  { id: "blue", label: "Blue", oklch: [0.571, 0.21, 264] },
  { id: "violet", label: "Violet", oklch: [0.58, 0.22, 295] },
  { id: "green", label: "Green", oklch: [0.62, 0.16, 155] },
  { id: "orange", label: "Orange", oklch: [0.68, 0.17, 50] },
  { id: "pink", label: "Pink", oklch: [0.64, 0.2, 350] },
];

export function themeInfo(id: string): ThemeInfo {
  return THEMES.find((t) => t.id === id) ?? (THEMES[0] as ThemeInfo);
}

export function accentInfo(id: string): AccentInfo {
  return ACCENTS.find((a) => a.id === id) ?? (ACCENTS[0] as AccentInfo);
}

const byte = (v: number): string =>
  Math.round(Math.min(1, Math.max(0, v)) * 255)
    .toString(16)
    .padStart(2, "0");

// Hermes has no oklch(), so the accents the desktop spells that way are
// converted to sRGB here (Björn Ottosson's Oklab matrices).
export function oklchToHex(l: number, c: number, h: number): string {
  const hue = (h * Math.PI) / 180;
  const a = c * Math.cos(hue);
  const b = c * Math.sin(hue);
  const l3 = (l + 0.3963377774 * a + 0.2158037573 * b) ** 3;
  const m3 = (l - 0.1055613458 * a - 0.0638541728 * b) ** 3;
  const s3 = (l - 0.0894841775 * a - 1.291485548 * b) ** 3;
  const linear = [
    4.0767416621 * l3 - 3.3077115913 * m3 + 0.2309699292 * s3,
    -1.2684380046 * l3 + 2.6097574011 * m3 - 0.3413193965 * s3,
    -0.0041960863 * l3 - 0.7034186147 * m3 + 1.707614701 * s3,
  ] as const;
  const [r, g, bl] = linear.map((v) => {
    const clamped = Math.min(1, Math.max(0, v));
    return clamped <= 0.0031308 ? 12.92 * clamped : 1.055 * clamped ** (1 / 2.4) - 0.055;
  }) as [number, number, number];
  return `#${byte(r)}${byte(g)}${byte(bl)}`;
}

// The share of `into` in the result, as CSS color-mix(in srgb) does.
export function mix(from: string, into: string, share: number): string {
  const channel = (hex: string, at: number): number => Number.parseInt(hex.slice(at, at + 2), 16);
  const out = [1, 3, 5].map((at) => {
    const v = channel(from, at) * (1 - share) + channel(into, at) * share;
    return Math.round(v).toString(16).padStart(2, "0");
  });
  return `#${out.join("")}`;
}

type Raw = Omit<Tokens, "mode">;

// Dark is the desktop's default; light is its [data-mode="light"] block.
const DARK: Raw = {
  background: "#0a0a0a",
  surface: mix("#0a0a0a", "#ffffff", 0.04),
  elevated: "rgba(255,255,255,0.07)",
  popover: mix("#0a0a0a", "#ffffff", 0.06),
  border: "rgba(255,255,255,0.08)",
  input: "rgba(255,255,255,0.10)",
  text: "#ededed",
  secondary: "#9a9a9a",
  muted: "#8a8a8a",
  accent: oklchToHex(0.571, 0.21, 264),
  accentInk: "#ffffff",
  warning: "#f59e0b",
  danger: "#f87171",
  success: "#34d399",
  info: "#60a5fa",
  code: "rgba(255,255,255,0.04)",
  diffAdd: "rgba(16,185,129,0.12)",
  diffAddFg: "#6ee7b7",
  diffDel: "rgba(239,68,68,0.12)",
  diffDelFg: "#fca5a5",
};

const LIGHT: Raw = {
  background: "#fcfcfc",
  surface: "#ffffff",
  elevated: "#f0f0f2",
  popover: "#ffffff",
  border: "#e4e4e7",
  input: "#d4d4d8",
  text: "#27272a",
  secondary: "#71717a",
  muted: "#71717a",
  accent: oklchToHex(0.488, 0.217, 264),
  accentInk: "#ffffff",
  // The desktop keeps amber text in light mode; its darker foreground stays legible on white.
  warning: "#b45309",
  danger: "#b91c1c",
  success: "#047857",
  info: "#1d4ed8",
  code: "#f4f4f5",
  diffAdd: "#e7f8ef",
  diffAddFg: "#047857",
  diffDel: "#fdecec",
  diffDelFg: "#b91c1c",
};

const PALETTES: Record<string, Partial<Raw>> = {
  "tokyo-night": {
    background: "#1a1b26",
    text: "#c0caf5",
    surface: "#1f2133",
    popover: "#24283b",
    accent: "#7aa2f7",
    accentInk: "#1a1b26",
    secondary: "#8089b3",
    muted: "#737aa2",
    border: "rgba(169,177,214,0.12)",
    input: "rgba(169,177,214,0.16)",
    elevated: "#292e42",
    code: "#16161e",
  },
  catppuccin: {
    background: "#1e1e2e",
    text: "#cdd6f4",
    surface: "#232336",
    popover: "#313244",
    accent: "#cba6f7",
    accentInk: "#1e1e2e",
    secondary: "#a6adc8",
    muted: "#9399b2",
    border: "rgba(205,214,244,0.10)",
    input: "rgba(205,214,244,0.14)",
    elevated: "#313244",
    code: "#181825",
  },
  nord: {
    background: "#2e3440",
    text: "#eceff4",
    surface: "#333a47",
    popover: "#3b4252",
    accent: "#88c0d0",
    accentInk: "#2e3440",
    secondary: "#a7b1c2",
    muted: "#9aa5b8",
    border: "rgba(236,239,244,0.10)",
    input: "rgba(236,239,244,0.15)",
    elevated: "#3b4252",
    code: "#292e39",
  },
  "rose-pine": {
    background: "#191724",
    text: "#e0def4",
    surface: "#1f1d2e",
    popover: "#26233a",
    accent: "#ebbcba",
    accentInk: "#191724",
    secondary: "#908caa",
    muted: "#817c9c",
    border: "rgba(224,222,244,0.09)",
    input: "rgba(224,222,244,0.14)",
    elevated: "#26233a",
    code: "#16141f",
  },
  dracula: {
    background: "#282a36",
    text: "#f8f8f2",
    surface: "#2d2f3d",
    popover: "#343746",
    accent: "#bd93f9",
    accentInk: "#282a36",
    secondary: "#a4a8c4",
    muted: "#9599b8",
    border: "rgba(248,248,242,0.09)",
    input: "rgba(248,248,242,0.14)",
    elevated: "#3a3d4e",
    code: "#21222c",
  },
  "solarized-light": {
    background: "#fdf6e3",
    text: "#3b4c52",
    surface: "#fffbf0",
    popover: "#fffbf0",
    accent: "#268bd2",
    accentInk: "#fdf6e3",
    secondary: "#657b83",
    muted: "#74888f",
    border: "#e4dcc6",
    input: "#d6cfba",
    elevated: "#eee8d5",
    code: "#eee8d5",
  },
  "github-light": {
    background: "#ffffff",
    text: "#1f2328",
    surface: "#ffffff",
    popover: "#ffffff",
    accent: "#0969da",
    accentInk: "#ffffff",
    secondary: "#59636e",
    muted: "#656d76",
    border: "#d1d9e0",
    input: "#c4cdd5",
    elevated: "#f6f8fa",
    code: "#f6f8fa",
  },
};

export interface AppearancePrefs {
  theme: string;
  accent: string;
}

export const DEFAULT_APPEARANCE: AppearancePrefs = { theme: "default", accent: "theme" };

export function resolveTokens(prefs: AppearancePrefs, system: Mode): Tokens {
  const mode = themeInfo(prefs.theme).mode ?? system;
  const raw: Raw = { ...(mode === "dark" ? DARK : LIGHT), ...PALETTES[prefs.theme] };
  const accent = accentInfo(prefs.accent).oklch;
  if (accent !== null) {
    raw.accent = oklchToHex(accent[0], accent[1], accent[2]);
    raw.accentInk = "#ffffff";
  }
  return { mode, ...raw };
}
