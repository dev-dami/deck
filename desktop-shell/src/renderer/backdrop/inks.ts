// Each theme's art direction, as the backdrop draws it: which process the
// picture goes through and the inks, threads or sky it is drawn in. The
// tokens the components read live in styles.css; these are the colours of
// the picture behind them, kept in step by hand (design-glass.md, "Art
// directions").

import { hex, mix, type RGB } from "./core";

export type Process = "scene" | "halftone" | "stitch";
export type Mode = "light" | "dark";

// Liquid Glass: a sky at dusk (or dawn, in light), soft ridges in haze, and
// the Deck mark huge and faint across it.
export interface SceneArt {
  process: "scene";
  ground: RGB;
  // Top to horizon, at fractions of the height.
  sky: [number, RGB][];
  // Clouds: lit underside, shaded top.
  cloudLit: RGB;
  cloudShade: RGB;
  // Ridges, farthest first.
  ridges: RGB[];
  mark: RGB;
  markAlpha: number;
  // Over a wallpaper: a veil that keeps glass legible.
  veil: RGB;
  veilAlpha: number;
  grain: number;
}

// Riso: two inks screened on paper, or white and pink on black stock.
export interface HalftoneArt {
  process: "halftone";
  ground: RGB; // the stock
  key: RGB; // black ink on paper, white ink on black
  spot: RGB; // fluorescent pink
  onBlack: boolean;
  // The picture the screens separate when there is no wallpaper.
  source: SceneArt;
}

// Stitch: cross-stitch on even-weave cloth.
export interface StitchArt {
  process: "stitch";
  ground: RGB; // the cloth
  cloth: RGB;
  hole: RGB;
  twill: boolean; // denim's diagonal, in dark
  // Thread per role (core.ts T): card, ink, red, red dark, edge, shadow,
  // die red, cream.
  roles: RGB[];
  // The floss box a wallpaper is charted in.
  floss: RGB[];
}

export type Art = SceneArt | HalftoneArt | StitchArt;

export const PINK = hex("#ff48b0");

const DUSK: SceneArt = {
  process: "scene",
  ground: hex("#0b0e1a"),
  sky: [
    [0, hex("#05070f")],
    [0.3, hex("#0b1024")],
    [0.52, hex("#191b3a")],
    [0.66, hex("#33284d")],
    [0.75, hex("#64405f")],
    [0.82, hex("#a3626c")],
    [0.87, hex("#d18c78")],
  ],
  cloudLit: hex("#c8787a"),
  cloudShade: hex("#2a2546"),
  ridges: [hex("#5a4560"), hex("#2c2542"), hex("#141429"), hex("#07080f")],
  mark: hex("#ffffff"),
  markAlpha: 0.055,
  veil: hex("#05060c"),
  veilAlpha: 0.32,
  grain: 0.05,
};

const DAWN: SceneArt = {
  process: "scene",
  ground: hex("#cfd5e3"),
  sky: [
    [0, hex("#7d93bd")],
    [0.3, hex("#a3b3d3")],
    [0.55, hex("#c9cde2")],
    [0.7, hex("#e2d4de")],
    [0.8, hex("#f2d9d3")],
    [0.87, hex("#f8e2cf")],
  ],
  cloudLit: hex("#fbe6dc"),
  cloudShade: hex("#aab3cf"),
  ridges: [hex("#d9c9d6"), hex("#b8b8cf"), hex("#959cbb"), hex("#727c9f")],
  mark: hex("#ffffff"),
  markAlpha: 0.2,
  veil: hex("#f4f5f9"),
  veilAlpha: 0.12,
  grain: 0.045,
};

// The riso prints the same dusk; its sky is pushed toward the pink so both
// plates have something to say.
const RISO_SOURCE: SceneArt = {
  ...DUSK,
  sky: [
    [0, hex("#000000")],
    [0.3, hex("#0b0b10")],
    [0.5, hex("#2a1830")],
    [0.64, hex("#7a2a62")],
    [0.75, hex("#d0508f")],
    [0.83, hex("#f59ab8")],
    [0.88, hex("#ffe9e0")],
  ],
  cloudLit: hex("#ff8fc0"),
  cloudShade: hex("#120a14"),
  ridges: [hex("#b04a86"), hex("#4a1c40"), hex("#160a14"), hex("#000000")],
};

