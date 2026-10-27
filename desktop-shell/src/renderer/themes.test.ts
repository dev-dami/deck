import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { defaultPrefs, merge } from "./prefs";
import {
  ACCENTS,
  ASPECT_KEYS,
  type Aspects,
  CORNERS,
  DIFF_THEMES,
  DIRECTIONS,
  FACES,
  lookFromPalette,
  MATERIALS,
  nearestPreset,
  PRESET_IDS,
  PROCESSES,
  presetOf,
  setOf,
  TEXTURES,
  THEMES,
  themeInfo,
  TRANSLUCENCIES,
} from "./themes";

const css = readFileSync(join(__dirname, "styles.css"), "utf8");

describe("themes", () => {
  test("Liquid Glass comes first and is the default", () => {
    expect(THEMES[0]).toMatchObject({ id: "glass", label: "Liquid Glass", mode: null });
    expect(defaultPrefs.palette).toBe("glass");
    expect(themeInfo("no-such-theme").id).toBe("glass");
  });

  test("Apple stays a choice that follows light or dark", () => {
    expect(themeInfo("apple")).toMatchObject({ label: "Apple", mode: null });
  });

  test("Tabletop stays a choice that follows light or dark", () => {
    expect(themeInfo("tabletop")).toMatchObject({ label: "Tabletop", mode: null });
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

  test("diff themes: Match app theme comes first and is the default", () => {
    expect(DIFF_THEMES[0]).toMatchObject({ id: "match", label: "Match app theme" });
    expect(defaultPrefs.diffTheme).toBe("match");
    expect(new Set(DIFF_THEMES.map((t) => t.id)).size).toBe(DIFF_THEMES.length);
  });

  test("styles.css has a block for every diff theme, and each named one sets every token", () => {
    const tokens = ["keyword", "string", "comment", "number", "function", "type", "punct", "variable", "tag", "attr", "operator"].map(
      (t) => `--syn-${t}:`,
    );
    const diff = ["--diff-bg:", "--diff-fg:", "--diff-add:", "--diff-add-fg:", "--diff-del:", "--diff-del-fg:", "--diff-add-word:", "--diff-del-word:"];
    for (const t of DIFF_THEMES) {
      expect(css).toContain(`[data-diff-theme="${t.id}"]`);
      if (t.id === "match") continue;
      const block = css.slice(css.lastIndexOf(`:root[data-diff-theme="${t.id}"] .syn-scope {`)).split("}")[0] ?? "";
      for (const k of t.id === "monochrome" ? tokens : [...tokens, ...diff]) expect(block, `${t.id} ${k}`).toContain(k);
    }
    // The derived tints and the gutter apply to every diff.
    expect(css).toContain("--diff-gutter:");
  });

  test("every window theme sets its own syntax colours for Match app theme", () => {
    // A dark block names the theme in its selector, alone or in :is().
    const dark = (id: string) =>
      css.split("}").filter((b) => {
        const sel = b.slice(0, b.indexOf("{"));
        return sel.includes(`[data-palette="${id}"]`) && sel.includes("[data-mode]");
      });
    for (const t of THEMES.filter((x) => x.id !== "tabletop")) {
      expect(dark(t.id).some((b) => b.includes("--syn-keyword:")), t.id).toBe(true);
    }
  });

  test("the Apple swatches match its tokens in dark and light", () => {
    const info = themeInfo("apple");
    for (const [canvas, sidebar, text, signal] of [info.swatch, info.light ?? info.swatch]) {
      expect(css).toContain(`--canvas: ${canvas};`);
      expect(css).toContain(`--sidebar: ${sidebar};`);
      expect(css).toContain(`--text: ${text};`);
      expect(css).toContain(`--signal: ${signal};`);
    }
  });

  test("the Liquid Glass swatches match its tokens in dark and light", () => {
    const info = themeInfo("glass");
    for (const [canvas, , text, signal] of [info.swatch, info.light ?? info.swatch]) {
      expect(css).toContain(`--canvas: ${canvas};`);
      expect(css).toContain(`--text: ${text};`);
      expect(css).toContain(`--signal: ${signal};`);
    }
    // The second swatch is the glass over the backdrop, not a token.
    expect(css).toContain(`--backdrop: ${info.light?.[1]};`);
  });

  test("the Tabletop swatches match its tokens", () => {
    const [canvas, sidebar, text, signal] = themeInfo("tabletop").swatch;
    expect(css).toContain(`--canvas: ${canvas};`);
    expect(css).toContain(`--sidebar: ${sidebar};`);
    expect(css).toContain(`--text: ${text};`);
    expect(css).toContain(`--signal: ${signal};`);
  });
});

describe("aspects and presets", () => {
  const glass = DIRECTIONS.glass.look;

  test("Liquid Glass is the default look, and each preset names only known choices", () => {
    expect(PRESET_IDS).toEqual(["glass", "riso", "stitch"]);
    for (const k of ASPECT_KEYS) expect(defaultPrefs[k], k).toBe(glass[k]);
    for (const id of PRESET_IDS) {
      const l = DIRECTIONS[id].look;
      expect(themeInfo(l.palette).id).toBe(l.palette);
      expect(MATERIALS.map((x) => x.id)).toContain(l.material);
      expect(PROCESSES.map((x) => x.id)).toContain(l.process);
      expect(FACES.map((x) => x.id)).toContain(l.face);
      expect(TEXTURES.map((x) => x.id)).toContain(l.texture);
      expect(l).toMatchObject({ corners: "auto", translucency: "auto" });
    }
    expect(DIRECTIONS.riso.look).toMatchObject({ palette: "riso", material: "paper", process: "halftone", face: "space-grotesk", texture: "fibre" });
    expect(DIRECTIONS.stitch.look).toMatchObject({ palette: "stitch", material: "felt", process: "stitch", face: "pixelify", texture: "felt" });
  });

  test("a preset is itself; changing any one aspect makes it Custom", () => {
    for (const id of PRESET_IDS) {
      const l = DIRECTIONS[id].look;
      expect(presetOf(l)).toBe(id);
      expect(presetOf({ ...l, material: l.material === "plain" ? "glass" : "plain" })).toBe("custom");
      expect(presetOf({ ...l, corners: "round" })).toBe("custom");
      expect(presetOf({ ...l, palette: "nord" })).toBe("custom");
    }
    // Riso's inks on glass, and felt in T3's colours.
    expect(presetOf({ ...glass, palette: "riso" })).toBe("custom");
    expect(presetOf({ ...DIRECTIONS.stitch.look, palette: "default" })).toBe("custom");
  });

  test("Custom says which preset it was mixed from", () => {
    expect(nearestPreset({ ...glass, palette: "riso" })).toBe("glass");
    expect(nearestPreset({ ...DIRECTIONS.stitch.look, palette: "default" })).toBe("stitch");
    expect(nearestPreset({ ...DIRECTIONS.riso.look, translucency: "solid", face: "system" })).toBe("riso");
    // Half one, half another: the material decides.
    const mixed: Aspects = { palette: "riso", material: "felt", process: "halftone", face: "pixelify", texture: "felt", corners: "auto", translucency: "auto" };
    expect(nearestPreset(mixed)).toBe("stitch");
    // Nothing in common with any: Liquid Glass.
    expect(nearestPreset({ palette: "nord", material: "plain", process: "none", face: "system", texture: "none", corners: "round", translucency: "solid" })).toBe("glass");
  });

  test("a theme saved before the aspects becomes the aspects it stood for", () => {
    expect(lookFromPalette("riso")).toEqual(DIRECTIONS.riso.look);
    expect(lookFromPalette("stitch")).toEqual(DIRECTIONS.stitch.look);
    expect(lookFromPalette("glass")).toEqual(glass);
    // A colourway was Liquid Glass in its colours.
    expect(lookFromPalette("tokyo-night")).toEqual({ ...glass, palette: "tokyo-night" });
    expect(lookFromPalette("apple")).toEqual({ ...glass, palette: "apple" });
    expect(lookFromPalette("gone")).toEqual(glass);
  });

  test("loading old prefs migrates the palette; new prefs keep each aspect", () => {
    expect(merge({ palette: "riso", accent: "green" })).toMatchObject({ ...DIRECTIONS.riso.look, accent: "green" });
    expect(merge({ palette: "stitch" })).toMatchObject(DIRECTIONS.stitch.look);
    expect(merge({ palette: "tokyo-night" })).toMatchObject({ ...glass, palette: "tokyo-night" });
    expect(merge({})).toMatchObject(glass);
    // Once saved with aspects, the palette is the colours alone.
    const mixed = { palette: "riso", material: "glass", process: "dither", face: "inter-tight", texture: "none", corners: "round", translucency: "frosted" };
    expect(merge(mixed)).toMatchObject(mixed);
    expect(merge({ palette: "riso", material: "glass" })).toMatchObject({ palette: "riso", material: "glass", process: "dither", face: "inter-tight" });
    // Unknown values fall back.
    expect(merge({ palette: "riso", material: "wood", process: "etch", corners: 4 })).toMatchObject({ material: "glass", process: "dither", corners: "auto" });
  });

  test("the layout's prefs load with their defaults, and the sidebar's width stays in range", () => {
    expect(merge({})).toMatchObject({ sidebarSide: "left", panelDock: "opposite", density: "comfortable", sidebarWidth: 256 });
    expect(merge({ sidebarSide: "right", panelDock: "float", density: "compact", sidebarWidth: 300 })).toMatchObject({
      sidebarSide: "right",
      panelDock: "float",
      density: "compact",
      sidebarWidth: 300,
    });
    expect(merge({ sidebarWidth: 9000 }).sidebarWidth).toBe(420);
    expect(merge({ sidebarWidth: 10 }).sidebarWidth).toBe(208);
    expect(merge({ sidebarSide: "top", panelDock: "under" })).toMatchObject({ sidebarSide: "left", panelDock: "opposite" });
  });

  test("each process prints from its own set of pictures", () => {
    expect(setOf("dither")).toBe("glass");
    expect(setOf("none")).toBe("glass");
    expect(setOf("halftone")).toBe("riso");
    expect(setOf("stitch")).toBe("stitch");
  });

  test("styles.css has a block for every material, texture, face and knob, each in any theme", () => {
    for (const m of MATERIALS.filter((x) => x.id !== "glass")) expect(css).toContain(`[data-material="${m.id}"][data-mode]`);
    // Glass is the default; the editor's swatch can ask for it back.
    expect(css).toContain('.look[data-material="glass"]');
    for (const t of TEXTURES) expect(css).toContain(`[data-texture="${t.id}"]`);
    for (const f of FACES.filter((x) => x.id !== "inter-tight")) expect(css).toContain(`[data-face="${f.id}"][data-mode]`);
    for (const c of CORNERS.filter((x) => x.id !== "auto")) expect(css).toContain(`[data-corners="${c.id}"][data-mode]`);
    for (const t of TRANSLUCENCIES.filter((x) => x.id !== "auto")) expect(css).toContain(`[data-translucency="${t.id}"]`);
    expect(css).toContain(`[data-process="stitch"][data-mode] body`);
    expect(css).toContain(`[data-density="compact"]`);
    // A theme's block is its colours alone: no material, face or texture
    // leaks back into Riso's or Stitch's.
    const blocks = css.split("}").filter((b) => /\[data-palette="(riso|stitch)"\]/.test(b.slice(0, b.indexOf("{"))));
    for (const b of blocks) for (const k of ["--glass-blur:", "--material-texture:", "--font-display:", "--panel-radius:"]) expect(b).not.toContain(k);
  });
});
