import { mkdtempSync } from "node:fs";
import { type AddressInfo, createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { afterEach, beforeEach, describe, expect, test } from "vitest";
import { testHost } from "../../test/bridge";
import { FakeRpcDeck } from "../../test/fake-grpc";
import { fakePng } from "../../test/png";
import {
  APP_ICON_LIMITS,
  APP_ICON_SIZES,
  type AppIconImage,
  type HostMethod,
  type LocalDeck,
  type OpenOutcome,
  type RpcStreamEvent,
} from "../shared/bridge";
import { callHost, fromApp } from "./host";
import { RecentFolders } from "./recent";

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

  test("threadMenu takes an id, a title and the archive offer, and nothing else", async () => {
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
      [{ id: "ses_1", title: 3, archive: "on" }],
      [{ id: "ses_1", title: "T" }],
      [{ id: "ses_1", title: "T", archive: "yes" }],
      [{ id: "ses_1", title: "T", archive: "on", extra: true }],
      [["ses_1", "T", "on"]],
      [{ id: "ses_1", title: "T", archive: "on" }, "more"],
    ]) {
      await expect(callHost(host, "threadMenu", bad)).rejects.toThrow(/bad arguments/);
    }
    await expect(callHost(host, "threadMenu", [{ id: "ses_1", title: "Fix it", archive: "busy" }])).resolves.toBe("rename");
    expect(seen).toEqual([{ id: "ses_1", title: "Fix it", archive: "busy" }]);
  });
});