const RISO_SOURCE_LIGHT: SceneArt = {
  ...RISO_SOURCE,
  sky: [
    [0, hex("#ffffff")],
    [0.3, hex("#fbeef3")],
    [0.52, hex("#f6c3da")],
    [0.68, hex("#ec80b6")],
    [0.8, hex("#c85a95")],
    [0.88, hex("#ffffff")],
  ],
  cloudLit: hex("#ffffff"),
  cloudShade: hex("#f3a6cb"),
  ridges: [hex("#9a6a86"), hex("#5c3a50"), hex("#33232e"), hex("#151214")],
};

// DMC-like floss: the colours a small pattern kit would carry.
const FLOSS = [
  "#f4f1e8", // blanc
  "#e9dcc0", // ecru
  "#d3c3a3", // light tan
  "#a98d68", // tan
  "#6d5038", // brown
  "#3a2a20", // dark brown
  "#1f1d1c", // black 310
  "#5b5e64", // pewter
  "#a5a8ab", // grey
  "#b8262d", // red 321
  "#7d1a22", // garnet
  "#d7703a", // rust
  "#e4b04a", // mustard
  "#f0a5b8", // pink
  "#8e4f7a", // plum
  "#283a6b", // navy
  "#4f74ad", // blue
  "#9fbad9", // sky
  "#2f5d45", // green
  "#8aa57c", // sage
].map(hex);

const LINEN: StitchArt = {
  process: "stitch",
  ground: hex("#d4c09c"),
  cloth: hex("#d4c09c"),
  hole: hex("#a28c66"),
  twill: false,
  roles: ["#f6f2e8", "#25211e", "#b8262d", "#7d1a22", "#8d877d", "#bba67f", "#c6302c", "#f1e4c4"].map(hex),
  floss: FLOSS,
};

const DENIM: StitchArt = {
  process: "stitch",
  ground: hex("#1b2240"),
  cloth: hex("#1d2546"),
  hole: hex("#0d1126"),
  twill: true,
  roles: ["#efe9da", "#16151a", "#c22f33", "#7d1a22", "#9a958c", "#131a33", "#d0392f", "#e8d8b4"].map(hex),
  floss: FLOSS,
};

export const PROCESS: Record<string, Process> = { riso: "halftone", stitch: "stitch" };

export function processOf(theme: string): Process {
  return PROCESS[theme] ?? "scene";
}

// A colourway of Liquid Glass (Apple, Tabletop, T3 and the catalog): the
// same sky, drawn from its own background and accent.
export function colourway(mode: Mode, background: RGB, accent: RGB, text: RGB): SceneArt {
  const dark = mode === "dark";
  const black: RGB = [0, 0, 0];
  const top = dark ? mix(background, black, 0.55) : mix(background, accent, 0.18);
  const horizon = mix(background, accent, dark ? 0.32 : 0.22);
  return {
    process: "scene",
    ground: dark ? mix(background, black, 0.4) : mix(background, black, 0.06),
    sky: [
      [0, top],
      [0.45, dark ? mix(background, black, 0.3) : mix(background, accent, 0.08)],
      [0.72, mix(background, accent, dark ? 0.14 : 0.12)],
      [0.86, horizon],
    ],
    cloudLit: mix(horizon, text, 0.2),
    cloudShade: dark ? mix(background, black, 0.25) : mix(background, accent, 0.2),
    ridges: [0.2, 0.4, 0.6, 0.8].map((t) => (dark ? mix(horizon, mix(background, black, 0.7), t) : mix(horizon, mix(background, accent, 0.5), t * 0.8))),
    mark: dark ? text : [255, 255, 255],
    markAlpha: dark ? 0.045 : 0.18,
    veil: dark ? black : [255, 255, 255],
    veilAlpha: dark ? 0.3 : 0.12,
    grain: 0.04,
  };
}

// The art of a theme in a mode. `swatch` resolves a colourway's colours
// (themes.ts swatches are CSS colours; the renderer parses them).
export function artFor(theme: string, mode: Mode, swatch?: { background: RGB; accent: RGB; text: RGB }): Art {
  if (theme === "riso") {
    return mode === "dark"
      ? { process: "halftone", ground: hex("#0e0d0d"), key: hex("#f3efe6"), spot: PINK, onBlack: true, source: RISO_SOURCE }
      : { process: "halftone", ground: hex("#f2ede2"), key: hex("#1c1a1a"), spot: PINK, onBlack: false, source: RISO_SOURCE_LIGHT };
  }
  if (theme === "stitch") return mode === "dark" ? DENIM : LINEN;
  if (theme === "glass" || swatch === undefined) return mode === "dark" ? DUSK : DAWN;
  return colourway(mode, swatch.background, swatch.accent, swatch.text);
}
