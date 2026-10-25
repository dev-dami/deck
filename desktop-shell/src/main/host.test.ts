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

describe("notifications, badge and focus", () => {
  const notes: unknown[] = [];
  const badges: number[] = [];
  let focused = false;
  const { host } = testHost(
    () => {},
    () => {},
    undefined,
    { notify: (n) => notes.push(n), badge: (c) => badges.push(c), focused: () => focused },
  );
  const note = { title: "deck · Docs pass", body: "Approve command", tag: "rpc:1\nses_docs", silent: false };

  test("notify takes a title, a body, a tag and the sound flag, and nothing else", async () => {
    await expect(callHost(host, "notify", [note])).resolves.toBeUndefined();
    expect(notes).toEqual([note]);
    for (const bad of [
      [],
      ["text"],
      [{ ...note, silent: "no" }],
      [{ ...note, extra: 1 }],
      [{ title: "t", body: "b", tag: "x" }],
      [{ ...note, title: "x".repeat(201) }],
      [{ ...note, body: "x".repeat(501) }],
      [note, note],
    ]) {
      await expect(callHost(host, "notify", bad)).rejects.toThrow(/bad arguments/);
    }
    expect(notes).toHaveLength(1);
  });

  test("badge takes a count from 0 to 9999", async () => {
    await callHost(host, "badge", [3]);
    await callHost(host, "badge", [0]);
    expect(badges).toEqual([3, 0]);
    for (const bad of [[-1], [1.5], ["3"], [10_000], []]) {
      await expect(callHost(host, "badge", bad)).rejects.toThrow(/bad arguments/);
    }
  });

  test("focused says whether the window has the focus", async () => {
    await expect(callHost(host, "focused", [])).resolves.toBe(false);
    focused = true;
    await expect(callHost(host, "focused", [])).resolves.toBe(true);
    await expect(callHost(host, "focused", [1])).rejects.toThrow(/bad arguments/);
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

  test("agentClis takes the refresh flag, looks once and looks again on refresh", async () => {
    let looks = 0;
    const { host } = testHost(noEmit, noEmit, undefined, {
      agentClis: async () => {
        looks++;
        return [{ kind: "claude", path: looks > 1 ? "/bin/claude" : null }];
      },
    });
    for (const bad of [[], ["yes"], [1], [true, true]]) {
      await expect(callHost(host, "agentClis", bad)).rejects.toThrow(/bad arguments/);
    }
    await expect(callHost(host, "agentClis", [false])).resolves.toEqual({ engine: true, clis: [{ kind: "claude", path: null }] });
    await callHost(host, "agentClis", [false]);
    expect(looks).toBe(1);
    await expect(callHost(host, "agentClis", [true])).resolves.toEqual({ engine: true, clis: [{ kind: "claude", path: "/bin/claude" }] });
    expect(looks).toBe(2);
    const missing = testHost(noEmit, noEmit, undefined, { deckPath: null });
    await expect(callHost(missing.host, "agentClis", [false])).resolves.toMatchObject({ engine: false });
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
