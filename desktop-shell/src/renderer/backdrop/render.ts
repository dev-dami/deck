// One backdrop render: the art of a theme, a picture and a size in, one
// bitmap out. It runs in the worker (worker.ts) or, where there is none,
// on the page; the code is the same.

import { type Ctx, context, type Framing, type Source, scratch } from "./canvas";
import { renderHalftone } from "./halftone";
import type { Art } from "./inks";
import { renderScene } from "./scene";
import { renderStitch } from "./stitch";

export interface Job {
  // Device pixels, and the ratio to CSS pixels.
  W: number;
  H: number;
  dpr: number;
  art: Art;
  framing: Framing;
}

export function draw(ctx: Ctx, job: Job, img: Source | null): void {
  const { W, H, dpr, art, framing } = job;
  if (art.process === "halftone") renderHalftone(ctx, W, H, dpr, art, img, framing);
  else if (art.process === "stitch") renderStitch(ctx, W, H, dpr, art, img, framing);
  else renderScene(ctx, W, H, dpr, art, img, framing);
}

// Renders to a new bitmap, and how long it took in ms.
export function renderBitmap(job: Job, img: Source | null): { bitmap: ImageBitmap; ms: number } {
  const t0 = performance.now();
  const c = scratch(job.W, job.H) as OffscreenCanvas;
  draw(context(c, { alpha: false }), job, img);
  return { bitmap: c.transferToImageBitmap(), ms: performance.now() - t0 };
}
