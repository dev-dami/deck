// The pages' pictures: works in the public domain, dithered ahead of time
// into 1-bit plates (scripts/dither-art.py, art/plates/). One pixel of a
// plate is one cell on screen; ui/kit/plate.tsx gives the two tones. Each
// moment has its own picture. The credits follow art/ATTRIBUTION.md and
// art/met/manifest.json, and each page shows its own, in faint type.

export interface PlateArt {
  name: string;
  src: string;
  // The plate's size in cells.
  w: number;
  h: number;
  // The faint line by the picture: who, what, when, where it is kept.
  credit: string;
}

const MET = "The Metropolitan Museum of Art, Open Access.";

export const PLATES = {
  // The Welcome's hero.
  alps: {
    name: "alps",
    src: new URL("./art/plates/alps.png", import.meta.url).href,
    w: 680,
    h: 311,
    credit: "Adolphe Braun, an alpine landscape, about 1870. The J. Paul Getty Museum, Open Content.",
  },
  // Pairing: two banks and the bridge between them.
  bridge: {
    name: "bridge",
    src: new URL("./art/plates/bridge.png", import.meta.url).href,
    w: 340,
    h: 437,
    credit: `Utagawa Hiroshige, Sudden Shower over Shin-Ōhashi Bridge and Atake, 1857. ${MET}`,
  },
  // An inbox with nothing waiting.
  sea: {
    name: "sea",
    src: new URL("./art/plates/sea.png", import.meta.url).href,
    w: 176,
    h: 94,
    credit: `Gustave Le Gray, Mediterranean Sea, Sète, 1857. ${MET}`,
  },
  // A team: crews rowing together under the wave.
  wave: {
    name: "wave",
    src: new URL("./art/plates/wave.png", import.meta.url).href,
    w: 144,
    h: 97,
    credit: `Katsushika Hokusai, Under the Wave off Kanagawa, about 1830–32. ${MET}`,
  },
  // No usage to show: a clear sky.
  fuji: {
    name: "fuji",
    src: new URL("./art/plates/fuji.png", import.meta.url).href,
    w: 144,
    h: 96,
    credit: `Katsushika Hokusai, South Wind, Clear Sky, about 1830–32. ${MET}`,
  },
} as const satisfies Record<string, PlateArt>;

export interface PlateFit {
  // CSS pixels per cell.
  cell: number;
  // The cells shown, and where they start in the plate.
  cols: number;
  rows: number;
  x: number;
  y: number;
}

// How a plate of w×h cells covers a box: the smallest whole cell of at
// least `min` px that fills the box, and the window of the plate it shows,
// placed by the focus (0 to 1 on each axis). Cells are whole pixels and
// the plate is cropped, never resampled, so the dither stays as drawn.
export function plateFit(boxW: number, boxH: number, w: number, h: number, min = 2, focus: readonly [number, number] = [0.5, 0.5]): PlateFit {
  const cell = Math.max(min, Math.ceil(boxW / w), Math.ceil(boxH / h));
  const cols = Math.min(w, Math.max(1, Math.ceil(boxW / cell)));
  const rows = Math.min(h, Math.max(1, Math.ceil(boxH / cell)));
  const clamp = (v: number) => Math.min(1, Math.max(0, v));
  return { cell, cols, rows, x: Math.round((w - cols) * clamp(focus[0])), y: Math.round((h - rows) * clamp(focus[1])) };
}

// The window's pixels in two colours (RGBA): `hi` where the plate's bit is
// set, `lo` where it is not.
export function paintPlate(
  bits: Uint8Array,
  w: number,
  fit: PlateFit,
  lo: readonly [number, number, number, number],
  hi: readonly [number, number, number, number],
): Uint8ClampedArray<ArrayBuffer> {
  const out = new Uint8ClampedArray(fit.cols * fit.rows * 4);
  for (let r = 0; r < fit.rows; r++) {
    const src = (fit.y + r) * w + fit.x;
    for (let c = 0; c < fit.cols; c++) {
      const tone = bits[src + c] === 1 ? hi : lo;
      out.set(tone, (r * fit.cols + c) * 4);
    }
  }
  return out;
}
