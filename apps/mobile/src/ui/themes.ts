// The phone wears the same themes as the Deck CLI (internal/ui/styles) and
// the desktop app (desktop-shell styles.css). Each palette is the handful of
// colors the app needs; every surface is derived from it so depth looks the
// same in every theme.

export type ThemeId =
  | "tabletop"
  | "tabletop-light"
  | "deck"
  | "deck-light"
  | "t3"
  | "tokyo-night"
  | "catppuccin-mocha"
  | "catppuccin-latte"
  | "nord"
  | "rose-pine"
  | "dracula"
  | "gruvbox-dark"
  | "solarized-light"
  | "github-light";

export interface Palette {
  id: ThemeId;
  name: string;
  source: "CLI" | "Desktop" | "Both";
  dark: boolean;
  bg: string;
  raise: string;
  fg: string;
  muted: string;
  primary: string;
  onPrimary: string;
  warn: string;
  danger: string;
  success: string;
  info: string;
  // Solid buttons. Tabletop spends its amber on state and the send button and
  // makes solid buttons paper; every other theme uses its primary.
  action?: string;
  onAction?: string;
}

// Some status and grey values are nudged from the source themes so text
// stays readable on a phone's smaller type.
export const PALETTES: readonly Palette[] = [
  {
    id: "tabletop",
    name: "Tabletop",
    source: "Desktop",
    dark: true,
    bg: "#0B0A08",
    raise: "#12100E",
    fg: "#EBE7E2",
    muted: "#A9A49C",
    primary: "#F2AF48",
    onPrimary: "#1F1306",
    warn: "#F59740",
    danger: "#E95048",
    success: "#5CB572",
    info: "#70B4DC",
    action: "#EBE7E2",
    onAction: "#0B0A08",
  },
  {
    id: "tabletop-light",
    name: "Tabletop Light",
    source: "Desktop",
    dark: false,
    bg: "#F9F6F2",
    raise: "#FDFBF9",
    fg: "#1E1A16",
    muted: "#59544E",
    primary: "#ED9E2F",
    onPrimary: "#1F1306",
    warn: "#AB5900",
    danger: "#BE2323",
    success: "#21763C",
    info: "#287AA3",
    action: "#1E1A16",
    onAction: "#F9F6F2",
  },
  {
    id: "deck",
    name: "Deck",
    source: "CLI",
    dark: true,
    bg: "#0E1211",
    raise: "#151B1A",
    fg: "#E6EDEB",
    muted: "#9AA5A2",
    primary: "#5FD7AF",
    onPrimary: "#0E1211",
    warn: "#E8B04B",
    danger: "#F0716A",
    success: "#9CCB8A",
    info: "#7FB8FF",
  },
  {
    id: "deck-light",
    name: "Deck Light",
    source: "CLI",
    dark: false,
    bg: "#F0F4F2",
    raise: "#FFFFFF",
    fg: "#1B1F1E",
    muted: "#5C6664",
    primary: "#0D7A62",
    onPrimary: "#FFFFFF",
    warn: "#8F5A00",
    danger: "#B3261E",
    success: "#3D7A3A",
    info: "#2F6FB8",
  },
  {
    id: "t3",
    name: "T3",
    source: "Desktop",
    dark: true,
    bg: "#0A0A0A",
    raise: "#151515",
    fg: "#EDEDED",
    muted: "#9A9A9A",
    primary: "#3B82F6",
    onPrimary: "#FFFFFF",
    warn: "#F5A524",
    danger: "#F87171",
    success: "#4ADE80",
    info: "#60A5FA",
  },
  {
    id: "tokyo-night",
    name: "Tokyo Night",
    source: "Both",
    dark: true,
    bg: "#1A1B26",
    raise: "#1F2133",
    fg: "#C0CAF5",
    muted: "#8C95BF",
    primary: "#7AA2F7",
    onPrimary: "#1A1B26",
    warn: "#E0AF68",
    danger: "#F7768E",
    success: "#9ECE6A",
    info: "#7DCFFF",
  },
  {
    id: "catppuccin-mocha",
    name: "Catppuccin Mocha",
    source: "Both",
    dark: true,
    bg: "#1E1E2E",
    raise: "#25253A",
    fg: "#CDD6F4",
    muted: "#A6ADC8",
    primary: "#CBA6F7",
    onPrimary: "#1E1E2E",
    warn: "#F9E2AF",
    danger: "#F38BA8",
    success: "#A6E3A1",
    info: "#89B4FA",
  },
  {
    id: "catppuccin-latte",
    name: "Catppuccin Latte",
    source: "CLI",
    dark: false,
    bg: "#EFF1F5",
    raise: "#FBFCFD",
    fg: "#3F4259",
    muted: "#5F6279",
    primary: "#8839EF",
    onPrimary: "#FFFFFF",
    warn: "#A35A06",
    danger: "#D20F39",
    success: "#40A02B",
    info: "#1E66F5",
  },
  {
    id: "nord",
    name: "Nord",
    source: "Both",
    dark: true,
    bg: "#2E3440",
    raise: "#353C4A",
    fg: "#ECEFF4",
    muted: "#B4BDCD",
    primary: "#88C0D0",
    onPrimary: "#2E3440",
    warn: "#EBCB8B",
    danger: "#E07A84",
    success: "#A3BE8C",
    info: "#81A1C1",
  },
  {
    id: "rose-pine",
    name: "Rosé Pine",
    source: "Both",
    dark: true,
    bg: "#191724",
    raise: "#1F1D2E",
    fg: "#E0DEF4",
    muted: "#9C98B8",
    primary: "#EBBCBA",
    onPrimary: "#191724",
    warn: "#F6C177",
    danger: "#EB6F92",
    success: "#9CCFD8",
    info: "#C4A7E7",
  },
  {
    id: "dracula",
    name: "Dracula",
    source: "Both",
    dark: true,
    bg: "#282A36",
    raise: "#2F3241",
    fg: "#F8F8F2",
    muted: "#ADB5CB",
    primary: "#BD93F9",
    onPrimary: "#282A36",
    warn: "#F1FA8C",
    danger: "#FF6E6E",
    success: "#50FA7B",
    info: "#8BE9FD",
  },
  {
    id: "gruvbox-dark",
    name: "Gruvbox Dark",
    source: "CLI",
    dark: true,
    bg: "#282828",
    raise: "#32302F",
    fg: "#EBDBB2",
    muted: "#BDAE93",
    primary: "#FABD2F",
    onPrimary: "#282828",
    warn: "#FE8019",
    danger: "#FB4934",
    success: "#B8BB26",
    info: "#83A598",
  },
  {
    id: "solarized-light",
    name: "Solarized Light",
    source: "Desktop",
    dark: false,
    bg: "#FDF6E3",
    raise: "#FFFCF4",
    fg: "#3B4C52",
    muted: "#586E75",
    primary: "#1F78B8",
    onPrimary: "#FFFFFF",
    warn: "#8A6800",
    danger: "#C42B28",
    success: "#5F6E00",
    info: "#1F78B8",
  },
  {
    id: "github-light",
    name: "GitHub Light",
    source: "Desktop",
    dark: false,
    bg: "#F6F8FA",
    raise: "#FFFFFF",
    fg: "#1F2328",
    muted: "#59636E",
    primary: "#0969DA",
    onPrimary: "#FFFFFF",
    warn: "#9A6700",
    danger: "#D1242F",
    success: "#1A7F37",
    info: "#0969DA",
  },
];

