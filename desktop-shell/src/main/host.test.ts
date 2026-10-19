import { describe, expect, test } from "vitest";
import { testHost } from "../../test/bridge";
import { callHost } from "./host";
import { findOnPath, parsePairOffer } from "./local";

describe("callHost", () => {
  const { host } = testHost(() => {});

  test("refuses arguments of the wrong type or count", async () => {
    await expect(callHost(host, "socketSend", ["1", "x"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "pair", [{ host: "127.0.0.1" }])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "listServers", ["extra"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "listServers", [])).resolves.toEqual([]);
  });

  test("pair checks the target before it connects", async () => {
    const out = await callHost(host, "pair", [
      { host: "192.168.1.5", port: 7878, tls: false, fingerprint: null, code: "ABCDE" },
    ]);
    expect(out).toMatchObject({ ok: false, message: expect.stringMatching(/this computer only/) });
  });
});

describe("deck remote pair --json", () => {
  test("reads the loopback and the TLS offer", () => {
    expect(
      parsePairOffer('{"url":"http://127.0.0.1:7878","hosts":["127.0.0.1"],"port":7878,"tls":false,"code":"AB-CD","expires_at":"2026-10-19T09:05:00Z"}'),
    ).toEqual({ target: { host: "127.0.0.1", port: 7878, tls: false, fingerprint: null }, code: "AB-CD" });
    const fp = `SHA256:${"A".repeat(43)}`;
    expect(
      parsePairOffer(`{"url":"https://192.168.1.5:7878","port":7878,"tls":true,"fingerprint":"${fp}","code":"AB-CD"}`),
    ).toEqual({ target: { host: "192.168.1.5", port: 7878, tls: true, fingerprint: fp }, code: "AB-CD" });
  });

  test("refuses TLS without a fingerprint and text that is not the offer", () => {
    expect(parsePairOffer('{"url":"https://192.168.1.5:7878","port":7878,"tls":true,"code":"AB"}')).toBeNull();
    expect(parsePairOffer("Scan with your phone's camera")).toBeNull();
  });

  test("findOnPath finds an executable", () => {
    expect(findOnPath("node")).not.toBeNull();
    expect(findOnPath("no-such-deck-binary")).toBeNull();
  });
});
