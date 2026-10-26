// Draws an app icon on a 2D canvas. Everything here takes the context and
// a size, so the picker's previews and the PNGs for main come from the same
// strokes. The shapes and numbers come from geometry.ts, which is tested.

import type { AppIconInfo, FlatStyle } from "./catalog";
import { darken, lighten, rgba } from "./color";
import {
  type Capsule,
  type Glyph,
  glyphBounds,
  glyphFor,
  type IconPlatform,
  dotRadius,
  screenPoints,
  seeded,
  STITCH_GRID,
  stitchChart,
  type Tier,
  type Tile,
  tierFor,
  tileFor,
} from "./geometry";

export type Ctx = CanvasRenderingContext2D | OffscreenCanvasRenderingContext2D;

// A scratch canvas of the same size, for a pass that is masked before it
// lands (the halftone).
export type LayerFactory = (w: number, h: number) => Ctx;

export const offscreenLayer: LayerFactory = (w, h) => {
  const ctx = new OffscreenCanvas(w, h).getContext("2d");
  if (ctx === null) throw new Error("no 2d context");
  return ctx;
};

interface Scene {
  ctx: Ctx;
  size: number;
  tile: Tile;
  glyph: Glyph;
  tier: Tier;
  platform: IconPlatform;
  // One unit of the original 928-wide tile, in pixels.
  u: number;
  layer: LayerFactory;
}

export function drawAppIcon(
  ctx: Ctx,
  icon: AppIconInfo,
  size: number,
  platform: IconPlatform,
  layer: LayerFactory = offscreenLayer,
): void {
  const tile = tileFor(size, platform);
  const s: Scene = { ctx, size, tile, glyph: glyphFor(tile, size), tier: tierFor(size), platform, u: tile.w / 928, layer };
  ctx.clearRect(0, 0, size, size);
  if (platform === "mac") macShadow(s);
  ctx.save();
  tilePath(ctx, tile, platform);
  ctx.clip();
  const style = icon.style;
  switch (style.kind) {
    case "flat":
      drawFlat(s, style);
      break;
    case "glass":
      drawGlass(s);
      break;
    case "riso":
      drawRiso(s, style.ground);
      break;
    case "stitch":
      drawStitch(s, style.cloth);
      break;
  }
  ctx.restore();
}

// ---------------------------------------------------------------- shapes

// The tile. On a Mac its corners are continuous, as the system's icons
// are; elsewhere they are plain arcs.
export function tilePath(ctx: Ctx, t: Tile, platform: IconPlatform, inset = 0): void {
  const x = t.x + inset;
  const y = t.y + inset;
  const w = t.w - inset * 2;
  const r = Math.max(0, t.r - inset);
  ctx.beginPath();
  if (platform !== "mac") {
    ctx.roundRect(x, y, w, w, r);
    return;
  }
  // The continuous corner: each corner spans 1.53r of its edges and is
  // three cubic curves (the shape of UIKit's rounded rects).
  const k = Math.min(r, w / 2 / 1.5287);
  const corner = (cx: number, cy: number, sx: number, sy: number, first: boolean) => {
    // From the horizontal edge into the vertical one, mirrored by sx, sy.
    const p = (dx: number, dy: number): [number, number] => [cx + sx * dx * k, cy + sy * dy * k];
    const pts: [number, number][] = [
      p(-1.52866483, 0),
      p(-1.08849323, 0),
      p(-0.86840689, 0),
      p(-0.66993427, 0.065496),
      p(-0.37820847, 0.18065549),
      p(-0.18065555, 0.37820853),
      p(-0.06549569, 0.66993493),
      p(0, 0.86840701),
      p(0, 1.08849299),
      p(0, 1.52866483),
    ];
    // A corner reached against its own direction runs backwards.
    const seq = first ? pts : [...pts].reverse();
    const [s0, ...rest] = seq;
    if (s0 === undefined) return;
    ctx.lineTo(s0[0], s0[1]);
    for (let i = 0; i + 2 < rest.length; i += 3) {
      const [a, b, c] = [rest[i], rest[i + 1], rest[i + 2]] as [[number, number], [number, number], [number, number]];
      ctx.bezierCurveTo(a[0], a[1], b[0], b[1], c[0], c[1]);
    }
  };
  ctx.moveTo(x + w / 2, y);
  corner(x + w, y, 1, 1, true);
  corner(x + w, y + w, 1, -1, false);
  corner(x, y + w, -1, -1, true);
  corner(x, y, -1, 1, false);
  ctx.closePath();
}

