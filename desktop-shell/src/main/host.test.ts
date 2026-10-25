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

const noEmit = () => {};

describe("platform methods", () => {
  test("appInfo and openLogs take no arguments", async () => {
    let opened = 0;
    const { host } = testHost(noEmit, noEmit, undefined, {
      openLogs: async () => {
        opened++;
      },
    });
    await expect(callHost(host, "appInfo", [])).resolves.toMatchObject({ version: "0.1.0" });
    await expect(callHost(host, "appInfo", ["x"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "openLogs", ["/etc"])).rejects.toThrow(/bad arguments/);
    await callHost(host, "openLogs", []);
    expect(opened).toBe(1);
  });

  test("logFromRenderer takes a known level and a string, and caps its length", async () => {
    const { host, logged } = testHost(noEmit, noEmit);
    await expect(callHost(host, "logFromRenderer", ["debug", "x"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "logFromRenderer", ["error", { message: "x" }])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "logFromRenderer", ["error"])).rejects.toThrow(/bad arguments/);
    await callHost(host, "logFromRenderer", ["error", "TypeError: boom"]);
    await callHost(host, "logFromRenderer", ["warn", "x".repeat(10_000)]);
    expect(logged[0]).toEqual(["error", "renderer: TypeError: boom"]);
    expect(logged[1]?.[0]).toBe("warn");
    expect(logged[1]?.[1].length).toBeLessThan(4100);
  });

  test("installUpdate takes no arguments", async () => {
    let installs = 0;
    const { host } = testHost(noEmit, noEmit, undefined, { installUpdate: () => installs++ });
    await expect(callHost(host, "installUpdate", [true])).rejects.toThrow(/bad arguments/);
    await callHost(host, "installUpdate", []);
    expect(installs).toBe(1);
  });

  test("threadMenu takes an id and a title and nothing else", async () => {
    const seen: unknown[] = [];
    const { host } = testHost(noEmit, noEmit, undefined, {
      threadMenu: async (t) => {
        seen.push(t);
        return "rename";
      },
    });
    for (const bad of [
      [],
      ["ses_1"],
      [{ id: "ses_1" }],
      [{ id: "ses_1", title: 3 }],
      [{ id: "ses_1", title: "T", extra: true }],
      [["ses_1", "T"]],
      [{ id: "ses_1", title: "T" }, "more"],
    ]) {
      await expect(callHost(host, "threadMenu", bad)).rejects.toThrow(/bad arguments/);
    }
    await expect(callHost(host, "threadMenu", [{ id: "ses_1", title: "Fix it" }])).resolves.toBe("rename");
    expect(seen).toEqual([{ id: "ses_1", title: "Fix it" }]);
  });
});
