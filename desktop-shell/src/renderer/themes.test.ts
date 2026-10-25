import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { defaultPrefs } from "./prefs";
import { ACCENTS, THEMES, themeInfo } from "./themes";

const css = readFileSync(join(__dirname, "styles.css"), "utf8");

describe("themes", () => {
  test("Tabletop comes first and is the default", () => {
    expect(THEMES[0]).toMatchObject({ id: "tabletop", label: "Tabletop", mode: null });
    expect(defaultPrefs.palette).toBe("tabletop");
    expect(themeInfo("no-such-theme").id).toBe("tabletop");
  });

  test("T3 stays a choice that follows light or dark", () => {
    expect(themeInfo("default")).toMatchObject({ label: "T3", mode: null });
  });

  test("amber is the first accent colour", () => {
    expect(ACCENTS.map((a) => a.id).slice(0, 2)).toEqual(["theme", "amber"]);
  });

  test("ids are unique and each has four swatches", () => {
    expect(new Set(THEMES.map((t) => t.id)).size).toBe(THEMES.length);
    for (const t of THEMES) expect(t.swatch.every((c) => c !== "")).toBe(true);
  });

  test("styles.css has a block for every theme and accent", () => {
    for (const t of THEMES) expect(css).toContain(`[data-palette="${t.id}"]`);
    for (const a of ACCENTS.filter((x) => x.id !== "theme")) expect(css).toContain(`[data-accent="${a.id}"]`);
  });

  test("the Tabletop swatches match its tokens", () => {
    const [canvas, sidebar, text, signal] = themeInfo("tabletop").swatch;
    expect(css).toContain(`--canvas: ${canvas};`);
    expect(css).toContain(`--sidebar: ${sidebar};`);
    expect(css).toContain(`--text: ${text};`);
    expect(css).toContain(`--signal: ${signal};`);
  });
});
