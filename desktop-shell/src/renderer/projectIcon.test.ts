import { describe, expect, test } from "vitest";
import { HUES, hash, monogram, projectHue, projectTile } from "./projectIcon";

describe("project tiles", () => {
  test("the hash is FNV-1a and stable", () => {
    expect(hash("")).toBe(0x811c9dc5);
    expect(hash("a")).toBe(0xe40c292c);
    expect(hash("folder:/home/me/deck")).toBe(hash("folder:/home/me/deck"));
  });

  test("a hue is one of the steps, the same for the same source, and spread over many", () => {
    const h = projectHue("folder:/home/me/deck");
    expect(h).toBe(projectHue("folder:/home/me/deck"));
    expect(h % (360 / HUES)).toBe(0);
    expect(h).toBeGreaterThanOrEqual(0);
    expect(h).toBeLessThan(360);
    const hues = new Set(Array.from({ length: 200 }, (_, i) => projectHue(`folder:/src/project-${i}`)));
    expect(hues.size).toBeGreaterThan(HUES - 4);
  });

  test("two folders of one name differ by where they are", () => {
    const a = projectTile("folder:/work/app", "app");
    const b = projectTile("folder:/home/app", "app");
    expect(a.glyph).toBe(b.glyph);
    expect([projectHue("folder:/work/app"), projectHue("folder:/home/app"), projectHue("folder:/tmp/app")].some((x) => x !== a.hue)).toBe(true);
  });

  test("the monogram takes the initials of the first two words", () => {
    expect(monogram("deck")).toBe("D");
    expect(monogram("my-app")).toBe("MA");
    expect(monogram("myApp")).toBe("MA");
    expect(monogram("hello_world_again")).toBe("HW");
    expect(monogram("Paired Deck")).toBe("PD");
    expect(monogram("my-app", 1)).toBe("M");
    expect(monogram("2fa.service")).toBe("2S");
    expect(monogram("über")).toBe("Ü");
    expect(monogram("日本 語")).toBe("日語");
    expect(monogram("---")).toBe("#");
    expect(monogram("")).toBe("#");
  });

  test("a paired Deck's tile shows a computer", () => {
    expect(projectTile("paired:abc", "Paired Deck").paired).toBe(true);
    expect(projectTile("folder:/x", "x")).toEqual({ hue: projectHue("folder:/x"), glyph: "X", paired: false });
  });
});