// One capsule as a closed subpath, wound clockwise, so capsules that
// overlap fill as one shape.
function capsulePath(ctx: Ctx, c: Capsule, dx = 0, dy = 0): void {
  const a = Math.atan2(c.by - c.ay, c.bx - c.ax);
  const h = Math.PI / 2;
  ctx.moveTo(c.bx + dx + c.r * Math.cos(a - h), c.by + dy + c.r * Math.sin(a - h));
  ctx.arc(c.bx + dx, c.by + dy, c.r, a - h, a + h);
  ctx.arc(c.ax + dx, c.ay + dy, c.r, a + h, a + 3 * h);
  ctx.closePath();
}

function chevronPath(ctx: Ctx, g: Glyph, dx = 0, dy = 0): void {
  ctx.beginPath();
  capsulePath(ctx, g.chevron[0], dx, dy);
  capsulePath(ctx, g.chevron[1], dx, dy);
}

function cursorPath(ctx: Ctx, g: Glyph, dx = 0, dy = 0): void {
  ctx.beginPath();
  capsulePath(ctx, g.cursor, dx, dy);
}

function glyphPath(ctx: Ctx, g: Glyph, dx = 0, dy = 0): void {
  ctx.beginPath();
  capsulePath(ctx, g.chevron[0], dx, dy);
  capsulePath(ctx, g.chevron[1], dx, dy);
  capsulePath(ctx, g.cursor, dx, dy);
}

function vertical(ctx: Ctx, y1: number, y2: number, stops: [number, string][]): CanvasGradient {
  const gr = ctx.createLinearGradient(0, y1, 0, y2);
  for (const [at, c] of stops) gr.addColorStop(at, c);
  return gr;
}

// The shadow a Mac draws under every app icon: short, soft and below.
function macShadow(s: Scene): void {
  const { ctx, size, tile, platform } = s;
  ctx.save();
  ctx.shadowColor = "rgba(0, 0, 0, 0.32)";
  ctx.shadowBlur = Math.max(1, size * 0.024);
  ctx.shadowOffsetY = Math.max(0.5, size * 0.011);
  tilePath(ctx, tile, platform);
  ctx.fillStyle = "#000";
  ctx.fill();
  ctx.restore();
}

// Fine grain over the whole tile: a few pixels lighter or darker.
function grain(s: Scene, seed: number, amount: number, light: string, dark: string): void {
  const { ctx, tile } = s;
  const rand = seeded(seed);
  const n = Math.round(tile.w * tile.w * amount);
  const dot = Math.max(1, s.size / 512);
  for (let i = 0; i < n; i++) {
    ctx.fillStyle = rand() < 0.5 ? light : dark;
    ctx.fillRect(tile.x + rand() * tile.w, tile.y + rand() * tile.w, dot, dot);
  }
}

// A hairline just inside the tile's edge: light catching it from above on a
// dark tile, a soft rule on a light one, so a white tile keeps its shape
// on a white taskbar.
function rim(s: Scene, light: boolean): void {
  const { ctx, tile, size, platform } = s;
  const w = size <= 32 ? 1 : Math.max(1, s.u * 2.5);
  ctx.save();
  tilePath(ctx, tile, platform, w / 2);
  ctx.lineWidth = w;
  ctx.strokeStyle = light
    ? vertical(ctx, tile.y, tile.y + tile.w, [
        [0, "rgba(0, 0, 0, 0.07)"],
        [1, "rgba(0, 0, 0, 0.16)"],
      ])
    : vertical(ctx, tile.y, tile.y + tile.w, [
        [0, "rgba(255, 255, 255, 0.16)"],
        [0.25, "rgba(255, 255, 255, 0.05)"],
        [1, "rgba(255, 255, 255, 0.03)"],
      ]);
  ctx.stroke();
  ctx.restore();
}

