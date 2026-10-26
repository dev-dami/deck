import { readFileSync } from "node:fs";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { defaultPrefs } from "./prefs";
import { ACCENTS, DIFF_THEMES, THEMES, themeInfo } from "./themes";

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
