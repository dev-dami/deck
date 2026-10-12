import { deflateSync } from "node:zlib";

// The launcher icon: three stacked cards, Deck's mark. These are the icon's
// own colours, not the app palette (R6 is about the UI, which takes every
// colour from welcome.server.theme).
type RGB = readonly [number, number, number];

const BG: RGB = [0x14, 0x18, 0x24];
const CARDS: ReadonlyArray<{ x: number; y: number; w: number; h: number; color: RGB }> = [
  { x: 0.24, y: 0.2, w: 0.4, h: 0.54, color: [0x4c, 0x5a, 0x8a] },
  { x: 0.31, y: 0.26, w: 0.4, h: 0.54, color: [0x74, 0x8f, 0xfc] },
  { x: 0.38, y: 0.32, w: 0.4, h: 0.54, color: [0xf1, 0xf3, 0xf9] },
];
const RADIUS = 0.05;
const SAMPLES = 3;

function inRoundedRect(
  u: number,
  v: number,
  c: { x: number; y: number; w: number; h: number },
): boolean {
  if (u < c.x || u > c.x + c.w || v < c.y || v > c.y + c.h) return false;
  const cx = Math.min(Math.max(u, c.x + RADIUS), c.x + c.w - RADIUS);
  const cy = Math.min(Math.max(v, c.y + RADIUS), c.y + c.h - RADIUS);
  return (u - cx) ** 2 + (v - cy) ** 2 <= RADIUS ** 2;
}

function colorAt(u: number, v: number): RGB {
  let out = BG;
  for (const card of CARDS) if (inRoundedRect(u, v, card)) out = card.color;
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