// ---------------------------------------------------------------- flat

function drawFlat(s: Scene, st: FlatStyle): void {
  const { ctx, tile, glyph, tier } = s;
  ctx.fillStyle = vertical(ctx, tile.y, tile.y + tile.w, [
    [0, st.tile[0]],
    [1, st.tile[1]],
  ]);
  ctx.fillRect(tile.x, tile.y, tile.w, tile.w);
  const b = glyphBounds(glyph);
  ctx.save();
  if (tier === "full") {
    // The mark sits on the tile, a little raised.
    ctx.shadowColor = st.light ? "rgba(20, 30, 60, 0.16)" : "rgba(0, 0, 0, 0.4)";
    ctx.shadowBlur = s.u * 16;
    ctx.shadowOffsetY = s.u * 7;
  }
  chevronPath(ctx, glyph);
  ctx.fillStyle = vertical(ctx, b.y1, b.y2, [
    [0, st.chevron[0]],
    [1, st.chevron[1]],
  ]);
  ctx.fill();
  cursorPath(ctx, glyph);
  ctx.fillStyle = st.cursor;
  ctx.fill();
  ctx.restore();
  rim(s, st.light);
}

// ---------------------------------------------------------------- glass

// A dusk sky behind frosted glass: deep blue at the top, the last light
// low on the horizon, a ridge of hills blurred by the glass. The mark is
// bright, lifted off the glass by its shadow.
function drawGlass(s: Scene): void {
  const { ctx, tile, glyph, tier, u } = s;
  const top = tile.y;
  const h = tile.w;
  ctx.fillStyle = vertical(ctx, top, top + h, [
    [0, "#14203f"],
    [0.45, "#253a6e"],
    [0.75, "#4f5f97"],
    [0.9, "#8a83ac"],
    [1, "#c49ea5"],
  ]);
  ctx.fillRect(tile.x, top, h, h);
  if (tier !== "micro") {
    // Hills on the horizon, softened by the frost.
    ctx.save();
    if (tier === "full") ctx.filter = `blur(${Math.round(u * 14)}px)`;
    ctx.beginPath();
    ctx.moveTo(tile.x - u * 40, top + h);
    const base = top + h * 0.83;
    ctx.lineTo(tile.x - u * 40, base);
    ctx.bezierCurveTo(tile.x + h * 0.2, base - h * 0.07, tile.x + h * 0.38, base - h * 0.02, tile.x + h * 0.55, base + h * 0.01);
    ctx.bezierCurveTo(tile.x + h * 0.72, base + h * 0.03, tile.x + h * 0.86, base - h * 0.06, tile.x + h + u * 40, base - h * 0.03);
    ctx.lineTo(tile.x + h + u * 40, top + h + u * 40);
    ctx.closePath();
    ctx.fillStyle = "rgba(24, 28, 64, 0.62)";
    ctx.fill();
    ctx.restore();
  }
  if (tier === "full") {
    // The frost: a pale veil and a fine grain.
    ctx.fillStyle = "rgba(220, 230, 255, 0.07)";
    ctx.fillRect(tile.x, top, h, h);
    grain(s, 7, 0.05, "rgba(255, 255, 255, 0.07)", "rgba(10, 16, 40, 0.08)");
    // Sheen on the upper half of the glass.
    ctx.fillStyle = vertical(ctx, top, top + h * 0.5, [
      [0, "rgba(255, 255, 255, 0.14)"],
      [1, "rgba(255, 255, 255, 0)"],
    ]);
    ctx.fillRect(tile.x, top, h, h * 0.5);
  }
  const b = glyphBounds(glyph);
  ctx.save();
  if (tier !== "micro") {
    ctx.shadowColor = "rgba(6, 10, 34, 0.5)";
    ctx.shadowBlur = Math.max(1, u * 22);
    ctx.shadowOffsetY = Math.max(1, u * 12);
  }
  chevronPath(ctx, glyph);
  ctx.fillStyle = vertical(ctx, b.y1, b.y2, [
    [0, "#ffffff"],
    [1, "#dfe5fb"],
  ]);
  ctx.fill();
  cursorPath(ctx, glyph);
  ctx.fillStyle = vertical(ctx, b.y1, b.y2, [
    [0, "#ffd3a1"],
    [1, "#ffb571"],
  ]);
  ctx.fill();
  ctx.restore();
  // The glass's edge: bright where the light comes from, and a second,
  // fainter catch along the bottom, as thick glass shows.
  const w = s.size <= 32 ? 1 : Math.max(1, u * 3);
  ctx.save();
  tilePath(ctx, tile, s.platform, w / 2);
  ctx.lineWidth = w;
  ctx.strokeStyle = vertical(ctx, top, top + h, [
    [0, "rgba(255, 255, 255, 0.55)"],
    [0.18, "rgba(255, 255, 255, 0.12)"],
    [0.8, "rgba(255, 255, 255, 0.06)"],
    [1, "rgba(255, 255, 255, 0.3)"],
  ]);
  ctx.stroke();
  ctx.restore();
}

