import { describe, expect, test } from "vitest";
import { rowOffsets, visibleRange } from "./virtual";

describe("rowOffsets", () => {
  test("each row starts after the rows and gaps above it", () => {
    expect(rowOffsets([10, 20, 30], 0)).toEqual([0, 10, 30, 60]);
    expect(rowOffsets([10, 20, 30], 4)).toEqual([0, 14, 38, 72]);
    expect(rowOffsets([], 4)).toEqual([0]);
  });
});

describe("visibleRange", () => {
  const offsets = rowOffsets([10, 10, 10, 10, 10, 10, 10, 10, 10, 10], 0);

  test("the rows that cross the view, end exclusive", () => {
    expect(visibleRange(offsets, 15, 35, 0)).toEqual({ start: 1, end: 4 });
    // A row that ends at the top of the view is out; one that starts at its bottom too.
    expect(visibleRange(offsets, 10, 30, 0)).toEqual({ start: 1, end: 3 });
  });

  test("overscan adds rows on each side, inside the list", () => {
    expect(visibleRange(offsets, 15, 35, 2)).toEqual({ start: 0, end: 6 });
    expect(visibleRange(offsets, 85, 100, 3)).toEqual({ start: 5, end: 10 });
  });

  test("a view past the end shows the last row; an empty list shows none", () => {
    expect(visibleRange(offsets, 500, 600, 0)).toEqual({ start: 9, end: 10 });
    expect(visibleRange(offsets, -50, -10, 0)).toEqual({ start: 0, end: 1 });
    expect(visibleRange([0], 0, 100, 2)).toEqual({ start: 0, end: 0 });
  });

  test("rows of different heights", () => {
    const o = rowOffsets([100, 5, 5, 5, 300, 20], 10);
    // 0, 110, 125, 140, 155, 465, 495
    expect(visibleRange(o, 120, 160, 0)).toEqual({ start: 1, end: 5 });
    expect(visibleRange(o, 200, 400, 0)).toEqual({ start: 4, end: 5 });
  });
});
