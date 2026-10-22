import { describe, expect, test } from "bun:test";
import { DEFAULT_APPEARANCE } from "../ui/palettes";
import { parseAppearance } from "./prefs";

describe("parseAppearance", () => {
  test("returns the default when nothing is stored", () => {
    expect(parseAppearance(null)).toEqual(DEFAULT_APPEARANCE);
  });

  test("reads a saved theme and accent", () => {
    expect(parseAppearance('{"theme":"nord","accent":"pink"}')).toEqual({
      theme: "nord",
      accent: "pink",
    });
  });

  test("ignores unknown ids field by field", () => {
    expect(parseAppearance('{"theme":"nord","accent":"chartreuse"}')).toEqual({
      theme: "nord",
      accent: "theme",
    });
    expect(parseAppearance('{"theme":7,"accent":"green"}')).toEqual({
      theme: "default",
      accent: "green",
    });
  });

  test("survives damaged or non-object storage", () => {
    expect(parseAppearance("{nope")).toEqual(DEFAULT_APPEARANCE);
    expect(parseAppearance("42")).toEqual(DEFAULT_APPEARANCE);
    expect(parseAppearance("null")).toEqual(DEFAULT_APPEARANCE);
  });
});