// ---------------------------------------------------------------- riso

const RISO_PINK = "#ff48b0";

// One fluorescent pink ink. Large, the mark is a halftone screen that
// closes up where the tone is dark; a second impression of the same ink
// landed a little off register, so a fringe of dots shows at its lower
// right. Small, the mark is solid ink.
function drawRiso(s: Scene, ground: "black" | "paper"): void {
  const { ctx, tile, glyph, tier, size } = s;
  const paper = ground === "paper";
  ctx.fillStyle = paper ? "#f3ede1" : "#151315";
  ctx.fillRect(tile.x, tile.y, tile.w, tile.w);
  const ink = paper ? "#fa3d9f" : RISO_PINK;
  if (tier === "full") {
    grain(s, 11, 0.06, paper ? "rgba(255, 255, 255, 0.5)" : "rgba(255, 255, 255, 0.045)", paper ? "rgba(120, 100, 80, 0.12)" : "rgba(0, 0, 0, 0.3)");
    // The second impression slipped down and to the right; the first is
    // solid at the top of the mark and opens into dots toward its foot.
    const slip = { x: tile.w * 0.02, y: tile.w * 0.013 };
    halftone(s, ink, slip, 75, () => 0.26, 23);
    halftone(s, ink, { x: 0, y: 0 }, 45, (t) => 1.12 - t * 0.78, 5);
    return;
  }
  ctx.save();
  if (paper) ctx.globalCompositeOperation = "multiply";
  if (size >= 48) {
    // The off-register impression, in solid ink.
    const d = Math.round(size * 0.035);
    glyphPath(ctx, glyph, d, d * 0.66);
    ctx.fillStyle = rgba(ink, 0.45);
    ctx.fill();
  }
  glyphPath(ctx, glyph);
  ctx.fillStyle = ink;
  ctx.fill();
  ctx.restore();
}

