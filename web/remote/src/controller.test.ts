import { describe, expect, test } from "bun:test";
import type { KV } from "./auth";
import { Controller } from "./controller";
import { FakeClock, FakePushBrowser, type FakeSocket, SocketLog } from "./fakes";
import { isRecord } from "./protocol";

const session = { id: "s1", title: "Fix bug", updated_at: 10, busy: false, pending: 0 };
const approvalEdit = {
  id: "p2",
  session_id: "s1",
  kind: "edit",
  description: "Edit a file",
  path: "a.go",
  diff: "@@ -1 +1 @@\n-a\n+b\n",
  created_at: 4,
};

function memory(): KV & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => {
      data.set(k, v);
    },
    removeItem: (k) => {
      data.delete(k);
    },
  };
}

const blocked: KV = {
  getItem: () => {
    throw new Error("blocked");
  },
  setItem: () => {
    throw new Error("blocked");
  },
  removeItem: () => {
    throw new Error("blocked");
  },
};

function setup(over: { storage?: KV | null; pushBrowser?: FakePushBrowser } = {}) {
  const storage = over.storage === undefined ? memory() : over.storage;
  const log = new SocketLog();
  const clock = new FakeClock();
  const vibrations: Array<number | number[]> = [];
  const pairCalls: unknown[] = [];
  const controller = new Controller({
    storage,
    fetchFn: async (url, init) => {
      pairCalls.push([url, JSON.parse(String(init.body))]);
      return Response.json({ token: "dev1.secret", device_id: "dev1" });
    },
    socketFactory: log.factory,
    wsUrl: "ws://host/api/v1/ws",
    deviceName: "Pixel",
    vibrate: (p) => vibrations.push(p),
    clock,
    ...(over.pushBrowser === undefined ? {} : { pushBrowser: over.pushBrowser }),
  });
  return { storage, log, clock, controller, vibrations, pairCalls };
}

function answer(sock: FakeSocket, results: Record<string, unknown>): void {
  for (const f of sock.sent) {
    if (!isRecord(f) || f["t"] !== "req" || typeof f["m"] !== "string") continue;
    if (f["m"] in results) sock.receive({ t: "res", id: f["id"], ok: true, r: results[f["m"]] });
  }
}

