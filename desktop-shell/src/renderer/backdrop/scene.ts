// Liquid Glass's process: a scene. The person's wallpaper, or a sky at dusk
// (dawn in light) with ridges falling away into haze, and the Deck mark
// across it huge and faint, as a watermark. The glass frosts it.

import { css, fbm, hash2, markGeometry, mix, type RGB } from "./core";
import { type AnyCanvas, type Ctx, context, cover, fill, grain, scratch } from "./canvas";
import type { SceneArt } from "./inks";

// The sky, its clouds and the ridges, with no grain and no mark: the
// picture the riso screens separate too. Drawn soft, at whatever size it
// is given; the caller scales it up.
export function drawSky(ctx: Ctx, w: number, h: number, art: SceneArt, seed = 3): void {
  const g = ctx.createLinearGradient(0, 0, 0, h);
  for (const [at, c] of art.sky) g.addColorStop(at, css(c));
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, w, h);
  const horizon = art.sky[art.sky.length - 1]?.[1] ?? art.ground;
  const horizonY = (art.sky[art.sky.length - 1]?.[0] ?? 0.86) * h;

  // Where the sun went down: the sky stays lighter low and to the right.
  // A wide, flat falloff of the horizon's own colour, not a glow.
  ctx.save();
  ctx.translate(w * 0.72, horizonY);
  ctx.scale(1, 0.32);
  const sun = ctx.createRadialGradient(0, 0, 0, 0, 0, w * 0.75);
  sun.addColorStop(0, css(horizon, 0.55));
  sun.addColorStop(0.5, css(horizon, 0.16));
  sun.addColorStop(1, css(horizon, 0));
  ctx.fillStyle = sun;
  ctx.fillRect(-w, -h * 4, w * 2.2, h * 8);
  ctx.restore();

  // Stratus: long thin bands, lit from below near the horizon, in shade
  // higher up.
  for (let i = 0; i < 9; i++) {
    const r = (k: number) => hash2(i, k, seed);
    const y = h * (0.42 + r(1) * 0.36);
    const t = Math.min(1, Math.max(0, (y / h - 0.42) / 0.36));
    const cx = w * (r(2) * 1.2 - 0.1);
    const rx = w * (0.18 + r(3) * 0.32);
    const ry = h * (0.004 + r(4) * 0.012);
    ctx.save();
    ctx.translate(cx, y);
    ctx.scale(rx, ry);
    const c = ctx.createRadialGradient(0, 0, 0, 0, 0, 1);
    const tone = mix(art.cloudShade, art.cloudLit, t * t);
    const a = 0.18 + 0.4 * t * r(5);
    c.addColorStop(0, css(tone, a));
    c.addColorStop(1, css(tone, 0));
    ctx.fillStyle = c;
    ctx.beginPath();
    ctx.arc(0, 0, 1, 0, Math.PI * 2);
    ctx.fill();
    ctx.restore();
  }

  // Ridges, far to near. Each is lighter and hazier at its crest, as air
  // between you and it would make it.
  const n = art.ridges.length;
  art.ridges.forEach((c, i) => {
    const depth = i / Math.max(1, n - 1);
    const base = h * (0.8 + depth * 0.1);
    const amp = h * (0.035 + depth * 0.06);
    const scale = w * (0.16 + depth * 0.22);
    const step = Math.max(2, w / 360);
    ctx.beginPath();
    ctx.moveTo(0, h);
    for (let x = 0; x <= w + step; x += step) {
      const ridge = fbm(x / scale + i * 7.3, i * 3.1, seed + i, 4);
      const lean = (x / w - 0.5) * (i % 2 === 0 ? 0.6 : -0.6);
      ctx.lineTo(x, base - amp * (ridge * 1.6 - 0.3 + lean * 0.4));
    }
    ctx.lineTo(w, h);
    ctx.closePath();
    const top = base - amp * 1.3;
    const lg = ctx.createLinearGradient(0, top, 0, h);
    lg.addColorStop(0, css(mix(c, horizon, 0.28 * (1 - depth))));
    lg.addColorStop(0.35, css(c));
    lg.addColorStop(1, css(mix(c, art.ground, 0.5)));
    ctx.fillStyle = lg;
    ctx.fill();
  });
}

// The mark: the prompt from the app icon, set huge to the left and cropped
// by the window's bottom edge, as a watermark sits on a print.
export function markFrame(w: number, h: number): { x: number; y: number; h: number } {
  const mh = Math.min(h * 0.86, w * 0.5);
  return { x: w * 0.06, y: h * 0.2, h: mh };
}

export function drawMark(ctx: Ctx, w: number, h: number, c: RGB, alpha: number): void {
  const f = markFrame(w, h);
  const m = markGeometry(f.x, f.y, f.h);
  ctx.save();
  ctx.strokeStyle = css(c, alpha);
  ctx.fillStyle = css(c, alpha);
  ctx.lineWidth = m.stroke;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.beginPath();
  const [first, ...rest] = m.chevron;
  if (first !== undefined) ctx.moveTo(first[0], first[1]);
  for (const [x, y] of rest) ctx.lineTo(x, y);
  ctx.stroke();
  const k = m.cursor;
  ctx.beginPath();
  ctx.roundRect(k.x, k.y, k.w, k.h, k.h / 2);
  ctx.fill();
  ctx.restore();
}

export function renderScene(ctx: Ctx, W: number, H: number, art: SceneArt, wallpaper: (CanvasImageSource & { width: number; height: number }) | null): void {
  fill(ctx, art.ground);
  ctx.fillRect(0, 0, W, H);
  if (wallpaper !== null) {
    cover(ctx, wallpaper, W, H);
    fill(ctx, art.veil, art.veilAlpha);
    ctx.fillRect(0, 0, W, H);
  } else {
    // The sky is soft: draw it at a quarter and let the scale-up blur it.
    const q = 4;
    const low: AnyCanvas = scratch(W / q, H / q);
    drawSky(context(low), low.width, low.height, art);
    ctx.imageSmoothingEnabled = true;
    ctx.imageSmoothingQuality = "high";
    ctx.drawImage(low, 0, 0, W, H);
  }
  drawMark(ctx, W, H, art.mark, art.markAlpha);
  grain(ctx, W, H, art.grain * 2.2, "overlay");
}
