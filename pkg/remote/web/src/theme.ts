import type { Theme } from "./protocol";

// R6: every colour comes from welcome.server.theme. The palette tokens of
// internal/ui/styles become `--p-<token>` custom properties (underscores to
// dashes) and each agent kind's tint becomes `--kind-<kind>`; style.css reads
// only those, with neutral greys as fallbacks for the moments before welcome.

const TOKEN_RE = /^[a-z0-9_]+$/;

export function cssVarName(token: string): string | null {
  return TOKEN_RE.test(token) ? `--p-${token.replaceAll("_", "-")}` : null;
}

// Kinds are arbitrary strings from the server; only a safe subset makes a
// custom property name.
export function kindVarName(kind: string): string | null {
  const safe = kind
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-|-$/g, "");
  return safe === "" ? null : `--kind-${safe}`;
}

const HEX_RE = /^#[0-9a-fA-F]{6}$/;

export function isHexColour(v: string): boolean {
  return HEX_RE.test(v);
}

// The custom properties a theme sets; values that are not #rrggbb are left
// out so nothing from the wire reaches the CSSOM unchecked.
export function cssVariables(theme: Theme): Record<string, string> {
  const out: Record<string, string> = {};
  for (const [token, hex] of Object.entries(theme.palette)) {
    const name = cssVarName(token);
    if (name !== null && isHexColour(hex)) out[name] = hex.toLowerCase();
  }
  for (const [kind, tone] of Object.entries(theme.kinds)) {
    const name = kindVarName(kind);
    if (name !== null && isHexColour(tone.tint)) out[name] = tone.tint.toLowerCase();
  }
  return out;
}

// Relative luminance of #rrggbb, 0 (black) to 1 (white).
export function luminance(hex: string): number {
  const n = Number.parseInt(hex.slice(1), 16);
  const lin = (c: number) => {
    const s = c / 255;
    return s <= 0.03928 ? s / 12.92 : ((s + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * lin((n >> 16) & 255) + 0.7152 * lin((n >> 8) & 255) + 0.0722 * lin(n & 255);
}

export type Scheme = "light" | "dark";

// The theme is dark when its base background is.
export function schemeOf(theme: Theme): Scheme | null {
  const bg = theme.palette["bg_base"];
  if (bg === undefined || !isHexColour(bg)) return null;
  return luminance(bg) < 0.4 ? "dark" : "light";
}

export interface ThemeTarget {
  setProperty(name: string, value: string): void;
  removeProperty(name: string): void;
  setScheme(scheme: Scheme | null): void;
  setThemeColor(hex: string | null): void;
}

// Applies a theme and returns the names it set, so a later theme.changed can
// clear tokens the new one no longer carries.
export function applyTheme(target: ThemeTarget, theme: Theme, previous: string[]): string[] {
  const vars = cssVariables(theme);
  for (const name of previous) if (!(name in vars)) target.removeProperty(name);
  for (const [name, value] of Object.entries(vars)) target.setProperty(name, value);
  target.setScheme(schemeOf(theme));
  const bg = theme.palette["bg_base"];
  target.setThemeColor(bg !== undefined && isHexColour(bg) ? bg.toLowerCase() : null);
  return Object.keys(vars);
}

export function documentTarget(doc: Document): ThemeTarget {
  const root = doc.documentElement;
  return {
    setProperty: (n, v) => root.style.setProperty(n, v),
    removeProperty: (n) => root.style.removeProperty(n),
    setScheme: (s) => {
      if (s === null) root.removeAttribute("data-scheme");
      else root.setAttribute("data-scheme", s);
    },
    setThemeColor: (hex) => {
      for (const el of doc.querySelectorAll('meta[name="theme-color"]')) {
        if (hex === null) el.removeAttribute("content");
        else el.setAttribute("content", hex);
      }
    },
  };
}

export function kindMark(theme: Theme | null, kind: string): string {
  return theme?.kinds[kind]?.mark ?? "";
}

// The CSS colour expression for an agent kind: its tint, or the base text
// colour for a kind the theme does not name.
export function kindColour(kind: string): string {
  const name = kindVarName(kind);
  return name === null ? "var(--p-fg-base)" : `var(${name}, var(--p-fg-base))`;
}
