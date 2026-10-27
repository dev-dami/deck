// Each theme's art direction, as the backdrop prints it: the process the
// picture goes through and the inks or threads it is printed in. The tokens
// the components read live in styles.css; these are the colours of the
// picture behind them, kept in step by hand (design-glass.md, "Art
// directions").

import type { PictureProcess } from "../themes";
import { hex, mix, type RGB } from "./core";

export type Process = "scene" | "halftone" | "stitch";
export type Mode = "light" | "dark";

// Liquid Glass and its colourways: the picture dithered (Atkinson) into
// two inks at `cell` px, or into a few inks when `colour`, with the Deck
// mark lifted into the tone as a watermark.
export interface SceneArt {
  process: "scene";
  picture: string;
  ground: RGB;
  // Duotone: shadow ink, then highlight ink. Colour: the whole set.
  inks: RGB[];
  colour: boolean;
  cell: number;
  // Bends the mid tones before the dither (below 1 lightens).
  gamma: number;
  // The sky's grade at the top of the frame, 0..1: toward the shadow ink
  // when positive, toward the light ink when negative.
  sky: number;
  // The watermark: how far the inks under the mark move toward each other,
  // as paper is thinner under a watermark and every tone in it is softer.
  mark: number;
  // A band at the window's edge, in CSS px, printed in the ground alone, so
  // the gutters round the panels are calm; a print has none.
  margin: number;
  // How far the band behind the sidebar is held back, as the sky is, 0..1.
  side: number;
}

// Riso: one fluorescent spot ink screened at 15° by tone, solid blocks of
// the same ink, and black where the stock is paper.
export interface HalftoneArt {
  process: "halftone";
  picture: string;
  ground: RGB; // the stock
  spot: RGB;
  key: RGB; // black ink; on black stock it never prints
  onBlack: boolean;
  pitch: number;
  // The press's margin in CSS px: a riso cannot print to the sheet's edge,
  // and the window's gutters, which this edge shows through, stay stock.
  margin: number;
}

// Stitch: cross-stitch on even-weave cloth.
export interface StitchArt {
  process: "stitch";
  picture: string;
  cloth: RGB;
  hole: RGB;
  twill: boolean; // denim's diagonal, in dark
  // The picture's threads, darkest first; the lightest tones (darkest on
  // denim) stay bare cloth.
  threads: RGB[];
  bare: number;
  // A thread per role (core.ts T) for the cards and dice.
  roles: RGB[];
  // Deck's own cards and dice over the picture.
  table: boolean;
  // A band on the left, in CSS px, that the cards and dice keep off: the
  // sidebar and its gutter, behind the window; nothing, on a print.
  clear: number;
  cell: number;
}

export type Art = SceneArt | HalftoneArt | StitchArt;

export const SPOT = hex("#ff48b0");

const DUSK: SceneArt = {
  process: "scene",
  picture: "alps",
  ground: hex("#1d2240"),
  inks: [hex("#1d2240"), hex("#f3c9a8")],
  colour: false,
  cell: 2,
  gamma: 1.15,
  sky: 0.82,
  mark: 0.14,
  margin: 16,
  side: 0.55,
};

const DAWN: SceneArt = {
  ...DUSK,
  ground: hex("#f4e6da"),
  inks: [hex("#a4abcb"), hex("#f6e8dc")],
  gamma: 0.8,
  sky: -0.7,
  mark: 0.16,
};

const RISO_DARK: HalftoneArt = {
  process: "halftone",
  picture: "allium",
  ground: hex("#121111"),
  spot: SPOT,
  key: hex("#121111"),
  onBlack: true,
  pitch: 5,
  margin: 20,
};

const RISO_LIGHT: HalftoneArt = {
  ...RISO_DARK,
  ground: hex("#f3eee3"),
  key: hex("#1d1b1b"),
  onBlack: false,
};

// DMC-like floss for the picture: tonal, so Deck's cards stay the subject.
const LINEN: StitchArt = {
  process: "stitch",
  picture: "canyon",
  cloth: hex("#d9c8a7"),
  hole: hex("#9c8661"),
  twill: false,
  threads: ["#3a2f27", "#6f5a44", "#a48c69"].map(hex),
  bare: 0.48,
  roles: ["#f6f2e8", "#25211e", "#b8262d", "#7d1a22", "#8d877d", "#a8946f", "#c6302c", "#efe2c2"].map(hex),
  table: true,
  clear: 280,
  cell: 10,
};

const DENIM: StitchArt = {
  process: "stitch",
  picture: "canyon",
  cloth: hex("#26335c"),
  hole: hex("#111830"),
  twill: true,
  threads: ["#56709f", "#93a9d0", "#c9d5e8"].map(hex),
  bare: 0.38,
  roles: ["#efe9da", "#16151a", "#c22f33", "#7d1a22", "#9a958c", "#1a2546", "#d0392f", "#e8d8b4"].map(hex),
  table: true,
  clear: 280,
  cell: 10,
};

