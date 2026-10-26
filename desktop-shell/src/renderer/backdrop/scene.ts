// Liquid Glass's process: the picture dithered (Atkinson) at a coarse cell
// into two inks, or a few for a colour picture. The top of the frame is
// graded toward the ground, as a photographer holds back a sky, so type
// and the sidebar sit on calm tone. The Deck mark lies over it all, huge
// and faint, as a watermark lies in paper. The glass frosts it.

import { atkinson, atkinsonPalette, autoLevels, levels, luma, markGeometry, mix, type Rect, type RGB } from "./core";
import { type Ctx, context, type Framing, pixels, scratch, type Source } from "./canvas";
import type { SceneArt } from "./inks";

// The mark: the prompt from the app icon, across most of the window, its
// cursor low on the right and cropped by nothing.
export function markFrame(w: number, h: number): Rect {
  const mh = Math.min(h * 0.82, (w * 0.84) / 1.47);
  return { x: w * 0.08, y: (h - mh) * 0.55, w: mh * 1.47, h: mh };
}

// How much of each cell the mark covers, 0..1.
function markMask(cols: number, rows: number): Float32Array {
  const ctx = context(scratch(cols, rows), { willReadFrequently: true });
  const f = markFrame(cols, rows);
  const m = markGeometry(f.x, f.y, f.h);
  ctx.strokeStyle = "#fff";
  ctx.fillStyle = "#fff";
  ctx.lineWidth = m.stroke;
  ctx.lineCap = "round";
  ctx.lineJoin = "round";
  ctx.beginPath();
  m.chevron.forEach(([x, y], i) => (i === 0 ? ctx.moveTo(x, y) : ctx.lineTo(x, y)));
  ctx.stroke();
  ctx.beginPath();
  ctx.roundRect(m.cursor.x, m.cursor.y, m.cursor.w, m.cursor.h, m.cursor.h / 2);
  ctx.fill();
  const a = ctx.getImageData(0, 0, cols, rows).data;
  const out = new Float32Array(cols * rows);
  for (let i = 0; i < out.length; i++) out[i] = (a[i * 4 + 3] as number) / 255;
  return out;
}

// RGBA bytes as one little-endian word.
const word = (c: RGB) => ((255 << 24) | (Math.round(c[2]) << 16) | (Math.round(c[1]) << 8) | Math.round(c[0])) >>> 0;

export function renderScene(ctx: Ctx, W: number, H: number, dpr: number, art: SceneArt, img: Source | null, f: Framing): void {
  if (img === null) {
    // No picture yet: the ground alone, so nothing flashes.
    ctx.fillStyle = `rgb(${art.ground.join(" ")})`;
    ctx.fillRect(0, 0, W, H);
    return;
  }
  const cell = Math.max(1, Math.round(art.cell * dpr));
  const cols = Math.ceil(W / cell);
  const rows = Math.ceil(H / cell);
  const data = pixels(img, cols, rows, f);
  const mark = markMask(cols, rows);
  const out = scratch(cols, rows);
  const octx = context(out);
  const im = octx.createImageData(cols, rows);
  const px = new Uint32Array(im.data.buffer);
  // The sky's grade: full at the top, gone by 60% of the height.
  const grade = new Float32Array(rows);
  for (let y = 0; y < rows; y++) {
    const t = Math.max(0, 1 - y / (rows * 0.6));
    grade[y] = art.sky * t * t * (3 - 2 * t);
  }
  // Each ink, and the same ink under the mark, moved toward the middle
  // of the inks.
  const inks = art.inks.map(word);
  const mid = mix(art.inks[0] ?? art.ground, art.inks[art.inks.length - 1] ?? art.ground, 0.5);
  const marked = art.inks.map((c) => word(mix(c, mid, art.mark)));

  if (art.colour) {
    const rgb = new Float32Array(cols * rows * 3);
    for (let y = 0; y < rows; y++) {
      const g = grade[y] as number;
      for (let x = 0; x < cols; x++) {
        const i = y * cols + x;
        for (let c = 0; c < 3; c++) {
          const v = data[i * 4 + c] as number;
          rgb[i * 3 + c] = g >= 0 ? v * (1 - g) : v + (255 - v) * -g;
        }
      }
    }
    const idx = atkinsonPalette(rgb, cols, rows, art.inks);
    for (let i = 0; i < idx.length; i++) px[i] = ((mark[i] as number) > 0.5 ? marked : inks)[idx[i] as number] as number;
  } else {
    const v = luma(data);
    const [black, white] = autoLevels(v);
    levels(v, black, white, art.gamma);
    for (let y = 0; y < rows; y++) {
      const g = grade[y] as number;
      if (g === 0) continue;
      for (let x = 0; x < cols; x++) {
        const i = y * cols + x;
        const t = v[i] as number;
        v[i] = g > 0 ? t * (1 - g) : t + (1 - t) * -g;
      }
    }
    const bits = atkinson(v, cols, rows);
    for (let i = 0; i < bits.length; i++) px[i] = ((mark[i] as number) > 0.5 ? marked : inks)[bits[i] as number] as number;
  }
  octx.putImageData(im, 0, 0);
  ctx.imageSmoothingEnabled = false;
  ctx.drawImage(out, 0, 0, cols * cell, rows * cell);
}
