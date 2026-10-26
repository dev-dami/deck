// The head of a PNG of w × h pixels (its signature and IHDR chunk), padded
// to `bytes`: enough for main's checks, which read only the header.
export function fakePng(w: number, h = w, bytes = 64): Uint8Array {
  const b = new Uint8Array(Math.max(bytes, 33));
  b.set([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const v = new DataView(b.buffer);
  v.setUint32(8, 13);
  b.set([0x49, 0x48, 0x44, 0x52], 12);
  v.setUint32(16, w);
  v.setUint32(20, h);
  // 8 bits a channel, RGBA.
  b[24] = 8;
  b[25] = 6;
  return b;
}
