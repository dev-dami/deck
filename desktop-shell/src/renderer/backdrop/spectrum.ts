// Horizon's process: the picture dithered in red, green and blue, each
// channel on its own, as a display's sub-pixels or a colour screen print.
// Every dot is one of a few steps of each primary, so a dark ground is a
// field of faint coloured specks and the bright limb resolves into pure
// red, gold, cyan and white dots up close. A little noise lifts the
// shadows, so no part of the window prints a dead black.

import { type Ctx, context, type Framing, pixels, scratch, type Source } from "./canvas";
import { hash2, type RGB } from "./core";
import type { SpectrumArt } from "./inks";

// An 8×8 Bayer matrix, the thresholds of an ordered dither, 0..63: the
// bits of x^y and y interleaved, the lowest pair the highest bits, so each
// step of the threshold lands as far as it can from the last.
export const BAYER = (() => {
  const m = new Uint8Array(64);
  for (let y = 0; y < 8; y++) {
    for (let x = 0; x < 8; x++) {
      const xy = x ^ y;
      let v = 0;
      for (let bit = 0; bit < 3; bit++) v = (v << 2) | (((xy >> bit) & 1) << 1) | ((y >> bit) & 1);
      m[y * 8 + x] = v;
    }
  }
  return m;
})();

// One channel of one cell: its value (0..1) dithered to `steps` levels by
// a threshold that is half the Bayer grid, half white noise, so the dots
// neither line up in a lattice nor clump. Each channel reads the grid from
// its own offset, so the three primaries of one cell rarely fire together.
export function ditherChannel(v: number, x: number, y: number, c: number, steps: number): number {
  const b = (BAYER[((y + c * 5) & 7) * 8 + ((x + c * 3) & 7)] as number) / 64;
  const t = b * 0.5 + hash2(x, y, 31 + c) * 0.5;
  const n = steps - 1;
  const q = Math.floor(v * n + t);
  return (q < 0 ? 0 : q > n ? n : q) / n;
}

// The picture's colour in a light mode: paper, with the picture's colours
// laid on it as much as the picture is bright, so the dark of space is
// bare paper and the limb prints as a pastel arc.
function onPaper(r: number, g: number, b: number, paper: RGB): [number, number, number] {
  const lum = 0.2126 * r + 0.7152 * g + 0.0722 * b;
  const k = Math.min(1, lum * 1.5);
  // The picture's hue at full chroma, so the arc stays coloured on white.
  const m = Math.max(r, g, b, 1e-3);
  const lay = (p: number, v: number) => (p / 255) * (1 - k) + (v / m) * k * 0.92;
  return [lay(paper[0], r), lay(paper[1], g), lay(paper[2], b)];
}

export function renderSpectrum(ctx: Ctx, W: number, H: number, dpr: number, art: SpectrumArt, img: Source | null, f: Framing): void {
  if (img === null) {
    ctx.fillStyle = `rgb(${art.ground.join(" ")})`;
    ctx.fillRect(0, 0, W, H);
    return;
  }
  const cell = Math.max(1, Math.round(art.cell * dpr));
  const cols = Math.ceil(W / cell);
  const rows = Math.ceil(H / cell);
  const data = pixels(img, cols, rows, f);
  const out = scratch(cols, rows);
  const octx = context(out);
  const im = octx.createImageData(cols, rows);
  const px = im.data;
  const { ground, peak, steps, gain, floor, noise } = art;
  for (let y = 0; y < rows; y++) {
    for (let x = 0; x < cols; x++) {
      const i = (y * cols + x) * 4;
      let r = ((data[i] as number) / 255) * gain;
      let g = ((data[i + 1] as number) / 255) * gain;
      let b = ((data[i + 2] as number) / 255) * gain;
      if (art.light) [r, g, b] = onPaper(r, g, b, art.paper);
      const vs = [r, g, b];
      for (let c = 0; c < 3; c++) {
        // The speckle: a floor and a little noise in each channel, so the
        // shadows carry a few dim dots of every primary.
        const v = (vs[c] as number) + floor + (hash2(x, y, 7 + c) - 0.5) * noise;
        const q = ditherChannel(v, x, y, c, steps);
        const lo = ground[c] as number;
        px[i + c] = lo + q * ((peak[c] as number) - lo);
      }
      px[i + 3] = 255;
    }
  }
  octx.putImageData(im, 0, 0);
  ctx.imageSmoothingEnabled = false;
  ctx.drawImage(out, 0, 0, cols * cell, rows * cell);
}
