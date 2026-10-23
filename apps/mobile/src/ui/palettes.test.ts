import { describe, expect, test } from "bun:test";
import {
  ACCENTS,
  DEFAULT_APPEARANCE,
  mix,
  oklchToHex,
  resolveTokens,
  THEMES,
  themeInfo,
} from "./palettes";

describe("oklchToHex", () => {
  test("maps the achromatic extremes", () => {
    expect(oklchToHex(1, 0, 0)).toBe("#ffffff");
    expect(oklchToHex(0, 0, 0)).toBe("#000000");
  });

  test("lands near sRGB red for red's Oklch coordinates", () => {
    const hex = oklchToHex(0.628, 0.2577, 29.23);
    const [r, g, b] = [1, 3, 5].map((at) => Number.parseInt(hex.slice(at, at + 2), 16)) as [
      number,
      number,
      number,
    ];
    expect(r).toBeGreaterThan(250);
    expect(g).toBeLessThan(6);
    expect(b).toBeLessThan(6);
  });

  test("always yields a #rrggbb string, even for out-of-gamut input", () => {
    for (const accent of ACCENTS) {
      if (accent.oklch !== null) expect(oklchToHex(...accent.oklch)).toMatch(/^#[0-9a-f]{6}$/);
    }
    expect(oklchToHex(0.7, 0.4, 140)).toMatch(/^#[0-9a-f]{6}$/);
  });
});

describe("mix", () => {
  test("blends toward the second colour by the given share", () => {
    expect(mix("#000000", "#ffffff", 0.5)).toBe("#808080");
    expect(mix("#0a0a0a", "#ffffff", 0)).toBe("#0a0a0a");
    expect(mix("#0a0a0a", "#ffffff", 1)).toBe("#ffffff");
  });
});

describe("resolveTokens", () => {
  test("the default theme follows the system mode", () => {
    expect(resolveTokens(DEFAULT_APPEARANCE, "dark").background).toBe("#0a0a0a");
    expect(resolveTokens(DEFAULT_APPEARANCE, "light").background).toBe("#fcfcfc");
    expect(resolveTokens(DEFAULT_APPEARANCE, "light").mode).toBe("light");
  });

  test("a themed palette keeps its own mode whatever the system says", () => {
    const nord = resolveTokens({ theme: "nord", accent: "theme" }, "light");
    expect(nord.mode).toBe("dark");
    expect(nord.background).toBe("#2e3440");
    expect(nord.accent).toBe("#88c0d0");
    const solar = resolveTokens({ theme: "solarized-light", accent: "theme" }, "dark");
    expect(solar.mode).toBe("light");
    expect(solar.background).toBe("#fdf6e3");
  });

  test("a light palette inherits the light defaults it does not override", () => {
    const solar = resolveTokens({ theme: "solarized-light", accent: "theme" }, "dark");
    expect(solar.danger).toBe("#b91c1c");
    expect(solar.diffAdd).toBe("#e7f8ef");
  });

  test("an accent replaces the palette's primary", () => {
    const tokens = resolveTokens({ theme: "dracula", accent: "green" }, "dark");
    expect(tokens.accent).not.toBe("#bd93f9");
    expect(tokens.accent).toMatch(/^#[0-9a-f]{6}$/);
    expect(tokens.accentInk).toBe("#ffffff");
    expect(tokens.background).toBe("#282a36");
  });

  test("unknown ids fall back to the default theme and accent", () => {
    expect(resolveTokens({ theme: "nope", accent: "nope" }, "dark")).toEqual(
      resolveTokens(DEFAULT_APPEARANCE, "dark"),
    );
    expect(themeInfo("nope").id).toBe("default");
  });

  test("every catalog theme resolves to complete tokens", () => {
    for (const theme of THEMES) {
      const tokens = resolveTokens({ theme: theme.id, accent: "theme" }, "dark");
      for (const value of Object.values(tokens)) expect(value).toBeTruthy();
    }
  });
});
