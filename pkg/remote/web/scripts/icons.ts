import { deflateSync } from "node:zlib";

// The launcher icon, shared with the desktop app: a terminal prompt on a
// deck of three cards. These are the icon's own colours, not the app palette
// (R6 is about the UI, which takes every colour from welcome.server.theme).
type RGB = readonly [number, number, number];

const BG: RGB = [0xf7, 0xf7, 0xf7];
const CARD = 0.635;
const RADIUS = 0.13;
const CARDS: ReadonlyArray<{ x: number; y: number; color: RGB }> = [
  { x: 0.318, y: 0.049, color: [0x8b, 0x8f, 0x9a] },
  { x: 0.201, y: 0.166, color: [0x3a, 0x3d, 0x46] },
  { x: 0.082, y: 0.283, color: [0x17, 0x18, 0x1d] },
];
// The prompt: a chevron and a cursor bar on the front card.
const CHEVRON: ReadonlyArray<readonly [number, number]> = [
  [0.241, 0.475],
  [0.371, 0.593],
  [0.241, 0.711],
];
const CHEVRON_HALF_WIDTH = 0.032;
const CHEVRON_COLOR: RGB = [0xf0, 0xa7, 0x40];
const BAR = { x: 0.435, y: 0.672, w: 0.164, h: 0.066 };
const BAR_COLOR: RGB = [0xf4, 0xf3, 0xee];
const SAMPLES = 3;

function inRoundedRect(
  u: number,
  v: number,
  c: { x: number; y: number; w: number; h: number },
  r: number,
): boolean {
  if (u < c.x || u > c.x + c.w || v < c.y || v > c.y + c.h) return false;
  const cx = Math.min(Math.max(u, c.x + r), c.x + c.w - r);
  const cy = Math.min(Math.max(v, c.y + r), c.y + c.h - r);
  return (u - cx) ** 2 + (v - cy) ** 2 <= r ** 2;
}

function nearSegment(
  u: number,
  v: number,
  a: readonly [number, number],
  b: readonly [number, number],
): number {
  const [ax, ay] = a;
  const [bx, by] = b;
  const dx = bx - ax;
  const dy = by - ay;
  const k = Math.min(1, Math.max(0, ((u - ax) * dx + (v - ay) * dy) / (dx * dx + dy * dy)));
  return Math.hypot(u - (ax + k * dx), v - (ay + k * dy));
}

function colorAt(u: number, v: number): RGB {
  let out = BG;
  for (const card of CARDS)
    if (inRoundedRect(u, v, { x: card.x, y: card.y, w: CARD, h: CARD }, RADIUS)) out = card.color;
  const [p0, p1, p2] = CHEVRON as [
    readonly [number, number],
    readonly [number, number],
    readonly [number, number],
  ];
  if (Math.min(nearSegment(u, v, p0, p1), nearSegment(u, v, p1, p2)) <= CHEVRON_HALF_WIDTH)
    out = CHEVRON_COLOR;
  if (inRoundedRect(u, v, BAR, BAR.h / 2)) out = BAR_COLOR;
  return out;
}

// Maskable icons keep the artwork inside the centre 80% so launchers can crop.
export function renderRaster(size: number, maskable: boolean): Uint8Array {
  const scale = maskable ? 0.8 : 0.92;
  const stride = size * 3 + 1;
  const raw = new Uint8Array(stride * size);
  for (let y = 0; y < size; y++) {
    raw[y * stride] = 0;
    for (let x = 0; x < size; x++) {
      let r = 0;
      let g = 0;
      let b = 0;
      for (let sy = 0; sy < SAMPLES; sy++) {
        for (let sx = 0; sx < SAMPLES; sx++) {
          const u = (x + (sx + 0.5) / SAMPLES) / size;
          const v = (y + (sy + 0.5) / SAMPLES) / size;
          const [cr, cg, cb] = colorAt(0.5 + (u - 0.5) / scale, 0.5 + (v - 0.5) / scale);
          r += cr;
          g += cg;
          b += cb;
        }
      }
      const n = SAMPLES * SAMPLES;
      const o = y * stride + 1 + x * 3;
      raw[o] = Math.round(r / n);
      raw[o + 1] = Math.round(g / n);
      raw[o + 2] = Math.round(b / n);
    }
  }
  return raw;
}

const CRC_TABLE = (() => {
  const t = new Uint32Array(256);
  for (let n = 0; n < 256; n++) {
    let c = n;
    for (let k = 0; k < 8; k++) c = c & 1 ? 0xedb88320 ^ (c >>> 1) : c >>> 1;
    t[n] = c >>> 0;
  }
  return t;
})();

export function crc32(bytes: Uint8Array): number {
  let c = 0xffffffff;
  for (const b of bytes) c = (CRC_TABLE[(c ^ b) & 0xff] ?? 0) ^ (c >>> 8);
  return (c ^ 0xffffffff) >>> 0;
}

function chunk(type: string, data: Uint8Array): Uint8Array {
  const out = new Uint8Array(12 + data.length);
  const view = new DataView(out.buffer);
  view.setUint32(0, data.length);
  for (let i = 0; i < 4; i++) out[4 + i] = type.charCodeAt(i);
  out.set(data, 8);
  view.setUint32(8 + data.length, crc32(out.subarray(4, 8 + data.length)));
  return out;
}

export function renderIcon(size: number, maskable = false): Uint8Array {
  const header = new Uint8Array(13);
  const hv = new DataView(header.buffer);
  hv.setUint32(0, size);
  hv.setUint32(4, size);
  header[8] = 8; // bit depth
  header[9] = 2; // truecolour RGB
  const idat = deflateSync(renderRaster(size, maskable), { level: 9 });
  const parts = [
    Uint8Array.of(0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a),
    chunk("IHDR", header),
    chunk("IDAT", idat),
    chunk("IEND", new Uint8Array(0)),
  ];
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let off = 0;
  for (const p of parts) {
    out.set(p, off);
    off += p.length;
  }
  return out;
}