// Tokyo Night prints the Hubble Deep Field in its own colours.
const DEEP_FIELD: SceneArt = {
  process: "scene",
  picture: "xdf",
  ground: hex("#101119"),
  inks: ["#101119", "#1a1b26", "#7aa2f7", "#bb9af7", "#f7768e", "#e0af68", "#c0caf5"].map(hex),
  colour: true,
  cell: 2,
  gamma: 1,
  sky: 0.3,
  mark: 0.1,
  margin: 16,
  side: 0.55,
};

export const PROCESS: Record<string, Process> = { riso: "halftone", stitch: "stitch" };

// A theme's own process: the one its preset prints through.
export function processOf(theme: string): Process {
  return PROCESS[theme] ?? "scene";
}

// The process the person picked (themes.ts), as the backdrop names it.
// "none" prints nothing behind the window; a print on a page dithers.
export function artProcess(p: PictureProcess): Process {
  return p === "halftone" || p === "stitch" ? p : "scene";
}

// The colours a colourway is drawn from: the theme's tokens as RGB.
export interface Tokens {
  backdrop: RGB;
  canvas: RGB;
  primary: RGB;
  text: RGB;
}

// A colourway of Liquid Glass (Apple, Tabletop, T3 and the catalog): the
// Alps in two of its own colours. The shadow ink is the backdrop; the
// light ink is the text warmed by the accent, held back in dark so the
// glass over it stays quiet.
export function colourway(mode: Mode, t: Tokens): SceneArt {
  if (mode === "dark") {
    const lo = mix(t.backdrop, t.canvas, 0.5);
    const hi = mix(mix(t.text, t.primary, 0.35), lo, 0.28);
    return { ...DUSK, ground: lo, inks: [lo, hi] };
  }
  const hi = mix(t.canvas, t.primary, 0.05);
  const lo = mix(t.backdrop, t.primary, 0.3);
  return { ...DAWN, ground: hi, inks: [lo, hi] };
}

// Another theme's colours through the riso: its accent is the spot ink,
// on its backdrop as the stock; on paper its text is the black.
export function risoIn(mode: Mode, t: Tokens): HalftoneArt {
  return mode === "dark"
    ? { ...RISO_DARK, ground: t.backdrop, spot: t.primary, key: t.backdrop }
    : { ...RISO_LIGHT, ground: t.backdrop, spot: t.primary, key: t.text };
}

// Another theme's colours in floss: its backdrop is the cloth, and the
// picture's three threads step from it toward its text, lighter on a dark
// cloth and darker on a light one. The cards and dice keep their own.
export function stitchIn(mode: Mode, t: Tokens): StitchArt {
  const cloth = t.backdrop;
  if (mode === "dark") {
    return { ...DENIM, cloth, hole: mix(cloth, [0, 0, 0], 0.5), threads: [0.3, 0.55, 0.8].map((k) => mix(cloth, t.text, k)) };
  }
  return { ...LINEN, cloth, hole: mix(cloth, t.text, 0.35), threads: [0.85, 0.6, 0.35].map((k) => mix(cloth, t.text, k)) };
}

// Where the art is printed: behind the window (the backdrop) or as a print
// on a page (the Welcome's and the connect screen's picture, ArtFigure).
export type Use = "window" | "print";

// The art of a theme in a mode, through a process: the theme's own unless
// the person mixed another. A colourway, and a theme through a process not
// its own, needs the theme's tokens; without them the process prints in
// its own inks.
//
// As a print, Liquid Glass and its colourways hang the true duotone, dusk
// navy and peach, in either mode and with no sky held back (nothing sits
// on it); the riso prints to the edge of its box; the stitch works in a
// finer count, so a hand of cards fits a smaller cloth.
export function artFor(theme: string, mode: Mode, tokens?: Tokens, use: Use = "window", process: Process = processOf(theme)): Art {
  if (process === "halftone") {
    const art = theme === "riso" || tokens === undefined ? (mode === "dark" ? RISO_DARK : RISO_LIGHT) : risoIn(mode, tokens);
    return use === "print" ? { ...art, margin: 0 } : art;
  }
  if (process === "stitch") {
    const art = theme === "stitch" || tokens === undefined ? (mode === "dark" ? DENIM : LINEN) : stitchIn(mode, tokens);
    return use === "print" ? { ...art, cell: 7, clear: 0 } : art;
  }
  if (theme === "tokyo-night") return use === "print" ? { ...DEEP_FIELD, sky: 0, margin: 0, side: 0 } : DEEP_FIELD;
  if (use === "print") return { ...DUSK, sky: 0, margin: 0, side: 0 };
  if (theme === "glass" || tokens === undefined) return mode === "dark" ? DUSK : DAWN;
  return colourway(mode, tokens);
}

// The same art with another picture (the person's choice).
export function withPicture(art: Art, picture: string): Art {
  return { ...art, picture };
}
