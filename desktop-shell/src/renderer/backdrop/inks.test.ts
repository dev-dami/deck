import { describe, expect, test } from "vitest";
import { artFor, SPECTRUM_PICTURES } from "./inks";
import { pictureInfo } from "./pictures";

describe("the spectrum looks' pictures", () => {
  test("each look prints a picture of its own: none is shared", () => {
    const pics = Object.values(SPECTRUM_PICTURES);
    expect(new Set(pics).size).toBe(pics.length);
    for (const [theme, id] of Object.entries(SPECTRUM_PICTURES)) {
      expect(pictureInfo(id), id).toBeDefined();
      for (const mode of ["dark", "light"] as const) expect(artFor(theme, mode, undefined, "window", "spectrum").picture).toBe(id);
    }
  });
});
