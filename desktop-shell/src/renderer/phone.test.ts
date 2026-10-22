import { expect, test } from "vitest";
import type { PhoneState } from "../shared/bridge";
import { codeUsable, qrPath, shownCode } from "./phone";

const base: PhoneState = {
  name: "pc",
  relayUrl: "http://192.168.1.5:8787",
  online: true,
  projects: 1,
  devices: [],
  pairUrl: "http://192.168.1.5:8787/#p=abc",
  qr: ["000", "010", "000"],
  expiresAt: "2026-10-22T12:05:00Z",
  message: null,
};

test("a state with a link, a QR and an expiry shows a code", () => {
  expect(shownCode(base)).toEqual({
    pairUrl: base.pairUrl,
    qr: base.qr,
    expiresAt: Date.parse("2026-10-22T12:05:00Z"),
    devices: 0,
  });
});

test("a state without all three shows none", () => {
  expect(shownCode({ ...base, qr: null })).toBeNull();
  expect(shownCode({ ...base, pairUrl: null })).toBeNull();
  expect(shownCode({ ...base, expiresAt: "soon" })).toBeNull();
});

test("a code lapses at its expiry and once another phone pairs", () => {
  const code = shownCode(base);
  if (code === null) throw new Error("expected a code");
  const before = Date.parse("2026-10-22T12:00:00Z");
  expect(codeUsable(code, [], before)).toBe(true);
  expect(codeUsable(code, [], code.expiresAt)).toBe(false);
  expect(codeUsable(code, [{ id: "d1", name: "Pixel" }], before)).toBe(false);
});

test("qrPath joins each run of dark modules into one rectangle", () => {
  expect(qrPath(["0110", "1001", "1111"])).toBe("M1 0h2v1h-2zM0 1h1v1h-1zM3 1h1v1h-1zM0 2h4v1h-4z");
  expect(qrPath(["000"])).toBe("");
});