export const DEFAULT_DARK: ThemeId = "tabletop";
export const DEFAULT_LIGHT: ThemeId = "tabletop-light";

export function isThemeId(value: unknown): value is ThemeId {
  return typeof value === "string" && PALETTES.some((p) => p.id === value);
}

export function palette(id: ThemeId): Palette {
  return PALETTES.find((p) => p.id === id) ?? (PALETTES[0] as Palette);
}

function channels(hex: string): [number, number, number] {
  const n = Number.parseInt(hex.slice(1), 16);
  return [(n >> 16) & 255, (n >> 8) & 255, n & 255];
}

export function rgba(hex: string, alpha: number): string {
  const [r, g, b] = channels(hex);
  return `rgba(${r},${g},${b},${alpha})`;
}

export function mix(from: string, to: string, amount: number): string {
  const a = channels(from);
  const b = channels(to);
  return `#${a
    .map((v, i) =>
      Math.round(v + ((b[i] as number) - v) * amount)
        .toString(16)
        .padStart(2, "0"),
    )
    .join("")}`;
}

function luminance(hex: string): number {
  const [r, g, b] = channels(hex).map((v) => {
    const c = v / 255;
    return c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4;
  }) as [number, number, number];
  return 0.2126 * r + 0.7152 * g + 0.0722 * b;
}

