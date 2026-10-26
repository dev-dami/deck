import { describe, expect, test } from "vitest";
import { APP_ICON_LIMITS, APP_ICON_SIZES } from "../../shared/bridge";
import { THEMES } from "../themes";
import { APP_ICON_IDS, APP_ICONS, appIconInfo, iconForTheme, resolveAppIcon } from "./catalog";
import { contrast, mix, parseHex, toHex } from "./color";
import { hostOs, ICON_NOTE, iconPlatform, iconSizes } from "./platform";

describe("the catalog", () => {
  test("has every icon once, the classic one first", () => {
    expect(new Set(APP_ICON_IDS).size).toBe(APP_ICONS.length);
    expect(APP_ICON_IDS[0]).toBe("classic");
    expect(APP_ICON_IDS).toEqual(
      expect.arrayContaining(["classic", "glass", "riso", "riso-paper", "stitch", "stitch-denim", "apple", "t3", "github-light"]),
    );
    expect(APP_ICON_IDS).not.toContain("theme");
    expect(appIconInfo("nope").id).toBe("classic");
  });

  test("every flat icon's mark reads on its tile", () => {
    for (const i of APP_ICONS) {
      if (i.style.kind !== "flat") continue;
      const tile = mix(i.style.tile[0], i.style.tile[1], 0.5);
      const chevron = mix(i.style.chevron[0], i.style.chevron[1], 0.5);
      expect(contrast(chevron, tile), `${i.id} chevron`).toBeGreaterThanOrEqual(2.5);
      expect(contrast(i.style.cursor, tile), `${i.id} cursor`).toBeGreaterThanOrEqual(3);
      // A light tile is marked as one, so its edge is drawn darker.
      expect(i.style.light, i.id).toBe(contrast(tile, "#ffffff") < contrast(tile, "#000000"));
    }
  });
});

describe("which icon a theme wears", () => {
  test("every theme of the catalog has an icon of its own colours", () => {
    for (const t of THEMES) {
      for (const mode of ["light", "dark"] as const) {
        const id = iconForTheme(t.id, mode);
        expect(APP_ICON_IDS, `${t.id} ${mode}`).toContain(id);
      }
    }
    expect(iconForTheme("glass", "dark")).toBe("glass");
    expect(iconForTheme("tabletop", "light")).toBe("classic");
    expect(iconForTheme("default", "dark")).toBe("t3");
    expect(iconForTheme("tokyo-night", "dark")).toBe("tokyo-night");
  });

  test("Riso and Cross-stitch change ground with the mode", () => {
    expect(iconForTheme("riso", "dark")).toBe("riso");
    expect(iconForTheme("riso", "light")).toBe("riso-paper");
    expect(iconForTheme("stitch", "light")).toBe("stitch");
    expect(iconForTheme("stitch", "dark")).toBe("stitch-denim");
  });

  test("a theme this build does not know wears the classic icon", () => {
    for (const id of ["", "solarized-dark", "toString", "__proto__", "constructor"]) {
      expect(iconForTheme(id, "dark"), id).toBe("classic");
    }
  });

  test("the pref picks an icon by hand, or follows the theme", () => {
    expect(resolveAppIcon("theme", "nord", "dark")).toBe("nord");
    expect(resolveAppIcon("theme", "riso", "light")).toBe("riso-paper");
    expect(resolveAppIcon("stitch-denim", "nord", "dark")).toBe("stitch-denim");
    expect(resolveAppIcon("glass", "unknown", "light")).toBe("glass");
    // A pref naming an icon that is gone follows the theme.
    expect(resolveAppIcon("old-icon", "dracula", "dark")).toBe("dracula");
    expect(resolveAppIcon("old-icon", "unknown", "dark")).toBe("classic");
  });
});

describe("the platform", () => {
  test("is read from navigator.platform", () => {
    expect(hostOs("MacIntel")).toBe("mac");
    expect(hostOs("Win32")).toBe("windows");
    expect(hostOs("Linux x86_64")).toBe("linux");
    expect(hostOs("")).toBe("linux");
    expect(iconPlatform("mac")).toBe("mac");
    expect(iconPlatform("windows")).toBe("other");
  });

  test("each draws sizes main takes, and says what the icon cannot change", () => {
    for (const os of ["mac", "windows", "linux"] as const) {
      const sizes = iconSizes(os);
      expect(sizes.length).toBeGreaterThan(0);
      expect(sizes.every((s) => (APP_ICON_LIMITS.sizes as readonly number[]).includes(s))).toBe(true);
      expect(ICON_NOTE[os]).toMatch(/standard/);
    }
    expect(iconSizes("mac")).toEqual(APP_ICON_SIZES.mac);
    expect(iconSizes("windows")).toContain(16);
    expect(ICON_NOTE.mac).toMatch(/Finder/);
    expect(ICON_NOTE.windows).toMatch(/Pinned shortcuts/);
    expect(ICON_NOTE.linux).toMatch(/launcher/);
  });
});

test("colour arithmetic", () => {
  expect(parseHex("#ff8000")).toEqual([255, 128, 0]);
  expect(toHex([255, 128, 0])).toBe("#ff8000");
  expect(toHex([300, -4, 12.4])).toBe("#ff000c");
  expect(mix("#000000", "#ffffff", 0.5)).toBe("#808080");
  expect(contrast("#000000", "#ffffff")).toBeCloseTo(21, 5);
  expect(() => parseHex("red")).toThrow();
});
