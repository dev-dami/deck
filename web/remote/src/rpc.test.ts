import { describe, expect, test } from "bun:test";
import { FakeClock, SocketLog } from "./fakes";
import { backoffDelay, type ConnStatus, RpcClient, RpcError, type StopReason } from "./rpc";

function harness(over: { requestTimeoutMs?: number } = {}) {
  const clock = new FakeClock();
  const log = new SocketLog();
  const token = { value: "dev1.secret" as string | null };
  const events: Array<[number, string, unknown]> = [];
  const statuses: ConnStatus[] = [];
  const welcomes: boolean[] = [];
  const stops: StopReason[] = [];
  const counts = { resyncs: 0 };
  let n = 0;
  const rpc = new RpcClient({
    url: "ws://x/api/v1/ws",
    getToken: () => token.value,
    socketFactory: log.factory,
    clock,
    random: () => 0.5,
    newId: () => `key-${++n}`,
    requestTimeoutMs: over.requestTimeoutMs ?? 1000,
    helloTimeoutMs: 500,
    backoffBaseMs: 100,
    backoffMaxMs: 1000,
    onStatus: (s) => statuses.push(s),
    onWelcome: (w) => welcomes.push(w.resync),
    onResync: () => {
      counts.resyncs++;
    },
    onEvent: (seq, e, p) => events.push([seq, e, p]),
    onStopped: (r) => stops.push(r),
  });
  return { clock, log, rpc, events, statuses, welcomes, stops, token, counts };
}

type Harness = ReturnType<typeof harness>;

function connect(h: Harness, epoch = "e1", replay = false): void {
  const s = h.log.latest;
  s.open();
  s.receive({ t: "welcome", epoch, replay, device: { id: "dev1", name: "Pixel" } });
}

const session = { id: "s1", title: "T", updated_at: 1, busy: false, pending: 0 };

describe("hello and resume", () => {
  test("first frame is hello with the token and no resume", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.open();
    expect(h.log.latest.sent[0]).toEqual({ t: "hello", v: 1, token: "dev1.secret" });
    expect(h.statuses).toEqual(["connecting"]);
  });

  test("welcome makes the client ready and requests a first-load resync", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    expect(h.rpc.status).toBe("ready");
    expect(h.welcomes).toEqual([true]);
  });

  test("reconnect sends resume with the last seq and skips resync on replay", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.receive({ t: "ev", seq: 1, e: "busy", p: {} });
    h.log.latest.receive({ t: "ev", seq: 2, e: "busy", p: {} });
    h.log.latest.drop();
    expect(h.rpc.status).toBe("reconnecting");
    h.clock.advance(1000);
    expect(h.log.sockets).toHaveLength(2);
    h.log.latest.open();
    expect(h.log.latest.sent[0]).toEqual({
      t: "hello",
      v: 1,
      token: "dev1.secret",
      resume: { epoch: "e1", seq: 2 },
    });
    h.log.latest.receive({ t: "welcome", epoch: "e1", replay: true });
    expect(h.welcomes).toEqual([true, false]);
  });

  test("a changed epoch forces a resync and resets the cursor", () => {
    const h = harness();
    h.rpc.start();
    connect(h, "e1");
    h.log.latest.receive({ t: "ev", seq: 7, e: "busy", p: {} });
    h.log.latest.drop();
    h.clock.advance(1000);
    connect(h, "e2", true);
    expect(h.welcomes).toEqual([true, true]);
    expect(h.rpc.resumeCursor).toEqual({ epoch: "e2", seq: 0 });
  });

  test("replay=false after a resume also resyncs", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop();
    h.clock.advance(1000);
    connect(h, "e1", false);
    expect(h.welcomes).toEqual([true, true]);
  });

  test("a resync frame clears the cursor and notifies", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.receive({ t: "ev", seq: 5, e: "busy", p: {} });
    h.log.latest.receive({ t: "resync" });
    expect(h.counts.resyncs).toBe(1);
    expect(h.rpc.resumeCursor?.seq).toBe(0);
  });

  test("duplicate and stale events from a replay are dropped", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const s = h.log.latest;
    s.receive({ t: "ev", seq: 3, e: "busy", p: { n: 1 } });
    s.receive({ t: "ev", seq: 3, e: "busy", p: { n: 2 } });
    s.receive({ t: "ev", seq: 2, e: "busy", p: { n: 3 } });
    s.receive({ t: "ev", seq: 4, e: "busy", p: { n: 4 } });
    expect(h.events.map((e) => e[0])).toEqual([3, 4]);
  });

  test("garbage frames are ignored", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.receive("not json");
    h.log.latest.receive({ t: "mystery" });
    expect(h.rpc.status).toBe("ready");
  });

  test("missing hello answer times out and reconnects", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.open();
    h.clock.advance(500);
    expect(h.rpc.status).toBe("reconnecting");
    h.clock.advance(1000);
    expect(h.log.sockets).toHaveLength(2);
  });
});

