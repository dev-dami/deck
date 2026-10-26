import { existsSync, readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { expect, test } from "vitest";
import { paintPlate, PLATES, plateFit } from "./plates";

test("a plate covers its box with whole cells of at least 2px, cropped by the focus", () => {
  expect(plateFit(1344, 440, 800, 366)).toEqual({ cell: 2, cols: 672, rows: 220, x: 64, y: 73 });
  expect(plateFit(1344, 440, 800, 366, 2, [0, 1])).toMatchObject({ x: 0, y: 146 });
  // A box wider than the plate at 2px takes bigger cells, never a fraction.
  expect(plateFit(2400, 500, 800, 366)).toMatchObject({ cell: 3, cols: 800, rows: 167 });
  // A tall box: the height decides.
  expect(plateFit(400, 1200, 560, 448)).toMatchObject({ cell: 3, cols: 134, rows: 400 });
  // An empty box still shows a cell.
  expect(plateFit(0, 0, 10, 10)).toMatchObject({ cols: 1, rows: 1 });
});

test("paints the window's bits in the two tones", () => {
  // 3×2 plate, window of 2×2 from x=1.
  const bits = new Uint8Array([0, 1, 0, 1, 0, 1]);
  const px = paintPlate(bits, 3, { cell: 2, cols: 2, rows: 2, x: 1, y: 0 }, [0, 0, 0, 0], [9, 9, 9, 255]);
  expect([...px]).toEqual([9, 9, 9, 255, 0, 0, 0, 0, 0, 0, 0, 0, 9, 9, 9, 255]);
});

test("every plate is on disk at the size it claims", () => {
  for (const p of Object.values(PLATES)) {
    const file = fileURLToPath(new URL(`./art/plates/${p.name}.png`, import.meta.url));
    expect(existsSync(file), p.name).toBe(true);
    const png = readFileSync(file);
    expect([png.readUInt32BE(16), png.readUInt32BE(20)], p.name).toEqual([p.w, p.h]);
  }
});