async function flush(): Promise<void> {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

describe("controller", () => {
  test("pairing stores the token, connects, and loads snapshots on welcome", async () => {
    const t = setup();
    await t.controller.start("abc");
    expect(t.pairCalls).toEqual([["/api/v1/pair", { code: "abc", device_name: "Pixel" }]]);
    expect(t.storage?.getItem("deck.token")).toBe("dev1.secret");
    expect(t.controller.state.auth).toBe("paired");

    const sock = t.log.latest;
    sock.open();
    expect(sock.sent[0]).toEqual({ t: "hello", v: 1, token: "dev1.secret" });
    sock.receive({
      t: "welcome",
      epoch: "e1",
      server: {
        version: "0.9.0",
        project: "deck",
        cwd: "/w",
        kinds: ["claude"],
        allow_yolo: true,
        protocol: 1,
        headless: false,
      },
      device: { id: "dev1", name: "Pixel" },
    });
    expect(t.controller.state.link).toBe("online");
    expect(t.controller.state.server?.allow_yolo).toBe(true);

    answer(sock, {
      "sessions.list": [session],
      "approvals.list": [approvalEdit],
      "mode.get": { mode: "ask" },
    });
    await flush();
    const s = t.controller.state;
    expect(s.sessions.map((x) => x.id)).toEqual(["s1"]);
    expect(s.sessions[0]?.pending).toBe(1);
    expect(s.approvals).toHaveLength(1);
    expect(s.mode).toBe("ask");
  });

  test("a failed pairing returns to the pairing screen with a message", async () => {
    const log = new SocketLog();
    const storage = memory();
    const c = new Controller({
      storage,
      fetchFn: async () => new Response("no", { status: 401 }),
      socketFactory: log.factory,
      wsUrl: "ws://x",
      deviceName: "Pixel",
      vibrate: () => {},
      clock: new FakeClock(),
    });
    await c.start("bad");
    expect(c.state.auth).toBe("unpaired");
    expect(c.state.authMessage).toContain("invalid");
    expect(log.sockets).toHaveLength(0);
    expect(storage.getItem("deck.token")).toBeNull();
  });

  test("with a stored token it connects without pairing", async () => {
    const storage = memory();
    storage.setItem("deck.token", "dev1.secret");
    const t = setup({ storage });
    await t.controller.start(null);
    expect(t.pairCalls).toHaveLength(0);
    expect(t.log.sockets).toHaveLength(1);
  });

  test("with nothing stored it shows pairing", async () => {
    const t = setup();
    await t.controller.start(null);
    expect(t.controller.state.auth).toBe("unpaired");
    expect(t.log.sockets).toHaveLength(0);
  });

  test("blocked storage keeps the session alive for this page load", async () => {
    const t = setup({ storage: blocked });
    await t.controller.start("abc");
    expect(t.controller.state.notices[0]?.kind).toBe("error");
    t.log.latest.open();
    expect(t.log.latest.sent[0]).toMatchObject({ t: "hello", token: "dev1.secret" });
    t.log.latest.receive({ t: "welcome", epoch: "e1" });
    t.log.latest.drop();
    t.clock.advance(1000);
    t.log.latest.open();
    expect(t.log.sockets).toHaveLength(2);
    expect(t.log.latest.sent[0]).toMatchObject({ token: "dev1.secret" });
  });

  test("live events update state and approvals vibrate", async () => {
    const t = setup();
    await t.controller.start("abc");
    const sock = t.log.latest;
    sock.open();
    sock.receive({ t: "welcome", epoch: "e1" });
    sock.receive({ t: "ev", seq: 1, e: "approval.requested", p: approvalEdit });
    sock.receive({ t: "ev", seq: 2, e: "busy", p: { session_id: "s1", busy: true } });
    sock.receive({ t: "ev", seq: 3, e: "mode.changed", p: { mode: "plan" } });
    expect(t.controller.state.approvals.map((a) => a.id)).toEqual(["p2"]);
    expect(t.controller.state.mode).toBe("plan");
    expect(t.vibrations).toHaveLength(1);
  });

  test("answering an approval sends the decision and drops it", async () => {
    const t = setup();
    await t.controller.start("abc");
    const sock = t.log.latest;
    sock.open();
    sock.receive({ t: "welcome", epoch: "e1" });
    sock.receive({ t: "ev", seq: 1, e: "approval.requested", p: approvalEdit });
    const p = t.controller.answerApproval("p2", "allow_session");
    expect(sock.last()).toMatchObject({
      m: "approvals.answer",
      p: { id: "p2", decision: "allow_session" },
    });
    sock.receive({ t: "res", id: sock.last()["id"], ok: true, r: { resolved: true } });
    await p;
    expect(t.controller.state.approvals).toEqual([]);
  });

  test("device.revoked and close 4401 return to pairing and clear the token", async () => {
    const a = setup();
    await a.controller.start("abc");
    a.log.latest.open();
    a.log.latest.receive({ t: "welcome", epoch: "e1" });
    a.log.latest.receive({ t: "ev", seq: 1, e: "device.revoked", p: {} });
    expect(a.controller.state.auth).toBe("unpaired");
    expect(a.storage?.getItem("deck.token")).toBeNull();

    const b = setup();
    await b.controller.start("abc");
    b.log.latest.open();
    b.log.latest.drop(4401);
    expect(b.controller.state.auth).toBe("unpaired");
    expect(b.storage?.getItem("deck.token")).toBeNull();
    expect(b.controller.state.authMessage).toContain("no longer valid");
  });

  test("a server error surfaces as a notice", async () => {
    const t = setup();
    await t.controller.start("abc");
    const sock = t.log.latest;
    sock.open();
    sock.receive({ t: "welcome", epoch: "e1" });
    const p = t.controller.setMode("yolo");
    sock.receive({
      t: "res",
      id: sock.last()["id"],
      ok: false,
      err: { code: "yolo_disabled", msg: "start deck serve with --allow-yolo" },
    });
    expect(await p).toBe(false);
    expect(t.controller.state.notices[0]?.text).toContain("--allow-yolo");
    expect(t.controller.state.mode).toBeNull();
  });

  test("editing a message sends message.edit and the update arrives as an event", async () => {
    const t = setup();
    await t.controller.start("abc");
    const sock = t.log.latest;
    sock.open();
    sock.receive({ t: "welcome", epoch: "e1" });
    const userMsg = {
      id: "m1",
      session_id: "s1",
      role: "user",
      created_at: 1,
      updated_at: 1,
      finished: true,
      editable: true,
      parts: [{ type: "text", text: "old" }],
    };
    sock.receive({ t: "ev", seq: 1, e: "message.created", p: userMsg });
    const p = t.controller.editMessage("s1", "m1", "new");
    expect(sock.last()).toMatchObject({
      m: "message.edit",
      p: { session_id: "s1", message_id: "m1", text: "new" },
    });
    sock.receive({ t: "res", id: sock.last()["id"], ok: true });
    expect(await p).toBe(true);
    sock.receive({
      t: "ev",
      seq: 2,
      e: "message.updated",
      p: { ...userMsg, updated_at: 2, parts: [{ type: "text", text: "new" }] },
    });
    expect(t.controller.state.messages["s1"]?.[0]?.parts[0]?.text).toBe("new");
  });

  test("edit rejections surface as toasts", async () => {
    for (const [code, msg] of [
      ["invalid", "message is not editable"],
      ["not_found", "message not found"],
    ] as const) {
      const t = setup();
      await t.controller.start("abc");
      const sock = t.log.latest;
      sock.open();
      sock.receive({ t: "welcome", epoch: "e1" });
      const p = t.controller.editMessage("s1", "m1", "x");
      sock.receive({ t: "res", id: sock.last()["id"], ok: false, err: { code, msg } });
      expect(await p).toBe(false);
      expect(t.controller.state.notices[0]).toMatchObject({ kind: "error", text: msg });
    }
  });

  test("editing while offline reports it instead of throwing", async () => {
    const t = setup();
    await t.controller.start("abc");
    expect(await t.controller.editMessage("s1", "m1", "x")).toBe(false);
    expect(t.controller.state.notices[0]?.text).toContain("offline");
  });

  test("a welcome syncs push with the server", async () => {
    const browser = new FakePushBrowser();
    browser.perm = "granted";
    const t = setup({ pushBrowser: browser });
    await t.controller.start("abc");
    const sock = t.log.latest;
    sock.open();
    sock.receive({
      t: "welcome",
      epoch: "e1",
      server: {
        version: "1",
        project: "deck",
        cwd: "/w",
        kinds: [],
        allow_yolo: false,
        protocol: 1,
        headless: false,
        push_key: "AQID",
      },
    });
    await flush();
    expect(t.controller.state.push).toMatchObject({ checked: true, gate: "ok" });
    const asked = sock.sent.filter((f) => isRecord(f) && f["m"] === "push.status");
    expect(asked).toHaveLength(1);
    sock.receive({
      t: "res",
      id: (asked[0] as Record<string, unknown>)["id"],
      ok: true,
      r: { available: true, enabled: true, approvals: false, done: true },
    });
    await flush();
    expect(t.controller.state.push).toMatchObject({ enabled: true, approvals: false, done: true });
  });

  test("a server with no push key leaves push blocked and sends nothing", async () => {
    const t = setup({ pushBrowser: new FakePushBrowser() });
    await t.controller.start("abc");
    const sock = t.log.latest;
    sock.open();
    sock.receive({ t: "welcome", epoch: "e1" });
    await flush();
    expect(t.controller.state.push.gate).toBe("server-off");
    expect(sock.sent.some((f) => isRecord(f) && f["m"] === "push.status")).toBe(false);
  });
});
