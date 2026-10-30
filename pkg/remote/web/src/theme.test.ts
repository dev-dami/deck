import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import { decodeTheme } from "./protocol";
import {
  applyTheme,
  cssVariables,
  cssVarName,
  deckLook,
  kindColour,
  kindMark,
  kindVarName,
  luminance,
  schemeOf,
  type ThemeTarget,
} from "./theme";

function fixtureTheme(name: string) {
  const raw = readFileSync(
    join(import.meta.dir, "..", "..", "wire", "testdata", `${name}.json`),
    "utf8",
  );
  const t = decodeTheme(JSON.parse(raw));
  if (t === null) throw new Error(`bad theme fixture ${name}`);
  return t;
}

function recorder() {
  const vars = new Map<string, string>();
  const log: string[] = [];
  const target: ThemeTarget = {
    setProperty: (n, v) => {
      vars.set(n, v);
    },
    removeProperty: (n) => {
      vars.delete(n);
    },
    setScheme: (s) => log.push(`scheme:${s}`),
    setThemeColor: (h) => log.push(`color:${h}`),
  };
  return { vars, log, target };
}

describe("theme to CSS variables", () => {
  test("every palette token and kind tint of the fixture becomes a variable", () => {
    const theme = fixtureTheme("entity_theme");
    const vars = cssVariables(theme);
    expect(Object.keys(vars)).toHaveLength(
      Object.keys(theme.palette).length + Object.keys(theme.kinds).length,
    );
    expect(vars["--p-bg-base"]).toBe("#111416");
    expect(vars["--p-diff-insert-code-bg"]).toBe("#1b3326");
    expect(vars["--kind-claude"]).toBe("#d97757");
    expect(vars["--kind-codex"]).toBe("#8b7cf6");
  });

  test("names are derived, never guessed", () => {
    expect(cssVarName("fg_more_subtle")).toBe("--p-fg-more-subtle");
    expect(cssVarName("Bad Token")).toBeNull();
    expect(cssVarName("x;y")).toBeNull();
    expect(kindVarName("claude")).toBe("--kind-claude");
    expect(kindVarName("Open Code!")).toBe("--kind-open-code");
    expect(kindVarName("!!!")).toBeNull();
    expect(kindColour("claude")).toBe("var(--kind-claude, var(--p-fg-base))");
    expect(kindColour("!!!")).toBe("var(--p-fg-base)");
  });

  test("values that are not #rrggbb never reach the CSSOM", () => {
    const vars = cssVariables({
      palette: { primary: "red", accent: "#12345", ok: "#ABCDEF", bg_base: "url(x)" },
      kinds: { codex: { tint: "#fff", mark: ">_" }, claude: { tint: "#D97757", mark: "✻" } },
    });
    expect(vars).toEqual({ "--p-ok": "#abcdef", "--kind-claude": "#d97757" });
  });

  test("the scheme follows the base background", () => {
    expect(luminance("#000000")).toBe(0);
    expect(luminance("#ffffff")).toBeCloseTo(1);
    expect(schemeOf(fixtureTheme("entity_theme"))).toBe("dark");
    expect(schemeOf(fixtureTheme("event_theme.changed"))).toBe("light");
    expect(schemeOf({ palette: {}, kinds: {} })).toBeNull();
  });

  test("applying a new theme clears tokens the old one set", () => {
    const r = recorder();
    const first = applyTheme(r.target, fixtureTheme("entity_theme"), []);
    expect(r.vars.get("--p-keyword")).toBe("#c792ea");
    expect(r.log).toEqual(["scheme:dark", "color:#111416"]);
    const second = applyTheme(r.target, fixtureTheme("event_theme.changed"), first);
    expect(r.vars.get("--p-keyword")).toBeUndefined();
    expect(r.vars.get("--p-bg-base")).toBe("#fbfaf7");
    expect(r.vars.get("--kind-codex")).toBe("#5b4bd6");
    expect(second).toEqual(Object.keys(cssVariables(fixtureTheme("event_theme.changed"))));
    expect(r.log.slice(2)).toEqual(["scheme:light", "color:#fbfaf7"]);
  });

  test("Deck's own look is known by its canvas and accent", () => {
    const own = (bg: string, primary: string) => ({ palette: { bg_base: bg, primary }, kinds: {} });
    expect(deckLook(own("#19191b", "#0a84ff"))).toBe(true);
    expect(deckLook(own("#FBFBFD", "#007AFF"))).toBe(true);
    // Dark's canvas with light's accent is a theme of the person's.
    expect(deckLook(own("#19191b", "#007aff"))).toBe(false);
    expect(deckLook(fixtureTheme("entity_theme"))).toBe(false);
    expect(deckLook({ palette: {}, kinds: {} })).toBe(false);
  });

  test("marks come from the theme", () => {
    expect(kindMark(fixtureTheme("entity_theme"), "claude")).toBe("✻");
    expect(kindMark(fixtureTheme("entity_theme"), "unknown")).toBe("");
    expect(kindMark(null, "claude")).toBe("");
  });
});
