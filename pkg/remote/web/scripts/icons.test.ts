import { expect, test } from "bun:test";
import { crc32, renderIcon } from "./icons";

test("icons are valid, deterministic PNGs", () => {
  const a = renderIcon(64);
  const b = renderIcon(64);
  expect(Buffer.from(a).equals(Buffer.from(b))).toBe(true);
  expect([...a.slice(0, 8)]).toEqual([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]);
  const view = new DataView(a.buffer, a.byteOffset, a.byteLength);
  expect(view.getUint32(16)).toBe(64);
  expect(view.getUint32(20)).toBe(64);
  expect(Buffer.from(renderIcon(64, true)).equals(Buffer.from(a))).toBe(false);
});

test("crc32 matches the reference value", () => {
  expect(crc32(new TextEncoder().encode("123456789"))).toBe(0xcbf43926);
});
