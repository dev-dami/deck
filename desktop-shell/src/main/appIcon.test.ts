import { mkdtempSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { APP_ICON_LIMITS, APP_ICON_SIZES } from "../shared/bridge";
import { fakePng } from "../../test/png";
import { AppIconFile, isAppIcon, pngSize, representations } from "./appIcon";

const set = (...sizes: number[]) => sizes.map((size) => ({ size, png: fakePng(size) }));

describe("pngSize", () => {
  test("reads the width and height of a PNG's header", () => {
    expect(pngSize(fakePng(32))).toEqual({ width: 32, height: 32 });
    expect(pngSize(fakePng(512, 256))).toEqual({ width: 512, height: 256 });
    // A Buffer and a view into a larger buffer read the same.
    expect(pngSize(Buffer.from(fakePng(16)))).toEqual({ width: 16, height: 16 });
    const big = new Uint8Array(200);
    big.set(fakePng(24), 100);
    expect(pngSize(big.subarray(100))).toEqual({ width: 24, height: 24 });
  });

  test("is null for anything else", () => {
    const jpeg = fakePng(32);
    jpeg.set([0xff, 0xd8, 0xff, 0xe0]);
    expect(pngSize(jpeg)).toBeNull();
    expect(pngSize(fakePng(32).subarray(0, 20))).toBeNull();
    const noIhdr = fakePng(32);
    noIhdr[12] = 0x69;
    expect(pngSize(noIhdr)).toBeNull();
    const longIhdr = fakePng(32);
    new DataView(longIhdr.buffer).setUint32(8, 14);
    expect(pngSize(longIhdr)).toBeNull();
    expect(pngSize(new TextEncoder().encode("<svg xmlns='http://www.w3.org/2000/svg'/>"))).toBeNull();
  });
});

describe("isAppIcon", () => {
  test("takes the sizes each platform's window draws", () => {
    expect(isAppIcon(set(...APP_ICON_SIZES.mac))).toBe(true);
    expect(isAppIcon(set(...APP_ICON_SIZES.other))).toBe(true);
    expect(isAppIcon(set(32))).toBe(true);
    for (const sizes of Object.values(APP_ICON_SIZES)) {
      expect(sizes.every((s) => (APP_ICON_LIMITS.sizes as readonly number[]).includes(s))).toBe(true);
    }
  });

  test("refuses anything that is not a set of PNGs of the sizes they claim", () => {
    const png = fakePng(32);
    const bad: unknown[] = [
      undefined,
      null,
      "png",
      {},
      [],
      [png],
      [{ size: 32 }],
      [{ png }],
      [{ size: 32, png, extra: true }],
      [{ size: "32", png }],
      // A size main does not take, and one used twice.
      [{ size: 33, png: fakePng(33) }],
      [{ size: 1024, png: fakePng(1024) }],
      [...set(32), ...set(32)],
      // The header says another size.
      [{ size: 32, png: fakePng(64) }],
      [{ size: 32, png: fakePng(32, 31) }],
      // Bytes that are not a PNG, or not bytes at all.
      [{ size: 32, png: new Uint8Array(64) }],
      [{ size: 32, png: [...png] }],
      [{ size: 32, png: png.buffer }],
      [{ size: 32, png: new Uint16Array(32) }],
      [{ size: 32, png: Buffer.from(png).toString("base64") }],
      // An object with a prototype other than Object's.
      [Object.assign(Object.create({ size: 1 }), { size: 32, png })],
    ];
    for (const v of bad) expect(isAppIcon(v), JSON.stringify(v)).toBe(false);
  });

  test("caps each PNG and all of them", () => {
    expect(isAppIcon([{ size: 512, png: fakePng(512, 512, APP_ICON_LIMITS.bytes) }])).toBe(true);
    expect(isAppIcon([{ size: 512, png: fakePng(512, 512, APP_ICON_LIMITS.bytes + 1) }])).toBe(false);
    const near = (size: number) => ({ size, png: fakePng(size, size, APP_ICON_LIMITS.bytes) });
    expect(isAppIcon([near(128), near(256), near(512)])).toBe(true);
    expect(isAppIcon([near(64), near(128), near(256), near(512)])).toBe(false);
    expect(isAppIcon(set(...APP_ICON_LIMITS.sizes))).toBe(true);
    expect(isAppIcon([...set(...APP_ICON_LIMITS.sizes), ...set(16)])).toBe(false);
  });
});

test("representations put the smallest image at scale 1 and the rest at their ratio to it", () => {
  const r = representations(set(64, 16, 32, 256));
  expect(r.map((x) => x.scaleFactor)).toEqual([1, 2, 4, 16]);
  expect(r.map((x) => pngSize(x.png)?.width)).toEqual([16, 32, 64, 256]);
  expect(representations(set(...APP_ICON_SIZES.mac)).map((x) => x.scaleFactor)).toEqual([1, 2, 4]);
});

describe("AppIconFile", () => {
  test("keeps the last icon for the next start", () => {
    const file = join(mkdtempSync(join(tmpdir(), "deck-icon-")), "data", "app-icon.json");
    expect(new AppIconFile(file).load()).toBeNull();
    const images = set(16, 32);
    new AppIconFile(file).save(images);
    expect(statSync(file).mode & 0o777).toBe(0o600);
    const back = new AppIconFile(file).load();
    expect(back?.map((i) => i.size)).toEqual([16, 32]);
    expect(back?.map((i) => [...i.png])).toEqual(images.map((i) => [...i.png]));
  });

  test("writes only when the icon changed", () => {
    const file = join(mkdtempSync(join(tmpdir(), "deck-icon-")), "app-icon.json");
    const icons = new AppIconFile(file);
    icons.save(set(16));
    writeFileSync(file, "marker");
    icons.save(set(16));
    expect(readFileSync(file, "utf8")).toBe("marker");
    icons.save(set(32));
    expect(new AppIconFile(file).load()?.map((i) => i.size)).toEqual([32]);
  });

  test("a file that is not a valid icon is ignored", () => {
    const dir = mkdtempSync(join(tmpdir(), "deck-icon-"));
    const file = join(dir, "app-icon.json");
    const write = (v: unknown) => writeFileSync(file, typeof v === "string" ? v : JSON.stringify(v));
    const b64 = (u: Uint8Array) => Buffer.from(u).toString("base64");
    for (const v of [
      "not json",
      [],
      { images: "x" },
      { images: [] },
      { images: [{ size: 32, png: b64(fakePng(64)) }] },
      { images: [{ size: 32, png: b64(new Uint8Array(40)) }] },
      { images: [{ size: 32, png: 7 }] },
      { images: [null] },
    ]) {
      write(v);
      expect(new AppIconFile(file).load(), JSON.stringify(v)).toBeNull();
    }
    write({ images: [{ size: 32, png: b64(fakePng(32)) }] });
    expect(new AppIconFile(file).load()?.[0]?.size).toBe(32);
  });

  test("a null file keeps nothing", () => {
    const icons = new AppIconFile(null);
    icons.save(set(16));
    expect(icons.load()).toBeNull();
  });
});
