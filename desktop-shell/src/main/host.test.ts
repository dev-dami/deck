import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { testHost } from "../../test/bridge";
import { FakeRpcDeck } from "../../test/fake-grpc";
import type { LocalDeck, OpenOutcome, RpcStreamEvent } from "../shared/bridge";
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

  test("restartFolder takes a Deck id and restarts only a stopped Deck", async () => {
    await expect(callHost(host, "restartFolder", [])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "restartFolder", [1])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "restartFolder", ["local-9", "x"])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "restartFolder", ["local-9"])).resolves.toEqual({ ok: false, message: "That Deck is closed." });
  });
});

describe("a Deck that stops by itself", () => {
  let fake: FakeRpcDeck;
  beforeEach(async () => {
    fake = await FakeRpcDeck.start({ tickMs: 20 });
    process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  });
  afterEach(async () => {
    delete process.env["DECK_FAKE_EXIT_AFTER"];
    await fake.close();
  });

  test("stays known, says why, and restartFolder starts it again under the same id", async () => {
    const events: RpcStreamEvent[] = [];
    const t = testHost(
      () => {},
      (_id, ev) => events.push(ev),
    );
    const dir = mkdtempSync(join(tmpdir(), "deck-stop-"));
    process.env["DECK_FAKE_EXIT_AFTER"] = "300";
    const out = await callHost(t.host, "openFolder", [dir]);
    if (!(out as OpenOutcome).ok) throw new Error("did not open");
    const deck = (out as { deck: LocalDeck }).deck;
    await until(() => events.some((e) => e.type === "stopped"));
    expect(events.find((e) => e.type === "stopped")).toEqual({
      type: "stopped",
      message: "Deck stopped (exit 3).",
      stderr: ["fake deck: the store is locked", "panic: fake crash"],
    });
    // A late subscribe of the stopped Deck does not close the project.
    await callHost(t.host, "rpcSubscribe", [deck.id, "", 0]);
    expect(events.some((e) => e.type === "exit")).toBe(false);

    delete process.env["DECK_FAKE_EXIT_AFTER"];
    const [a, b] = await Promise.all([callHost(t.host, "restartFolder", [deck.id]), callHost(t.host, "restartFolder", [deck.id])]);
    expect(a).toEqual({ ok: true, deck });
    expect(b).toEqual({ ok: true, deck });
    await expect(callHost(t.host, "rpcCall", [deck.id, "Info", {}])).resolves.toMatchObject({ ok: true });
    t.decks.closeAll();
    await expect(callHost(t.host, "restartFolder", [deck.id])).resolves.toEqual({ ok: false, message: "That Deck is closed." });
  });
});

async function until(cond: () => boolean, ms = 5000): Promise<void> {
  const end = Date.now() + ms;
  while (!cond()) {
    if (Date.now() > end) throw new Error("timed out waiting");
    await new Promise((r) => setTimeout(r, 10));
  }
}
