// Small canvas helpers the three processes share.

import { css, hash2, type RGB } from "./core";

export type Ctx = CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D;
export type AnyCanvas = HTMLCanvasElement | OffscreenCanvas;

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

// A tile of fine grain, the same every time: film grain on the sky, fibre
// in the paper, slub in the cloth. Mid-grey with light and dark specks, for
// "overlay" or "soft-light".
const tiles = new Map<string, AnyCanvas>();
export function grainTile(size = 192, seed = 7): AnyCanvas {
  const key = `${size}:${seed}`;
  const had = tiles.get(key);
  if (had !== undefined) return had;
  const c = scratch(size, size);
  const ctx = context(c);
  const img = ctx.createImageData(size, size);
  for (let y = 0; y < size; y++)
    for (let x = 0; x < size; x++) {
      const v = Math.round(128 + (hash2(x, y, seed) - 0.5) * 255 * 0.9);
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
export function grain(ctx: Ctx, w: number, h: number, alpha: number, mode: GlobalCompositeOperation = "overlay", seed = 7): void {
  if (alpha <= 0) return;
  const p = ctx.createPattern(grainTile(192, seed), "repeat");
  if (p === null) return;
  ctx.save();
  ctx.globalAlpha = alpha;
  ctx.globalCompositeOperation = mode;
  ctx.fillStyle = p;
  ctx.fillRect(0, 0, w, h);
  ctx.restore();
}

export function fill(ctx: Ctx, c: RGB, a = 1): void {
  ctx.fillStyle = css(c, a);
}

// Draws an image to cover a w×h frame.
export function cover(ctx: Ctx, img: CanvasImageSource & { width: number; height: number }, w: number, h: number): void {
  const s = Math.max(w / img.width, h / img.height);
  const dw = img.width * s;
  const dh = img.height * s;
  ctx.imageSmoothingEnabled = true;
  ctx.imageSmoothingQuality = "high";
  ctx.drawImage(img, (w - dw) / 2, (h - dh) / 2, dw, dh);
}
