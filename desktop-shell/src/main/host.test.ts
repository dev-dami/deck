import { describe, expect, test } from "vitest";
import { testHost } from "../../test/bridge";
import { callHost } from "./host";

describe("callHost", () => {
  const { host } = testHost(
    () => {},
    () => {},
  );

  test("refuses arguments of the wrong type or count", async () => {
    await expect(callHost(host, "socketSend", ["1", "x"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "pair", [{ host: "127.0.0.1" }])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "listServers", ["extra"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "listServers", [])).resolves.toEqual([]);
  });

  test("rpc calls name a known method and pass a plain object", async () => {
    await expect(callHost(host, "rpcCall", ["local-1", "Shutdown", {}])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "rpcCall", ["local-1", "Send", "text"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "rpcCall", ["local-1", "Send", [1]])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "rpcSubscribe", ["local-1", "", -1])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "rpcCall", ["local-1", "ListSessions", {}])).resolves.toMatchObject({
      ok: false,
      message: "That Deck is closed.",
    });
  });

  test("pair checks the target before it connects", async () => {
    const out = await callHost(host, "pair", [
      { host: "192.168.1.5", port: 7878, tls: false, fingerprint: null, code: "ABCDE" },
    ]);
    expect(out).toMatchObject({ ok: false, message: expect.stringMatching(/this computer only/) });
  });

  test("a folder that does not exist does not start Deck", async () => {
    await expect(callHost(host, "openFolder", ["/no/such/folder"])).resolves.toEqual({
      ok: false,
      message: "That folder does not exist.",
    });
  });
});