// One impression of the mark as a halftone screen: dots on a grid turned
// by angle, sized by tone(t) where t runs from the top of the mark (0) to
// its bottom (1), and kept to the mark's shape moved by slip. The drum
// leaves specks where the ink did not take.
function halftone(s: Scene, ink: string, slip: { x: number; y: number }, angle: number, tone: (t: number) => number, seed: number): void {
  const { ctx, glyph, tile, size } = s;
  const cell = Math.max(3.2, tile.w / 70);
  const b = glyphBounds(glyph);
  const box = { x1: b.x1 + slip.x, y1: b.y1 + slip.y, x2: b.x2 + slip.x, y2: b.y2 + slip.y };
  const rand = seeded(seed);
  const l = s.layer(size, size);
  l.fillStyle = ink;
  for (const p of screenPoints(box, cell, angle)) {
    const r = dotRadius(tone((p.y - box.y1) / (box.y2 - box.y1)), cell) * (0.96 + rand() * 0.08);
    if (r < 0.35) continue;
    l.beginPath();
    l.arc(p.x, p.y, r, 0, Math.PI * 2);
    l.fill();
  }
  l.globalCompositeOperation = "destination-out";
  const specks = Math.round(((box.x2 - box.x1) * (box.y2 - box.y1)) / (cell * cell * 6));
  for (let i = 0; i < specks; i++) {
    l.beginPath();
    l.arc(box.x1 + rand() * (box.x2 - box.x1), box.y1 + rand() * (box.y2 - box.y1), cell * (0.08 + rand() * 0.14), 0, Math.PI * 2);
    l.fill();
  }
  l.globalCompositeOperation = "destination-in";
  glyphPath(l, glyph, slip.x, slip.y);
  l.fillStyle = "#000";
  l.fill();
  ctx.save();
  if (ink !== RISO_PINK) ctx.globalCompositeOperation = "multiply";
  ctx.drawImage(l.canvas, 0, 0);
  ctx.restore();
}

// ---------------------------------------------------------------- stitch

interface Thread {
  base: string;
  shade: string;
  shine: string;
}

const thread = (base: string): Thread => ({ base, shade: darken(base, 0.28), shine: lighten(base, 0.35) });

const CLOTH = {
  linen: { ground: "#e9dfca", hole: "rgba(96, 74, 40, 0.28)", weave: "rgba(120, 96, 60, 0.1)", chevron: thread("#c73a30"), cursor: thread("#2c2b33"), edge: thread("#c73a30") },
  denim: { ground: "#33507a", hole: "rgba(10, 20, 40, 0.35)", weave: "rgba(190, 210, 240, 0.08)", chevron: thread("#efa43a"), cursor: thread("#f3ead8"), edge: thread("#efa43a") },
} as const;

// Cross-stitch: the cloth's grid of holes, the mark worked in crosses of
// thread on it, and a running stitch around the tile's edge. Below 128px
// the stitches would blur into mush, so the mark is solid thread there.
function drawStitch(s: Scene, cloth: "linen" | "denim"): void {
  const { ctx, tile, glyph, tier, size } = s;
  const c = CLOTH[cloth];
  ctx.fillStyle = c.ground;
  ctx.fillRect(tile.x, tile.y, tile.w, tile.w);
  if (size < 128) {
    if (tier !== "micro") weave(s, c.weave, cloth, Math.max(2, tile.w / 24));
    chevronPath(ctx, glyph);
    ctx.fillStyle = c.chevron.base;
    ctx.fill();
    cursorPath(ctx, glyph);
    ctx.fillStyle = c.cursor.base;
    ctx.fill();
    if (size >= 48) runningStitch(s, c.edge);
    return;
  }
  const n = STITCH_GRID;
  const cell = tile.w / n;
  // Aida cloth: blocks of thread with a hole at each corner, where the
  // needle goes through.
  weave(s, c.weave, cloth, cell);
  grain(s, 3, 0.04, "rgba(255, 255, 255, 0.08)", "rgba(0, 0, 0, 0.06)");
  ctx.fillStyle = c.hole;
  for (let i = 1; i < n; i++) {
    for (let j = 1; j < n; j++) {
      ctx.beginPath();
      ctx.arc(tile.x + i * cell, tile.y + j * cell, cell * 0.1, 0, Math.PI * 2);
      ctx.fill();
    }
  }
  const cells = stitchChart();
  const rand = seeded(17);
  // Each cross: the under-stitch from bottom left to top right, then the
  // top stitch across it, each a strand with a shadow and a sheen.
  for (const pass of [0, 1] as const) {
    for (const k of cells) {
      const t = k.part === "chevron" ? c.chevron : c.cursor;
      const x = tile.x + k.col * cell;
      const y = tile.y + k.row * cell;
      const j = (rand() - 0.5) * cell * 0.04;
      const inset = cell * 0.16;
      const [x1, y1, x2, y2] =
        pass === 0 ? [x + inset, y + cell - inset, x + cell - inset, y + inset] : [x + inset, y + inset, x + cell - inset, y + cell - inset];
      strand(ctx, x1 + j, y1, x2 + j, y2, cell * 0.36, t, cell, pass === 0);
    }
  }
  runningStitch(s, c.edge);
}

