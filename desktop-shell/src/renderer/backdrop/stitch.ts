// Stitch's process: the picture charted for cross-stitch in a few tonal
// threads, with Deck's own cards and dice stitched over it in full colour,
// on even-weave linen (light) or indigo denim (dark). Each stitch is a
// real "x": an under stroke, then an over stroke that shades the one
// below, each a twisted thread lit along its middle. Where the chart is
// empty the cloth shows, holes and all.

import { autoLevels, chartTable, chartTones, hash2, levels, luma, mix, type RGB, T, tableLayout } from "./core";
import { type AnyCanvas, type Ctx, context, type Framing, fill, grain, pixels, scratch, type Source } from "./canvas";
import type { StitchArt } from "./inks";

const css = (c: RGB, a = 1) => `rgb(${Math.round(c[0])} ${Math.round(c[1])} ${Math.round(c[2])} / ${a})`;

// One cell of cloth, to repeat: a woven block with a hole at each corner.
function clothTile(art: StitchArt, cell: number): AnyCanvas {
  const c = scratch(cell, cell);
  const ctx = context(c);
  fill(ctx, art.cloth);
  ctx.fillRect(0, 0, cell, cell);
  const light = mix(art.cloth, [255, 255, 255], 0.14);
  const dark = mix(art.cloth, art.hole, 0.45);
  if (art.twill) {
    // Denim: the twill's diagonal, a lighter weft over indigo.
    ctx.strokeStyle = css(light, 0.38);
    ctx.lineWidth = Math.max(1, cell / 12);
    for (let k = -cell; k < cell * 2; k += cell / 4) {
      ctx.beginPath();
      ctx.moveTo(k, 0);
      ctx.lineTo(k + cell, cell);
      ctx.stroke();
    }
  } else {
    // Linen: three threads each way, slubbed.
    for (let i = 0; i < 3; i++) {
      const at = ((i + 0.5) / 3) * cell;
      ctx.fillStyle = css(i === 1 ? light : dark, 0.35);
      ctx.fillRect(0, at - cell / 14, cell, cell / 7);
      ctx.fillStyle = css(i === 1 ? dark : light, 0.25);
      ctx.fillRect(at - cell / 14, 0, cell / 7, cell);
    }
  }
  // The block's soft rise: lit top left, in shade bottom right.
  const g = ctx.createLinearGradient(0, 0, cell, cell);
  g.addColorStop(0, css([255, 255, 255], 0.1));
  g.addColorStop(0.5, css([255, 255, 255], 0));
  g.addColorStop(1, css([0, 0, 0], 0.14));
  ctx.fillStyle = g;
  ctx.fillRect(0, 0, cell, cell);
  // The holes, as quarters at the four corners.
  const r = cell * 0.13;
  fill(ctx, art.hole);
  for (const [x, y] of [
    [0, 0],
    [cell, 0],
    [0, cell],
    [cell, cell],
  ] as const) {
    ctx.beginPath();
    ctx.arc(x, y, r, 0, Math.PI * 2);
    ctx.fill();
  }
  return c;
}

// One stroke of thread from corner to corner, in a frame turned to run
// along x: a long capsule, dark at its edges and lit along its middle,
// with the ply's twist across it.
function stroke(ctx: Ctx, c: RGB, len: number, wid: number, seed: number): void {
  const g = ctx.createLinearGradient(0, -wid / 2, 0, wid / 2);
  g.addColorStop(0, css(mix(c, [0, 0, 0], 0.42)));
  g.addColorStop(0.32, css(c));
  g.addColorStop(0.48, css(mix(c, [255, 255, 255], 0.3)));
  g.addColorStop(0.68, css(c));
  g.addColorStop(1, css(mix(c, [0, 0, 0], 0.5)));
  ctx.fillStyle = g;
  ctx.beginPath();
  ctx.roundRect(-len / 2, -wid / 2, len, wid, wid / 2);
  ctx.fill();
  // The twist: thin dark lines slanting across the strands.
  ctx.save();
  ctx.clip();
  ctx.strokeStyle = css(mix(c, [0, 0, 0], 0.6), 0.32);
  ctx.lineWidth = Math.max(0.6, wid / 9);
  const step = wid * 0.7;
  const shift = hash2(seed, 3, 1) * step;
  for (let x = -len / 2 - wid + shift; x < len / 2 + wid; x += step) {
    ctx.beginPath();
    ctx.moveTo(x, wid / 2);
    ctx.lineTo(x + wid * 0.6, -wid / 2);
    ctx.stroke();
  }
  ctx.restore();
}