export function contrast(a: string, b: string): number {
  const [hi, lo] = [luminance(a), luminance(b)].sort((x, y) => y - x) as [number, number];
  return (hi + 0.05) / (lo + 0.05);
}

// Theme is what screens read: the palette plus derived surfaces. Dark themes
// lift surfaces toward the text color; light themes put white-ish cards on a
// tinted page. Shadows are CSS box-shadow strings (React Native's new
// architecture renders them on both platforms).
export interface Theme {
  id: ThemeId;
  name: string;
  dark: boolean;
  bg: string;
  card: string;
  sheet: string;
  chip: string;
  track: string;
  thumb: string;
  code: string;
  line: string;
  fg: string;
  soft: string;
  muted: string;
  primary: string;
  onPrimary: string;
  action: string;
  onAction: string;
  warn: string;
  danger: string;
  success: string;
  info: string;
  scrim: string;
  shadow: { card: string; float: string; sheet: string; thumb: string; primary: string };
  tint(color: string): string;
}

export function buildTheme(p: Palette): Theme {
  const dk = p.dark;
  const line = rgba(p.fg, dk ? 0.08 : 0.09);
  return {
    id: p.id,
    name: p.name,
    dark: dk,
    bg: p.bg,
    card: dk ? mix(p.bg, p.fg, 0.055) : p.raise,
    sheet: dk ? mix(p.bg, p.fg, 0.07) : p.raise,
    chip: dk ? mix(p.bg, p.fg, 0.1) : mix(p.bg, p.fg, 0.05),
    track: dk ? mix(p.bg, p.fg, 0.03) : mix(p.bg, p.fg, 0.045),
    thumb: dk ? mix(p.bg, p.fg, 0.14) : p.raise,
    code: dk ? mix(p.bg, "#000000", 0.25) : mix(p.bg, p.fg, 0.035),
    line,
    fg: p.fg,
    soft: dk ? mix(p.fg, p.bg, 0.08) : p.fg,
    muted: p.muted,
    primary: p.primary,
    onPrimary: p.onPrimary,
    action: p.action ?? p.primary,
    onAction: p.onAction ?? p.onPrimary,
    warn: p.warn,
    danger: p.danger,
    success: p.success,
    info: p.info,
    scrim: dk ? "rgba(0,0,0,0.6)" : "rgba(15,20,25,0.38)",
    // Flat like the desktop app: a hairline ring and a 1px top highlight on
    // raised surfaces; only floating bars and sheets cast a shadow.
    shadow: dk
      ? {
          card: `inset 0 1px 0 ${rgba("#FFFFFF", 0.05)}, 0 0 0 1px ${line}`,
          float: `inset 0 1px 0 ${rgba("#FFFFFF", 0.06)}, 0 0 0 1px ${rgba(p.fg, 0.12)}, 0 16px 40px -16px rgba(0,0,0,0.55)`,
          sheet: `inset 0 1px 0 ${rgba("#FFFFFF", 0.07)}, 0 -16px 40px -16px rgba(0,0,0,0.6)`,
          thumb: `inset 0 1px 0 ${rgba("#FFFFFF", 0.06)}, 0 0 0 1px ${line}`,
          primary: `0 0 0 1px ${rgba(p.primary, 0.4)}`,
        }
      : {
          card: `0 0 0 1px ${line}`,
          float: `0 0 0 1px ${rgba(p.fg, 0.15)}, 0 16px 40px -16px rgba(16,24,40,0.25)`,
          sheet: "0 -12px 36px -12px rgba(16,24,40,0.22)",
          thumb: `0 0 0 1px ${line}`,
          primary: `0 0 0 1px ${rgba(p.primary, 0.4)}`,
        },
    tint: (color: string) => rgba(color, dk ? 0.16 : 0.12),
  };
}
