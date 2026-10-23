import { describe, expect, test } from "bun:test";
import { agentMark, agentTint, instanceOf, kindTones } from "./agents";

const theme = {
  palette: { bg_base: "#101010" },
  kinds: {
    claude: { tint: "#D97757", mark: "✻" },
    codex: { tint: "not-a-colour", mark: "◇" },
  },
};

describe("kindTones", () => {
  test("keeps kinds with a valid tint, lowercased", () => {
    expect(kindTones(theme)).toEqual({ claude: { tint: "#d97757", mark: "✻" } });
  });

  test("returns nothing for a theme it cannot decode", () => {
    expect(kindTones(null)).toEqual({});
    expect(kindTones({ palette: 3 })).toEqual({});
  });
});

describe("agentTint", () => {
  const tones = kindTones(theme);

  test("uses the kind's tint, or the fallback for an unnamed kind", () => {
    expect(agentTint(tones, "claude", "claude", "#ffffff")).toBe("#d97757");
    expect(agentTint(tones, "gemini", "gemini", "#123456")).toBe("#123456");
  });

  test("lightens further instances of one kind", () => {
    const first = agentTint(tones, "claude", "claude", "#fff");
    const second = agentTint(tones, "claude-2", "claude", "#fff");
    expect(second).not.toBe(first);
    expect(instanceOf("claude-2")).toBe(2);
    expect(instanceOf("claude")).toBe(1);
  });

  test("marks default to a dot", () => {
    expect(agentMark(tones, "claude")).toBe("✻");
    expect(agentMark(tones, "gemini")).toBe("●");
  });
});