// One stitch in a thread, to stamp: a shadow on the cloth, the under
// stroke (/), the over stroke's shadow on it, then the over stroke (\).
// A few variants a thread, so the stitches are not all the same.
function stitchSprite(c: RGB, cell: number, seed: number): AnyCanvas {
  // A margin round the cell, for the shadow.
  const m = margin(cell);
  const s = scratch(cell + 2 * m, cell + 2 * m);
  const ctx = context(s);
  const pad = cell * 0.1;
  const len = Math.SQRT2 * (cell - 2 * pad) + cell * 0.12;
  const wid = cell * 0.36;
  const jitter = (k: number) => (hash2(seed, k, 9) - 0.5) * cell * 0.04;
  const at = (angle: number, dx: number, dy: number, draw: () => void) => {
    ctx.save();
    ctx.translate(m + cell / 2 + dx, m + cell / 2 + dy);
    ctx.rotate(angle);
    draw();
    ctx.restore();
  };
  const shadow = (angle: number) =>
    at(angle, cell * 0.05, cell * 0.07, () => {
      ctx.fillStyle = "rgb(0 0 0 / 0.28)";
      ctx.beginPath();
      ctx.roundRect(-len / 2, -wid / 2, len, wid, wid / 2);
      ctx.fill();
    });
  shadow(-Math.PI / 4);
  shadow(Math.PI / 4);
  at(-Math.PI / 4 + jitter(1) / cell, jitter(2), jitter(3), () => stroke(ctx, mix(c, [0, 0, 0], 0.08), len, wid, seed));
  at(Math.PI / 4, cell * 0.03, cell * 0.045, () => {
    ctx.fillStyle = "rgb(0 0 0 / 0.22)";
    ctx.beginPath();
    ctx.roundRect(-len / 2, -wid / 2, len, wid, wid / 2);
    ctx.fill();
  });
  at(Math.PI / 4 + jitter(4) / cell, jitter(5), jitter(6), () => stroke(ctx, c, len, wid, seed + 1));
  return s;
}

const VARIANTS = 3;
const margin = (cell: number) => Math.ceil(cell * 0.15);

export function renderStitch(ctx: Ctx, W: number, H: number, dpr: number, art: StitchArt, img: Source | null, f: Framing): void {
  const cell = Math.max(4, Math.round(art.cell * dpr));
  const cols = Math.ceil(W / cell);
  const rows = Math.ceil(H / cell);
  // The cloth, with a slow unevenness in the dye.
  const p = ctx.createPattern(clothTile(art, cell), "repeat");
  if (p !== null) ctx.fillStyle = p;
  ctx.fillRect(0, 0, W, H);
  grain(ctx, W, H, 0.12, 13);

  // The chart: a thread index a cell, the picture's tones first.
  const threads: RGB[] = [...art.threads, ...art.roles];
  const chart = new Int16Array(cols * rows).fill(-1);
  if (img !== null) {
    const v = luma(pixels(img, cols, rows, f));
    const [black, white] = autoLevels(v);
    levels(v, black, white);
    const tones = chartTones(v, art.threads.length, art.bare, art.twill);
    for (let i = 0; i < tones.length; i++) chart[i] = tones[i] ?? -1;
  }
  if (art.table) {
    const table = chartTable(cols, rows, tableLayout(cols, rows, Math.ceil((art.clear * dpr) / cell)));
    for (let i = 0; i < table.length; i++) {
      const t = table[i] ?? T.Blank;
      if (t !== T.Blank) chart[i] = art.threads.length + t;
    }
  }

  const sprites = new Map<number, AnyCanvas>();
  const sprite = (k: number, v: number) => {
    const key = k * VARIANTS + v;
    let s = sprites.get(key);
    if (s === undefined) {
      s = stitchSprite(threads[k] ?? art.cloth, cell, key);
      sprites.set(key, s);
    }
    return s;
  };
  const m = margin(cell);
  for (let y = 0; y < rows; y++)
    for (let x = 0; x < cols; x++) {
      const k = chart[y * cols + x] ?? -1;
      if (k < 0) continue;
      ctx.drawImage(sprite(k, Math.floor(hash2(x, y, 4) * VARIANTS)), x * cell - m, y * cell - m);
    }
}
