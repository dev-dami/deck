import { describe, expect, test } from "vitest";
import { parsePairText } from "./connect";

const FP = `SHA256:${"A".repeat(43)}`;

describe("parsePairText", () => {
  test("reads the loopback link deck remote pair prints", () => {
    expect(parsePairText("http://127.0.0.1:7878/#pair=ABCDE-FGHIJ", "")).toEqual({
      ok: true,
      input: { host: "127.0.0.1", port: 7878, tls: false, fingerprint: null, code: "ABCDE-FGHIJ" },
    });
  });

  test("reads an invite line with its fingerprint", () => {
    const r = parsePairText(`deck://pair?v=1&host=192.168.1.5&port=7878&code=ABCDE-FGHIJ&fp=${encodeURIComponent(FP)}`, "");
    expect(r).toEqual({
      ok: true,
      input: { host: "192.168.1.5", port: 7878, tls: true, fingerprint: FP, code: "ABCDE-FGHIJ" },
    });
  });

  test("an https link needs a typed fingerprint", () => {
    expect(parsePairText("https://192.168.1.5:7878/#pair=ABCDE", "").ok).toBe(false);
    expect(parsePairText("https://192.168.1.5:7878/#pair=ABCDE", "AB:CD")).toEqual({
      ok: true,
      input: { host: "192.168.1.5", port: 7878, tls: true, fingerprint: "AB:CD", code: "ABCDE" },
    });
  });

  test("refuses text with no code", () => {
    expect(parsePairText("http://127.0.0.1:7878/", "").ok).toBe(false);
    expect(parsePairText("hello", "").ok).toBe(false);
    expect(parsePairText("deck://pair?v=1&host=x", "").ok).toBe(false);
  });
});
