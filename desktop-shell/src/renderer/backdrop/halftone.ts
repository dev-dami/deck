// Riso's process: the picture separated into two plates, each put through
// its own rotated dot screen, and printed out of register: black (or white,
// on black stock) at 45°, fluorescent pink at 15°. Then a few hard shapes
// of solid ink, as a zine pastes a block of spot colour over a print.

import { css, dotRadius, fbm, hash2, type RGB, risoShapes, sample, screen, separateOnBlack, separateOnPaper, toneCurve } from "./core";
import { type AnyCanvas, type Ctx, context, cover, fill, grain, scratch } from "./canvas";
import type { HalftoneArt } from "./inks";
import { drawMark, drawSky } from "./scene";

type Image = CanvasImageSource & { width: number; height: number };

// The pitch of the screen in CSS px: coarse, so the dots read as print at
// arm's length.
export const PITCH = 6;

// Solid ink is never flat on a riso: the drum leaves it a little mottled
// and specked with paper.
function inkTexture(ctx: Ctx, w: number, h: number, stock: RGB, scale: number): void {
  ctx.save();
  ctx.globalCompositeOperation = "source-atop";
  grain(ctx, w, h, 0.22, "soft-light", 11);
  const n = Math.round((w * h) / (900 * scale * scale));
  fill(ctx, stock, 0.55);
  ctx.beginPath();
  for (let i = 0; i < n; i++) {
    const x = hash2(i, 1, 5) * w;
    const y = hash2(i, 2, 5) * h;
    const r = (0.3 + hash2(i, 3, 5) * 0.7) * scale;
    ctx.moveTo(x + r, y);
    ctx.arc(x, y, r, 0, Math.PI * 2);
  }
  ctx.fill();
  ctx.restore();
}

export function renderHalftone(ctx: Ctx, W: number, H: number, dpr: number, art: HalftoneArt, wallpaper: Image | null, pitchCss = PITCH): void {
  const pitch = pitchCss * dpr;
  // The source, at two samples a dot.
  const s = pitch / 2;
  const src: AnyCanvas = scratch(W / s, H / s);
  const sctx = context(src, { willReadFrequently: true });
  if (wallpaper !== null) cover(sctx, wallpaper, src.width, src.height);
  else {
    drawSky(sctx, src.width, src.height, art.source);
    // The mark goes into the picture, so it prints as a tint of its own.
    drawMark(sctx, src.width, src.height, art.onBlack ? [255, 255, 255] : [0, 0, 0], art.onBlack ? 0.16 : 0.1);
  }
  const data = sctx.getImageData(0, 0, src.width, src.height).data;
  const sw = src.width;
  const sh = src.height;

  // The stock.
  fill(ctx, art.ground);
  ctx.fillRect(0, 0, W, H);
  grain(ctx, W, H, art.onBlack ? 0.05 : 0.08, "soft-light", 3);

  const shapes = risoShapes(W, H);

  // One plate: dots of `ink` sized by `which` coverage, at `angle`.
  const plate = (ink: RGB, angle: number, which: "key" | "spot", dx: number, dy: number, limit: number) => {
    const pts = screen(W, H, pitch, angle);
    const min = 0.35 * dpr;
    ctx.save();
    ctx.translate(dx, dy);
    ctx.beginPath();
    for (let i = 0; i < pts.length; i += 2) {
      const x = pts[i] ?? 0;
      const y = pts[i + 1] ?? 0;
      const [r, g, b] = sample(data, sw, sh, (x / W) * sw, (y / H) * sh);
      const sep = art.onBlack ? separateOnBlack([r, g, b], art.spot) : separateOnPaper([r, g, b], art.spot);
      // The drum inks unevenly: a slow mottle over the plate.
      const mottle = 0.84 + 0.16 * fbm(x / (260 * dpr), y / (260 * dpr), which === "key" ? 2 : 9, 2);
      const cov = toneCurve(sep[which] * limit) * mottle;
      const rad = dotRadius(cov, pitch);
      if (rad < min) continue;
      ctx.moveTo(x + rad, y);
      ctx.arc(x, y, rad, 0, Math.PI * 2);
    }
    fill(ctx, ink);
    ctx.fill();
    ctx.restore();
  };

  // Pink goes down first; the key prints over it, out of register by a
  // hair. On paper the inks multiply; on black the white sits on top.
  const off = 1.6 * dpr;
  ctx.save();
  if (!art.onBlack) ctx.globalCompositeOperation = "multiply";
  plate(art.spot, 15, "spot", off, off * 0.6, art.onBlack ? 0.95 : 0.85);
  // The spot shapes are on the pink drum too.
  for (const sh of shapes.filter((x) => x.ink === "spot")) {
    const layer = scratch(W, H);
    const l = context(layer);
    fill(l, art.spot);
    shapePath(l, sh, off, off * 0.6);
    l.fill();
    inkTexture(l, W, H, art.ground, dpr);
    ctx.drawImage(layer, 0, 0);
  }
  plate(art.key, 45, "key", 0, 0, art.onBlack ? 0.9 : 0.7);
  ctx.restore();

  // Hard shapes of the key ink (white on black, black on paper), and paper
  // knocked out of the print.
  for (const sh of shapes.filter((x) => x.ink !== "spot")) {
    const layer = scratch(W, H);
    const l = context(layer);
    fill(l, sh.ink === "key" ? art.key : art.ground);
    shapePath(l, sh, 0, 0);
    l.fill();
    if (sh.ink === "key") inkTexture(l, W, H, art.ground, dpr);
    ctx.drawImage(layer, 0, 0);
  }
  grain(ctx, W, H, art.onBlack ? 0.06 : 0.1, "soft-light", 21);
}

function shapePath(ctx: Ctx, s: ReturnType<typeof risoShapes>[number], dx: number, dy: number): void {
  ctx.beginPath();
  if (s.kind === "circle") ctx.arc(s.x + dx, s.y + dy, s.r, 0, Math.PI * 2);
  else if (s.kind === "half") ctx.arc(s.x + dx, s.y + dy, s.r, Math.PI, 0);
  else ctx.rect(s.x + dx, s.y + dy, s.w, s.h);
}

export { css };
