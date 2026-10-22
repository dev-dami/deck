import { describe, expect, test } from "bun:test";
import { buildTheme, contrast, isThemeId, mix, PALETTES, palette, rgba } from "./themes";

const HEX = /^#[0-9A-Fa-f]{6}$/;

describe("themes", () => {
  test("ships every CLI and desktop theme once", () => {
    const ids = PALETTES.map((p) => p.id);
    expect(new Set(ids).size).toBe(ids.length);
    expect(ids).toEqual([
      "deck",
      "deck-light",
      "t3",
      "tokyo-night",
      "catppuccin-mocha",
      "catppuccin-latte",
      "nord",
      "rose-pine",
      "dracula",
      "gruvbox-dark",
      "solarized-light",
      "github-light",
    ]);
  });

  test("palette colors are plain hex", () => {
    for (const p of PALETTES)
      for (const key of [
        "bg",
        "raise",
        "fg",
        "muted",
        "primary",
        "onPrimary",
        "warn",
        "danger",
        "success",
        "info",
      ] as const)
        expect(p[key]).toMatch(HEX);
  });

  // Text the app actually draws must stay readable in every theme.
  test.each(PALETTES.map((p) => [p.name, p] as const))("%s keeps text readable", (_, p) => {
    const t = buildTheme(p);
    expect(contrast(t.fg, t.bg)).toBeGreaterThanOrEqual(7);
    expect(contrast(t.fg, t.card)).toBeGreaterThanOrEqual(7);
    expect(contrast(t.muted, t.card)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(t.muted, t.bg)).toBeGreaterThanOrEqual(4.5);
    expect(contrast(t.fg, t.chip)).toBeGreaterThanOrEqual(7);
    expect(contrast(t.onPrimary, t.primary)).toBeGreaterThanOrEqual(3.5);
  });

  test("dark surfaces step up from the page; light cards sit on a tinted page", () => {
    const dark = buildTheme(palette("deck"));
    expect(contrast(dark.card, dark.bg)).toBeGreaterThan(1);
    expect(contrast(dark.chip, dark.bg)).toBeGreaterThan(contrast(dark.card, dark.bg));
    const light = buildTheme(palette("github-light"));
    expect(light.card).toBe("#FFFFFF");
    expect(light.bg).not.toBe(light.card);
  });

  test("color helpers", () => {
    expect(mix("#000000", "#FFFFFF", 0.5)).toBe("#808080");
    expect(rgba("#5FD7AF", 0.2)).toBe("rgba(95,215,175,0.2)");
    expect(contrast("#000000", "#FFFFFF")).toBeCloseTo(21, 5);
    expect(isThemeId("nord")).toBe(true);
    expect(isThemeId("solarized-dark")).toBe(false);
    expect(palette("nord").name).toBe("Nord");
  });
});
