// Small canvas helpers the three processes share. They take either kind of
// canvas, so the same code runs in the worker and on the page.

import { css, hash2, place, type RGB } from "./core";

export type Ctx = CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D;
export type AnyCanvas = HTMLCanvasElement | OffscreenCanvas;
export type Source = CanvasImageSource & { width: number; height: number };

// Where a picture's subject sits, so a crop keeps it (pictures.ts).
export interface Framing {
  ax: number;
  ay: number;
  figure: boolean;
  // The margin to cut away: top, right, bottom, left, as fractions.
  crop: [number, number, number, number];
}

// A scratch canvas: offscreen where the browser has it.
export function scratch(w: number, h: number): AnyCanvas {
  const W = Math.max(1, Math.round(w));
  const H = Math.max(1, Math.round(h));
  if (typeof OffscreenCanvas !== "undefined") return new OffscreenCanvas(W, H);
  const c = document.createElement("canvas");
  c.width = W;
  c.height = H;
  return c;
}

export function context(c: AnyCanvas, opts?: CanvasRenderingContext2DSettings): Ctx {
  const ctx = c.getContext("2d", opts) as Ctx | null;
  if (ctx === null) throw new Error("no 2d context");
  return ctx;
}

export function fill(ctx: Ctx, c: RGB, a = 1): void {
  ctx.fillStyle = css(c, a);
}

// The picture at w×h pixels, read back as RGBA: cropped to fill (cover), or
// set whole against its own ground colour (a figure, contain). The browser
// resamples it, which averages the pixels a cell covers.
export function pixels(img: Source, w: number, h: number, f: Framing, zoom = 1): Uint8ClampedArray {
  const c = scratch(w, h);
  const ctx = context(c, { willReadFrequently: true });
  const [t, rt, b, l] = f.crop;
  const sx = img.width * l;
  const sy = img.height * t;
  const sw = img.width * (1 - l - rt);
  const sh = img.height * (1 - t - b);
  if (f.figure) {
    // The ground of a plate photograph: its corner, read from the picture.
    const probe = context(scratch(1, 1), { willReadFrequently: true });
    probe.drawImage(img, sx + 2, sy + 2, 1, 1, 0, 0, 1, 1);
    const [r = 255, g = 255, bl = 255] = probe.getImageData(0, 0, 1, 1).data;
    ctx.fillStyle = css([r, g, bl]);
    ctx.fillRect(0, 0, c.width, c.height);
  }
  const r = place(sw, sh, c.width, c.height, f.figure ? "contain" : "cover", f.ax, f.ay, zoom);
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(img, sx, sy, sw, sh, r.x, r.y, r.w, r.h);
  return ctx.getImageData(0, 0, c.width, c.height).data;
}

// A tile of fine grain, the same every time: fibre in the paper, slub in
// the cloth. Mid-grey with light and dark specks, for "soft-light".
const tiles = new Map<string, AnyCanvas>();
function grainTile(size: number, seed: number): AnyCanvas {
  const key = `${size}:${seed}`;
  const had = tiles.get(key);
  if (had !== undefined) return had;
  const c = scratch(size, size);
  const ctx = context(c);
  const img = ctx.createImageData(size, size);
  for (let y = 0; y < size; y++)
    for (let x = 0; x < size; x++) {
      const v = Math.round(128 + (hash2(x, y, seed) - 0.5) * 230);
      const i = (y * size + x) * 4;
      img.data[i] = v;
      img.data[i + 1] = v;
      img.data[i + 2] = v;
      img.data[i + 3] = 255;
    }
  ctx.putImageData(img, 0, 0);
  tiles.set(key, c);
  return c;
}

// Lays grain over the whole frame.
export function grain(ctx: Ctx, w: number, h: number, alpha: number, seed = 7, mode: GlobalCompositeOperation = "soft-light"): void {
  if (alpha <= 0) return;
  const p = ctx.createPattern(grainTile(160, seed), "repeat");
  if (p === null) return;
  ctx.save();
  ctx.globalAlpha = alpha;
  ctx.globalCompositeOperation = mode;
  ctx.fillStyle = p;
  ctx.fillRect(0, 0, w, h);
  ctx.restore();
}