describe("requests", () => {
  test("correlates responses by id and decodes results", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const a = h.rpc.call("sessions.list", {});
    const b = h.rpc.call("mode.get", {});
    const [r1, r2] = h.log.latest.sent.slice(1) as Array<{ id: string }>;
    h.log.latest.receive({ t: "res", id: r2?.id, ok: true, r: { mode: "ask" } });
    h.log.latest.receive({ t: "res", id: r1?.id, ok: true, r: [session] });
    expect(await b).toBe("ask");
    expect(await a).toEqual([session]);
  });

  test("an error response rejects with its code", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.call("mode.set", { mode: "yolo" });
    const sent = h.log.latest.last();
    h.log.latest.receive({
      t: "res",
      id: sent["id"],
      ok: false,
      err: { code: "yolo_disabled", msg: "start deck serve with --allow-yolo" },
    });
    const err = await p.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RpcError);
    expect(err instanceof RpcError && err.code).toBe("yolo_disabled");
  });

  test("a malformed result becomes bad_response", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.call("sessions.list", {});
    h.log.latest.receive({ t: "res", id: h.log.latest.last()["id"], ok: true, r: "nope" });
    const err = await p.catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("bad_response");
  });

  test("times out", async () => {
    const h = harness({ requestTimeoutMs: 200 });
    h.rpc.start();
    connect(h);
    const p = h.rpc.call("sessions.list", {});
    h.clock.advance(200);
    const err = await p.catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("timeout");
    // A late response must not throw or resolve anything.
    h.log.latest.receive({ t: "res", id: h.log.latest.last()["id"], ok: true, r: [] });
  });

  test("pending requests fail when the connection drops", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.call("sessions.list", {});
    h.log.latest.drop();
    const err = await p.catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("disconnected");
  });

  test("calls while not ready fail fast", async () => {
    const h = harness();
    h.rpc.start();
    const err = await h.rpc.call("sessions.list", {}).catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("offline");
  });

  test("request frames use the req envelope", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    void h.rpc.call("sessions.rename", { id: "s1", title: "x" });
    expect(h.log.latest.last()).toMatchObject({
      t: "req",
      m: "sessions.rename",
      p: { id: "s1", title: "x" },
    });
  });
});

describe("chat.send idempotency", () => {
  test("carries an idempotency key", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.sendChat({ session_id: "s1", text: "hi" });
    const sent = h.log.latest.last();
    expect(sent["m"]).toBe("chat.send");
    expect(sent["p"]).toEqual({ session_id: "s1", text: "hi", idempotency_key: "key-1" });
    h.log.latest.receive({ t: "res", id: sent["id"], ok: true, r: {} });
    await p;
  });

  test("a retry after a dropped connection reuses the same key", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.sendChat({ session_id: "s1", text: "hi" });
    h.log.latest.drop();
    await Promise.resolve();
    h.clock.advance(1000);
    connect(h, "e1", true);
    // Let the retry loop observe the ready state.
    await Promise.resolve();
    await Promise.resolve();
    const sent = h.log.latest.last();
    expect(sent["m"]).toBe("chat.send");
    expect(sent["p"]).toEqual({ session_id: "s1", text: "hi", idempotency_key: "key-1" });
    h.log.latest.receive({ t: "res", id: sent["id"], ok: true, r: {} });
    await p;
  });

  test("a server rejection is not retried", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.sendChat({ session_id: "s1", text: "hi" });
    const sent = h.log.latest.last();
    h.log.latest.receive({
      t: "res",
      id: sent["id"],
      ok: false,
      err: { code: "too_large", msg: "image too big" },
    });
    const err = await p.catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("too_large");
    expect(h.log.latest.sent.filter((f) => (f as { m?: string }).m === "chat.send")).toHaveLength(
      1,
    );
  });
});

