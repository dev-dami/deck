// Riso's process: the picture put through one round-dot screen at 15° in
// fluorescent pink, and two solid blocks of the same ink with the picture
// knocked out of them, so the stock shows through the blocks as dots. All
// of that is one drum, a hair out of register with the second: black, on
// paper only, for the deepest shadows at 45°. Solid ink is specked where
// the drum ran dry, and the stock has fibre.
//
// Each plate is a plane of ink cover, and each dot is stamped into it over
// its own few pixels; then one pass prints the planes. A canvas path of
// tens of thousands of dots costs several times as much.

import { autoLevels, type Block, clamp01, dotRadius, fbm, hash2, levels, luma, place, type Rect, risoBlocks, sampleGrey, screen } from "./core";
import { type Ctx, context, type Framing, fill, grain, pixels, scratch, type Source } from "./canvas";
import type { HalftoneArt } from "./inks";

// A block's coverage of a pixel, antialiased over one pixel.
function blockCover(b: Block, x: number, y: number): number {
  if (b.kind === "disc") return clamp01(b.r - Math.hypot(x - b.x, y - b.y) + 0.5);
  return clamp01(Math.min(x - b.x, b.x + b.w - x, y - b.y, b.y + b.h - y) + 0.5);
}

// A figure (a plant on a plate) is set whole, right of centre and a little
// small, so its ground frames it.
const FIGURE = { ax: 0.88, ay: 0.3, zoom: 0.92 };

// Where the subject sits in a w×h frame: a figure's box, or a square round
// the framing's point of interest.
function subjectOf(img: Source | null, f: Framing, w: number, h: number): Rect {
  if (img !== null && f.figure) {
    const [t, r, b, l] = f.crop;
    return place(img.width * (1 - l - r), img.height * (1 - t - b), w, h, "contain", FIGURE.ax, FIGURE.ay, FIGURE.zoom);
  }
  const s = Math.min(w, h) * 0.7;
  return { x: f.ax * w - s / 2, y: f.ay * h - s / 2, w: s, h: s };
}