function strand(ctx: Ctx, x1: number, y1: number, x2: number, y2: number, w: number, t: Thread, cell: number, under: boolean): void {
  ctx.save();
  ctx.lineCap = "round";
  ctx.shadowColor = "rgba(0, 0, 0, 0.28)";
  ctx.shadowBlur = cell * 0.08;
  ctx.shadowOffsetY = cell * 0.05;
  ctx.strokeStyle = under ? t.shade : t.base;
  ctx.lineWidth = w;
  ctx.beginPath();
  ctx.moveTo(x1, y1);
  ctx.lineTo(x2, y2);
  ctx.stroke();
  ctx.restore();
  // The sheen along the strand, off its centre toward the light.
  ctx.save();
  ctx.lineCap = "round";
  ctx.strokeStyle = rgba(t.shine, under ? 0.35 : 0.6);
  ctx.lineWidth = w * 0.26;
  ctx.beginPath();
  ctx.moveTo(x1 - w * 0.12, y1 - w * 0.08);
  ctx.lineTo(x2 - w * 0.12, y2 - w * 0.08);
  ctx.stroke();
  ctx.restore();
}

// The cloth's weave: linen as fine crossing threads, denim as a diagonal
// twill.
function weave(s: Scene, colour: string, cloth: "linen" | "denim", pitch: number): void {
  const { ctx, tile } = s;
  ctx.save();
  ctx.strokeStyle = colour;
  ctx.lineWidth = Math.max(0.5, pitch * 0.18);
  ctx.beginPath();
  if (cloth === "linen") {
    for (let v = tile.x + pitch; v < tile.x + tile.w; v += pitch) {
      ctx.moveTo(v, tile.y);
      ctx.lineTo(v, tile.y + tile.w);
      ctx.moveTo(tile.x, v - tile.x + tile.y);
      ctx.lineTo(tile.x + tile.w, v - tile.x + tile.y);
    }
  } else {
    const step = pitch / 3;
    for (let d = -tile.w; d < tile.w; d += step) {
      ctx.moveTo(tile.x + d, tile.y + tile.w);
      ctx.lineTo(tile.x + d + tile.w, tile.y);
    }
  }
  ctx.stroke();
  ctx.restore();
}

// A running stitch just inside the tile's edge.
function runningStitch(s: Scene, t: Thread): void {
  const { ctx, tile, size, platform } = s;
  const inset = tile.w * 0.06;
  const w = Math.max(1.2, tile.w * 0.016);
  const dash = Math.max(3, tile.w * 0.034);
  ctx.save();
  tilePath(ctx, tile, platform, inset);
  ctx.setLineDash([dash, dash * 0.75]);
  ctx.lineCap = "round";
  ctx.lineWidth = w;
  ctx.strokeStyle = t.base;
  if (size >= 128) {
    ctx.shadowColor = "rgba(0, 0, 0, 0.25)";
    ctx.shadowBlur = w * 0.6;
    ctx.shadowOffsetY = w * 0.35;
  }
  ctx.stroke();
  if (size >= 128) {
    ctx.shadowColor = "transparent";
    ctx.strokeStyle = rgba(t.shine, 0.45);
    ctx.lineWidth = w * 0.3;
    ctx.stroke();
  }
  ctx.restore();
}