describe("reconnect", () => {
  test("backoff grows exponentially and caps", () => {
    expect(backoffDelay(0, 100, 1000, 1)).toBe(100);
    expect(backoffDelay(1, 100, 1000, 1)).toBe(200);
    expect(backoffDelay(2, 100, 1000, 1)).toBe(400);
    expect(backoffDelay(10, 100, 1000, 1)).toBe(1000);
    expect(backoffDelay(3, 100, 1000, 0)).toBe(400);
  });

  test("repeated failures back off, success resets", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.drop();
    // attempt 0: ceiling 100, random 0.5 -> 75ms
    h.clock.advance(74);
    expect(h.log.sockets).toHaveLength(1);
    h.clock.advance(1);
    expect(h.log.sockets).toHaveLength(2);
    h.log.latest.drop();
    // attempt 1: ceiling 200 -> 150ms
    h.clock.advance(149);
    expect(h.log.sockets).toHaveLength(2);
    h.clock.advance(1);
    expect(h.log.sockets).toHaveLength(3);
    connect(h);
    h.log.latest.drop();
    h.clock.advance(75);
    expect(h.log.sockets).toHaveLength(4);
  });

  test("visibilitychange while waiting reconnects immediately", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop();
    expect(h.log.sockets).toHaveLength(1);
    h.rpc.notifyVisible();
    expect(h.log.sockets).toHaveLength(2);
  });

  test("visibilitychange probes a socket that looks open and recycles it if dead", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.rpc.notifyVisible();
    const probe = h.log.latest.last();
    expect(probe["m"]).toBe("mode.get");
    h.clock.advance(4000);
    await Promise.resolve();
    await Promise.resolve();
    expect(h.log.sockets).toHaveLength(2);
  });

  test("a healthy socket survives the visibility probe", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.rpc.notifyVisible();
    h.log.latest.receive({ t: "res", id: h.log.latest.last()["id"], ok: true, r: { mode: "ask" } });
    await Promise.resolve();
    await Promise.resolve();
    h.clock.advance(5000);
    expect(h.log.sockets).toHaveLength(1);
    expect(h.rpc.status).toBe("ready");
  });
});

describe("auth failures", () => {
  test("close 4401 stops without reconnecting", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop(4401);
    h.clock.advance(60_000);
    expect(h.stops).toEqual(["unauthorized"]);
    expect(h.rpc.status).toBe("stopped");
    expect(h.log.sockets).toHaveLength(1);
  });

  test("close 4403 reports revoked", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop(4403);
    expect(h.stops).toEqual(["revoked"]);
    expect(h.rpc.status).toBe("stopped");
  });

  test("4401 before welcome (bad token) also stops", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.open();
    h.log.latest.drop(4401);
    expect(h.stops).toEqual(["unauthorized"]);
  });

  test("no stored token stops immediately", () => {
    const h = harness();
    h.token.value = null;
    h.rpc.start();
    expect(h.stops).toEqual(["unauthorized"]);
    expect(h.log.sockets).toHaveLength(0);
  });

  test("slow-consumer close 4408 reconnects with resume", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.receive({ t: "ev", seq: 9, e: "busy", p: {} });
    h.log.latest.drop(4408);
    h.clock.advance(1000);
    h.log.latest.open();
    expect(h.log.latest.sent[0]).toMatchObject({ resume: { epoch: "e1", seq: 9 } });
  });
});
