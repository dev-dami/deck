import { describe, expect, test } from "bun:test";
import { openRelayDevice, openRelayPair } from "./relay";
import { ScriptedSocket } from "./testkit";
import { openSocket, TransportError } from "./transport";

function factoryOf(sockets: ScriptedSocket[]) {
  return (url: string) => {
    const s = new ScriptedSocket(url);
    sockets.push(s);
    return s;
  };
}

describe("relay endpoints", () => {
  test("a device lane sends the token as the first text frame, never in the URL", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openRelayDevice("https://app.example.dev/", "Zk3p0J", "rt_secret", {
      factory: factoryOf(sockets),
    });
    const ws = sockets[0] as ScriptedSocket;
    expect(ws.url).toBe("wss://app.example.dev/r/v1/c/Zk3p0J/dev");
    expect(ws.url).not.toContain("rt_secret");
    ws.open();
    await p;
    expect(ws.sent).toEqual([JSON.stringify({ token: "rt_secret" })]);
    expect(ws.binaryType).toBe("arraybuffer");
  });

  test("a pair lane names its window in the first frame", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openRelayPair("http://localhost:8787", "cid1", "pid_abc", {
      factory: factoryOf(sockets),
    });
    const ws = sockets[0] as ScriptedSocket;
    expect(ws.url).toBe("ws://localhost:8787/r/v1/c/cid1/pair");
    ws.open();
    await p;
    expect(ws.sent).toEqual([JSON.stringify({ pair_id: "pid_abc" })]);
  });

  test("the relay address must be http or https", () => {
    expect(() => openRelayDevice("ftp://x.example", "c", "t")).toThrow();
  });

  test("a cid in the path is encoded", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openRelayDevice("https://r.example", "a/b", "t", { factory: factoryOf(sockets) });
    expect((sockets[0] as ScriptedSocket).url).toBe("wss://r.example/r/v1/c/a%2Fb/dev");
    (sockets[0] as ScriptedSocket).open();
    await p;
  });
});

describe("socket transport", () => {
  test("messages that arrive before a handler is set are kept, in order", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openSocket("wss://x", { factory: factoryOf(sockets) });
    const ws = sockets[0] as ScriptedSocket;
    ws.open();
    const t = await p;
    ws.receive(new Uint8Array([1]).buffer);
    ws.receive(new Uint8Array([2]).buffer);
    const got: number[] = [];
    t.onMessage((m) => got.push(m[0] as number));
    ws.receive(new Uint8Array([3]).buffer);
    expect(got).toEqual([1, 2, 3]);
  });

  test("text frames after the first are ignored", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openSocket("wss://x", { factory: factoryOf(sockets) });
    const ws = sockets[0] as ScriptedSocket;
    ws.open();
    const t = await p;
    const got: Uint8Array[] = [];
    t.onMessage((m) => got.push(m));
    ws.receive("hello");
    expect(got).toHaveLength(0);
  });

  test("a refusal before open rejects with the close code", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openSocket("wss://x", { factory: factoryOf(sockets) });
    (sockets[0] as ScriptedSocket).fail(4404, "unknown computer");
    const err = await p.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect((err as TransportError).code).toBe(4404);
  });

  test("a socket that never opens times out and is closed", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openSocket("wss://x", { factory: factoryOf(sockets), timeoutMs: 20 });
    const err = await p.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect((sockets[0] as ScriptedSocket).closedWith).not.toBeNull();
  });

  test("closing reports once, and sending afterwards fails", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openSocket("wss://x", { factory: factoryOf(sockets) });
    const ws = sockets[0] as ScriptedSocket;
    ws.open();
    const t = await p;
    const closes: number[] = [];
    t.onClose((code) => closes.push(code));
    ws.fail(4403, "revoked");
    ws.fail(1006);
    expect(closes).toEqual([4403]);
    expect(() => t.send(new Uint8Array(1))).toThrow();
  });

  test("a close that happened before the handler was set is still reported", async () => {
    const sockets: ScriptedSocket[] = [];
    const p = openSocket("wss://x", { factory: factoryOf(sockets) });
    const ws = sockets[0] as ScriptedSocket;
    ws.open();
    const t = await p;
    ws.fail(4401, "bad token");
    let seen = 0;
    t.onClose((code) => {
      seen = code;
    });
    expect(seen).toBe(4401);
  });
});