export function renderHalftone(ctx: Ctx, W: number, H: number, dpr: number, art: HalftoneArt, img: Source | null, f: Framing): void {
  const pitch = art.pitch * dpr;
  // The pink drum sits a little down and right of the black one.
  const off = 1.6 * dpr;
  const blocks = risoBlocks(W / dpr, H / dpr, subjectOf(img, f, W / dpr, H / dpr), art.margin).map((b) =>
    b.kind === "disc" ? { ...b, x: b.x * dpr, y: b.y * dpr, r: b.r * dpr } : { ...b, x: b.x * dpr, y: b.y * dpr, w: b.w * dpr, h: b.h * dpr },
  );

  // Ink per point, 0..1, from the picture at two samples a dot. A figure
  // (a plant on a plate) is set whole on the right, and its dark tones are
  // ink on either stock. Any other picture prints positive: dark tones on
  // paper, light tones on black.
  let ink: (x: number, y: number) => number = () => 0;
  if (img !== null) {
    const sw = Math.ceil((W / pitch) * 2);
    const sh = Math.ceil((H / pitch) * 2);
    const v = luma(pixels(img, sw, sh, f.figure ? { ...f, ax: FIGURE.ax, ay: FIGURE.ay } : f, f.figure ? FIGURE.zoom : 1));
    // A figure's ground is most of the frame: its white point sits low.
    const [black, white] = autoLevels(v, 0.01, f.figure ? 0.4 : 0.99);
    levels(v, black, white, f.figure ? 0.9 : 1);
    if (f.figure || !art.onBlack) for (let i = 0; i < v.length; i++) v[i] = 1 - (v[i] as number);
    ink = (x, y) => sampleGrey(v, sw, sh, (x / W) * sw, (y / H) * sh);
  }
  // A whisper of tone below which the screen drops out, so the stock is
  // clean where the picture is empty; and the drum's slow, uneven inking.
  const cut = (c: number) => (c < 0.07 ? 0 : c);
  const mottle = (x: number, y: number, seed: number) => 0.8 + 0.2 * fbm(x / (220 * dpr), y / (220 * dpr), seed, 2);

  // Two plates, each a layer of its ink whose alpha is the ink's cover:
  // the pink drum and the black. Every pixel starts in the ink's colour
  // with no cover, so the stamps only ever write alpha.
  const word = (c: readonly number[], a: number) => ((a << 24) | ((c[2] as number) << 16) | ((c[1] as number) << 8) | (c[0] as number)) >>> 0;
  const layer = (c: readonly number[]) => {
    const w32 = new Uint32Array(W * H).fill(word(c, 0));
    return { w32, a: new Uint8ClampedArray(w32.buffer) };
  };
  const pink = layer(art.spot);
  const key = art.onBlack ? null : layer(art.key);
  const pinkA = pink.a;

  // The blocks, solid, row by row: the inside of each row is filled at
  // once and only its two ends are worked out, antialiased. Then specks of
  // stock, where the drum ran dry.
  for (const b of blocks) {
    const y0 = Math.max(0, Math.floor((b.kind === "disc" ? b.y - b.r : b.y) + off));
    const y1 = Math.min(H - 1, Math.ceil((b.kind === "disc" ? b.y + b.r : b.y + b.h) + off));
    for (let y = y0; y <= y1; y++) {
      const py = y + 0.5 - off;
      const half = b.kind === "disc" ? Math.sqrt(Math.max(0, b.r * b.r - (py - b.y) ** 2)) : b.w / 2;
      const mid = (b.kind === "disc" ? b.x : b.x + b.w / 2) + off;
      const x0 = Math.max(0, Math.floor(mid - half - 1));
      const x1 = Math.min(W - 1, Math.ceil(mid + half + 1));
      const in0 = Math.max(x0, Math.ceil(mid - half + 1));
      const in1 = Math.min(x1, Math.floor(mid + half - 2));
      const vert = b.kind === "bar" ? clamp01(Math.min(py - b.y, b.y + b.h - py) + 0.5) : 1;
      if (in1 >= in0) pink.w32.fill(word(art.spot, Math.round(255 * vert)), y * W + in0, y * W + in1 + 1);
      for (let x = x0; x <= x1; x++) {
        if (x >= in0 && x <= in1) continue;
        const a = blockCover(b, x + 0.5 - off, py);
        const i = ((y * W + x) << 2) + 3;
        if (a > 0) pinkA[i] = Math.max(pinkA[i] as number, a * 255);
      }
    }
    const [bx, by, bw, bh] = b.kind === "disc" ? [b.x - b.r, b.y - b.r, b.r * 2, b.r * 2] : [b.x, b.y, b.w, b.h];
    const n = Math.round((bw * bh) / (900 * dpr * dpr));
    for (let k = 0; k < n; k++) {
      const x = Math.floor(bx + off + hash2(k, 1, 5) * bw);
      const y = Math.floor(by + off + hash2(k, 2, 5) * bh);
      const r = Math.max(1, Math.round((0.4 + hash2(k, 3, 5) ** 3 * 1.2) * dpr));
      for (let yy = Math.max(0, y); yy < Math.min(H, y + r); yy++)
        for (let xx = Math.max(0, x); xx < Math.min(W, x + r); xx++) {
          const i = ((yy * W + xx) << 2) + 3;
          pinkA[i] = (pinkA[i] as number) * 0.35;
        }
    }
  }
  const inside = (x: number, y: number) => blocks.some((b) => blockCover(b, x, y) >= 0.5);

  // One plate's dots, each stamped over its own box: on paper (or black)
  // a dot is ink; inside a block on the same drum it is a hole.
  const stamp = (plane: Uint8ClampedArray, angle: number, dx: number, cov: (x: number, y: number) => number, knock: boolean) => {
    const pts = screen(W, H, pitch, angle);
    const min = 0.3 * dpr;
    for (let k = 0; k < pts.length; k += 2) {
      const cx = (pts[k] as number) + dx;
      const cy = (pts[k + 1] as number) + dx;
      const r = dotRadius(cov(cx - dx, cy - dx), pitch);
      if (r < min) continue;
      const hole = knock && inside(cx - dx, cy - dx);
      const outer = (r + 0.5) ** 2;
      const inner = r > 0.5 ? (r - 0.5) ** 2 : -1;
      const x0 = Math.max(0, Math.floor(cx - r - 1));
      const x1 = Math.min(W - 1, Math.ceil(cx + r + 1));
      const y0 = Math.max(0, Math.floor(cy - r - 1));
      const y1 = Math.min(H - 1, Math.ceil(cy + r + 1));
      for (let y = y0; y <= y1; y++) {
        const ddy = y + 0.5 - cy;
        for (let x = x0; x <= x1; x++) {
          const ddx = x + 0.5 - cx;
          const d2 = ddx * ddx + ddy * ddy;
          if (d2 >= outer) continue;
          const a = d2 <= inner ? 1 : Math.min(1, r - Math.sqrt(d2) + 0.5);
          const i = ((y * W + x) << 2) + 3;
          const was = plane[i] as number;
          plane[i] = hole ? was * (1 - a * 0.97) : was + (255 - was) * a;
        }
      }
    }
  };
  stamp(pink.a, 15, off, (x, y) => cut(ink(x, y)) * mottle(x, y, 2), true);
  if (key !== null) stamp(key.a, 45, 0, (x, y) => clamp01((ink(x, y) - 0.6) / 0.4) * 0.85 * mottle(x, y, 9), false);

  // The press's margin: no ink within it on either plate.
  const m = Math.round(art.margin * dpr);
  if (m > 0)
    for (const l of key === null ? [pink] : [pink, key]) {
      const clear = word([0, 0, 0], 0);
      l.w32.fill(clear, 0, Math.min(W * H, m * W));
      l.w32.fill(clear, Math.max(0, (H - m) * W));
      for (let y = m; y < H - m; y++) {
        l.w32.fill(clear, y * W, y * W + m);
        l.w32.fill(clear, y * W + W - m, y * W + W);
      }
    }

  // The press: the stock, the pink over it, and black multiplied on top.
  fill(ctx, art.ground);
  ctx.fillRect(0, 0, W, H);
  const print = (l: { a: Uint8ClampedArray }, mode: GlobalCompositeOperation) => {
    const c = scratch(W, H);
    context(c).putImageData(new ImageData(l.a as Uint8ClampedArray<ArrayBuffer>, W, H), 0, 0);
    ctx.save();
    ctx.globalCompositeOperation = mode;
    ctx.drawImage(c, 0, 0);
    ctx.restore();
  };
  print(pink, "source-over");
  if (key !== null) print(key, "multiply");
  // Fibre in the stock, over the ink too.
  grain(ctx, W, H, art.onBlack ? 0.05 : 0.07, 3);
}