describe("ports and the preview", () => {
  const listen = async () => {
    const server = createServer();
    await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
    return { server, port: (server.address() as AddressInfo).port };
  };

  test("probePorts finds a port a real server listens on, and not one that closed", async () => {
    const up = await listen();
    const gone = await listen();
    await new Promise((done) => gone.server.close(done));
    const { host } = testHost(noEmit, noEmit);
    try {
      await expect(callHost(host, "probePorts", [[up.port, gone.port]])).resolves.toEqual([up.port]);
      await expect(callHost(host, "probePorts", [[]])).resolves.toEqual([]);
    } finally {
      up.server.close();
    }
  });

  test("probePorts takes up to 32 ports from 1 to 65535", async () => {
    const { host } = testHost(noEmit, noEmit);
    for (const bad of [[], [3000], [[0]], [[65536]], [[3000.5]], [["3000"]], [Array.from({ length: 33 }, (_, i) => 3000 + i)], [[3000], 1]]) {
      await expect(callHost(host, "probePorts", bad)).rejects.toThrow(/bad arguments/);
    }
  });

  test("the preview methods check their arguments before main sees them", async () => {
    const seen: unknown[] = [];
    const preview = {
      probe: async () => [],
      load: (url: string) => (seen.push(["load", url]), true),
      nav: (n: unknown) => void seen.push(["nav", n]),
      bounds: (b: unknown) => void seen.push(["bounds", b]),
      hide: () => void seen.push(["hide"]),
      close: () => void seen.push(["close"]),
    };
    const { host } = testHost(noEmit, noEmit, undefined, { preview });
    const bounds = { x: 10, y: 20, w: 300, h: 400, zoom: 1 };
    const bad: [HostMethod, unknown[]][] = [
      ["previewOpen", []],
      ["previewOpen", ["http://localhost:3000/"]],
      ["previewOpen", [{ url: 3000 }]],
      ["previewOpen", [{ url: "http://localhost:3000/", extra: 1 }]],
      ["previewOpen", [{ url: `http://localhost:3000/${"x".repeat(2048)}` }]],
      ["previewNav", [{ action: "home" }]],
      ["previewNav", [{ action: "path" }]],
      ["previewNav", [{ action: "path", path: "docs" }]],
      ["previewNav", [{ action: "back", path: "/" }]],
      ["previewBounds", [{ ...bounds, w: -1 }]],
      ["previewBounds", [{ ...bounds, x: Number.NaN }]],
      ["previewBounds", [{ ...bounds, zoom: 2 }]],
      ["previewBounds", [{ x: 1, y: 2, w: 3, h: 4 }]],
      ["previewHide", [true]],
      ["previewClose", ["now"]],
    ];
    for (const [m, args] of bad) {
      await expect(callHost(host, m, args), `${m} ${JSON.stringify(args)}`).rejects.toThrow(/bad arguments/);
    }
    expect(seen).toEqual([]);
    await expect(callHost(host, "previewOpen", [{ url: "http://localhost:3000/" }])).resolves.toBe(true);
    await callHost(host, "previewNav", [{ action: "path", path: "/docs?q=1" }]);
    await callHost(host, "previewNav", [{ action: "reload" }]);
    await callHost(host, "previewBounds", [{ ...bounds, zoom: 0.5 }]);
    await callHost(host, "previewHide", []);
    await callHost(host, "previewClose", []);
    expect(seen).toEqual([
      ["load", "http://localhost:3000/"],
      ["nav", { action: "path", path: "/docs?q=1" }],
      ["nav", { action: "reload" }],
      ["bounds", { ...bounds, zoom: 0.5 }],
      ["hide"],
      ["close"],
    ]);
  });

  test("setAppIcon takes PNGs of the sizes they claim, and nothing else reaches main", async () => {
    const seen: AppIconImage[][] = [];
    let decodes = true;
    const { host } = testHost(noEmit, noEmit, undefined, {
      setAppIcon: (images) => {
        seen.push(images);
        return decodes;
      },
    });
    const icon = APP_ICON_SIZES.other.map((size) => ({ size, png: fakePng(size) }));
    const bad: unknown[][] = [
      [],
      [icon, icon],
      ["data:image/png;base64,AAAA"],
      [{ size: 32, png: fakePng(32) }],
      [[{ size: 32, png: fakePng(64) }]],
      [[{ size: 32, png: new Uint8Array(64) }]],
      [[{ size: 2048, png: fakePng(2048) }]],
      [[{ size: 512, png: fakePng(512, 512, APP_ICON_LIMITS.bytes + 1) }]],
      [[{ size: 32, png: fakePng(32), path: "/etc/passwd" }]],
    ];
    for (const args of bad) await expect(callHost(host, "setAppIcon", args)).rejects.toThrow(/bad arguments/);
    expect(seen).toEqual([]);
    await expect(callHost(host, "setAppIcon", [icon])).resolves.toBe(true);
    expect(seen[0]?.map((i) => i.size)).toEqual([...APP_ICON_SIZES.other]);
    // Bytes the system cannot decode are not shown.
    decodes = false;
    await expect(callHost(host, "setAppIcon", [icon.slice(0, 1)])).resolves.toBe(false);
  });

  test("projectIcon reads only a folder the app knows, once a run", async () => {
    const asked: string[] = [];
    const recent = new RecentFolders(null);
    recent.add("/work/deck");
    const { host } = testHost(noEmit, noEmit, undefined, {
      recent,
      findIcon: async (dir) => {
        asked.push(dir);
        return "data:image/png;base64,AAAA";
      },
    });
    await expect(callHost(host, "projectIcon", [42])).rejects.toThrow(/bad arguments/);
    await expect(callHost(host, "projectIcon", ["/etc"])).resolves.toBeNull();
    await expect(callHost(host, "projectIcon", ["/work/deck"])).resolves.toBe("data:image/png;base64,AAAA");
    await expect(callHost(host, "projectIcon", ["/work/deck"])).resolves.toBe("data:image/png;base64,AAAA");
    expect(asked).toEqual(["/work/deck"]);
  });

  test("a message from the preview's page is refused: only the window's own page may call", () => {
    const window = { id: 1 };
    const previewPage = { id: 2 };
    expect(fromApp(window, "app://deck/index.html", window, "app://deck")).toBe(true);
    expect(fromApp(previewPage, "app://deck/index.html", window, "app://deck")).toBe(false);
    expect(fromApp(previewPage, "http://localhost:3000/", window, "app://deck")).toBe(false);
    expect(fromApp(window, "http://localhost:3000/", window, "app://deck")).toBe(false);
    expect(fromApp(window, undefined, window, "app://deck")).toBe(false);
    expect(fromApp(window, "app://deck/index.html", null, "app://deck")).toBe(false);
  });
});
