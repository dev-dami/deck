import { describe, expect, test } from "bun:test";
import { FakeClock, flush, SocketLog, welcomeFrame } from "./fakes";
import {
  backoffDelay,
  type ConnStatus,
  closeReasonCode,
  RpcClient,
  RpcError,
  retryAfterMs,
  type StopReason,
} from "./rpc";

function harness(over: { requestTimeoutMs?: number; focus?: string | null } = {}) {
  const clock = new FakeClock();
  const log = new SocketLog();
  const token = { value: "dev1.secret" as string | null };
  const focus = { value: over.focus ?? null };
  const events: Array<[number, string, unknown]> = [];
  const statuses: ConnStatus[] = [];
  const welcomes: boolean[] = [];
  const stops: StopReason[] = [];
  const warnings: string[] = [];
  const counts = { resyncs: 0 };
  let n = 0;
  const rpc = new RpcClient({
    url: "ws://x/api/v1/ws",
    getToken: () => token.value,
    getFocus: () => focus.value,
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
    warn: (m) => warnings.push(m),
  });
  return { clock, log, rpc, events, statuses, welcomes, stops, token, focus, counts, warnings };
}

type Harness = ReturnType<typeof harness>;

function connect(h: Harness, epoch = "e1", replay = false): void {
  const s = h.log.latest;
  s.open();
  s.receive(welcomeFrame({ epoch, replay }));
}

const session = {
  id: "s1",
  title: "T",
  updated_at: "2026-10-12T09:00:00Z",
  busy: false,
  pending: 0,
  mode: "ask",
};

describe("hello and resume", () => {
  test("first frame is hello v2 with the token and no resume", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.open();
    expect(h.log.latest.sent[0]).toEqual({ t: "hello", v: 2, token: "dev1.secret" });
    expect(h.statuses).toEqual(["connecting"]);
  });

  test("welcome makes the client ready and requests a first-load resync", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    expect(h.rpc.status).toBe("ready");
    expect(h.welcomes).toEqual([true]);
  });

  test("reconnect sends resume with the last seq and focus, and skips resync on replay", () => {
    const h = harness({ focus: "ses_8f2a" });
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
      v: 2,
      token: "dev1.secret",
      resume: { epoch: "e1", seq: 2, focus: "ses_8f2a" },
    });
    h.log.latest.receive(welcomeFrame({ epoch: "e1", replay: true }));
    expect(h.welcomes).toEqual([true, false]);
  });

  test("resume has no focus field when nothing is open", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop();
    h.clock.advance(1000);
    h.log.latest.open();
    expect(h.log.latest.sent[0]).toEqual({
      t: "hello",
      v: 2,
      token: "dev1.secret",
      resume: { epoch: "e1", seq: 0 },
    });
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

  test("garbage and invalid frames are dropped with a warning, never a crash", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.receive("not json");
    h.log.latest.receive({ t: "mystery" });
    h.log.latest.receive({ t: "ev", e: "busy", p: {} });
    expect(h.rpc.status).toBe("ready");
    expect(h.warnings).toHaveLength(3);
    expect(h.events).toHaveLength(0);
  });

  test("a welcome that fails validation is dropped and the hello times out", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.open();
    h.log.latest.receive({ t: "welcome", epoch: "e1" });
    expect(h.rpc.status).toBe("connecting");
    h.clock.advance(500);
    expect(h.rpc.status).toBe("reconnecting");
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
    const b = h.rpc.call("mode.get", { session_id: "s1" });
    const [r1, r2] = h.log.latest.sent.slice(1) as Array<{ id: string }>;
    h.log.latest.receive({ t: "res", id: r2?.id, ok: true, r: { mode: "ask" } });
    h.log.latest.receive({ t: "res", id: r1?.id, ok: true, r: [session] });
    expect(await b).toEqual({ mode: "ask" });
    expect(await a).toEqual([session]);
  });

  test("request ids are strings", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    void h.rpc.call("sessions.list", {});
    expect(typeof h.log.latest.last()["id"]).toBe("string");
  });

  test("an error response rejects with its code and message", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.call("mode.set", { session_id: "s1", mode: "yolo" });
    const sent = h.log.latest.last();
    h.log.latest.receive({
      t: "res",
      id: sent["id"],
      ok: false,
      err: { code: "yolo_disabled", msg: "This phone may not switch to yolo." },
    });
    const err = await p.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RpcError);
    expect(err instanceof RpcError && err.code).toBe("yolo_disabled");
    expect(err instanceof RpcError && err.message).toContain("yolo");
  });

  test("a malformed result becomes bad_response", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.call("sessions.list", {});
    h.log.latest.receive({ t: "res", id: h.log.latest.last()["id"], ok: true, r: "nope" });
    const err = await p.catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("bad_response");
    expect(h.warnings.some((w) => w.includes("sessions.list"))).toBe(true);
  });

  test("a {} result may arrive without r", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.call("sessions.focus", { id: "s1" });
    h.log.latest.receive({ t: "res", id: h.log.latest.last()["id"], ok: true });
    expect(await p).toEqual({});
  });

  test("times out after 15 s by default", async () => {
    const clock = new FakeClock();
    const log = new SocketLog();
    const rpc = new RpcClient({
      url: "ws://x",
      getToken: () => "t",
      socketFactory: log.factory,
      clock,
    });
    rpc.start();
    log.latest.open();
    log.latest.receive(welcomeFrame({}));
    const p = rpc.call("sessions.list", {});
    clock.advance(14_999);
    expect(rpc.inFlight).toBe(1);
    clock.advance(1);
    const err = await p.catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("timeout");
    // A late response must not throw or resolve anything.
    log.latest.receive({ t: "res", id: log.latest.last()["id"], ok: true, r: [] });
  });

  test("at most 8 requests are in flight; the rest wait for a slot", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const calls = Array.from({ length: 10 }, () => h.rpc.call("sessions.list", {}));
    expect(h.log.latest.requests("sessions.list")).toHaveLength(8);
    expect(h.rpc.inFlight).toBe(8);
    const first = h.log.latest.requests("sessions.list")[0];
    h.log.latest.receive({ t: "res", id: first?.["id"], ok: true, r: [] });
    expect(h.log.latest.requests("sessions.list")).toHaveLength(9);
    expect(await calls[0]).toEqual([]);
    // A timeout also frees a slot.
    h.clock.advance(1000);
    await flush();
    expect(h.log.latest.requests("sessions.list")).toHaveLength(10);
    h.clock.advance(1000);
    await Promise.all(calls.map((c) => c.catch(() => null)));
  });

  test("pending and queued requests fail when the connection drops", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const calls = Array.from({ length: 9 }, () => h.rpc.call("sessions.list", {}));
    h.log.latest.drop();
    const codes = await Promise.all(
      calls.map((c) =>
        c.then(() => "ok").catch((e: unknown) => (e instanceof RpcError ? e.code : "?")),
      ),
    );
    expect(codes).toEqual(Array(9).fill("disconnected"));
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
    expect(h.log.latest.last()).toEqual({
      t: "req",
      id: h.log.latest.last()["id"],
      m: "sessions.rename",
      p: { id: "s1", title: "x" },
    });
  });
});

describe("chat.send idempotency", () => {
  test("carries an idempotency key and returns the message id", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.sendChat({ session_id: "s1", text: "hi" });
    const sent = h.log.latest.last();
    expect(sent["m"]).toBe("chat.send");
    expect(sent["p"]).toEqual({ session_id: "s1", text: "hi", idempotency_key: "key-1" });
    h.log.latest.receive({ t: "res", id: sent["id"], ok: true, r: { message_id: "m9" } });
    expect(await p).toEqual({ message_id: "m9" });
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
    await flush();
    const sent = h.log.latest.last();
    expect(sent["m"]).toBe("chat.send");
    expect(sent["p"]).toEqual({ session_id: "s1", text: "hi", idempotency_key: "key-1" });
    h.log.latest.receive({ t: "res", id: sent["id"], ok: true, r: { message_id: "m1" } });
    await p;
  });

  test("gives up after three retries, every one with the first key", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    const p = h.rpc.sendChat({ session_id: "s1", text: "hi" });
    const seen: unknown[] = [];
    for (let i = 0; i < 4; i++) {
      await flush();
      const sent = h.log.latest.last();
      expect(sent["m"]).toBe("chat.send");
      seen.push((sent["p"] as { idempotency_key: string }).idempotency_key);
      h.clock.advance(30_000);
    }
    const err = await p.catch((e: unknown) => e);
    expect(err instanceof RpcError && err.code).toBe("timeout");
    expect(seen).toEqual(["key-1", "key-1", "key-1", "key-1"]);
    expect(h.log.latest.requests("chat.send")).toHaveLength(4);
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
    expect(h.log.latest.requests("chat.send")).toHaveLength(1);
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

  test("the cap is 60 s whatever the options say", () => {
    const clock = new FakeClock();
    const log = new SocketLog();
    const rpc = new RpcClient({
      url: "ws://x",
      getToken: () => "t",
      socketFactory: log.factory,
      clock,
      random: () => 1,
      backoffBaseMs: 100_000,
      backoffMaxMs: 600_000,
    });
    rpc.start();
    log.latest.drop();
    expect(clock.nextDelay()).toBe(60_000);
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
    expect(h.log.latest.last()["m"]).toBe("push.status");
    h.clock.advance(4000);
    await flush();
    expect(h.log.sockets).toHaveLength(2);
  });

  test("a healthy socket survives the visibility probe", async () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.rpc.notifyVisible();
    h.log.latest.receive({
      t: "res",
      id: h.log.latest.last()["id"],
      ok: true,
      r: { available: false, enabled: false, approvals: false, done: false },
    });
    await flush();
    h.clock.advance(5000);
    expect(h.log.sockets).toHaveLength(1);
    expect(h.rpc.status).toBe("ready");
  });
});

describe("close codes (R1)", () => {
  test("4429 backs off with the server's wait as a floor and keeps the token", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop(4429, "30");
    expect(h.rpc.status).toBe("reconnecting");
    expect(h.stops).toEqual([]);
    const delay = h.clock.nextDelay() ?? 0;
    // 30 s floor plus up to 25% jitter (random 0.5 -> 3750 ms), under the cap.
    expect(delay).toBe(33_750);
    h.clock.advance(delay);
    h.log.latest.open();
    expect(h.log.latest.sent[0]).toMatchObject({ t: "hello", token: "dev1.secret" });
  });

  test("4429 without a number uses the normal backoff; a huge wait is capped at 60 s", () => {
    const a = harness();
    a.rpc.start();
    connect(a);
    a.log.latest.drop(4429, "");
    expect(a.clock.nextDelay()).toBe(75);

    const b = harness();
    b.rpc.start();
    connect(b);
    b.log.latest.drop(4429, "3600");
    expect(b.clock.nextDelay()).toBe(60_000);
  });

  test("retryAfterMs and closeReasonCode parse the reason", () => {
    expect(retryAfterMs("30")).toBe(30_000);
    expect(retryAfterMs(" 5 ")).toBe(5_000);
    expect(retryAfterMs("")).toBeNull();
    expect(retryAfterMs("soon")).toBeNull();
    expect(closeReasonCode("token_invalid")).toBe("token_invalid");
    expect(closeReasonCode("rate_limited: 30")).toBe("rate_limited");
    expect(closeReasonCode("")).toBe("");
  });

  test("4401 token_invalid stops and asks to forget the token", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop(4401, "token_invalid");
    h.clock.advance(60_000);
    expect(h.stops).toEqual(["token_invalid"]);
    expect(h.rpc.status).toBe("stopped");
    expect(h.log.sockets).toHaveLength(1);
  });

  test("4401 with an empty reason (a proxy stripped it) keeps the token and retries", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.open();
    h.log.latest.drop(4401);
    expect(h.stops).toEqual([]);
    expect(h.rpc.status).toBe("reconnecting");
    // Same path as a 4429 without a number: ordinary backoff.
    expect(h.clock.nextDelay()).toBe(75);
    h.clock.advance(75);
    expect(h.log.sockets).toHaveLength(2);
    h.log.latest.open();
    expect(h.log.latest.sent[0]).toMatchObject({ t: "hello", token: "dev1.secret" });
  });

  test("4401 with another code is a retry that keeps the token", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop(4401, "rate_limited");
    expect(h.stops).toEqual([]);
    expect(h.rpc.status).toBe("reconnecting");
    h.clock.advance(1000);
    expect(h.log.sockets).toHaveLength(2);
  });

  test("4403 reports revoked", () => {
    const h = harness();
    h.rpc.start();
    connect(h);
    h.log.latest.drop(4403, "revoked");
    expect(h.stops).toEqual(["revoked"]);
    expect(h.rpc.status).toBe("stopped");
  });

  test("4400 reports a protocol mismatch and never retries", () => {
    const h = harness();
    h.rpc.start();
    h.log.latest.open();
    h.log.latest.drop(4400, "unsupported protocol version");
    h.clock.advance(120_000);
    expect(h.stops).toEqual(["protocol"]);
    expect(h.log.sockets).toHaveLength(1);
  });

  test("no stored token stops immediately", () => {
    const h = harness();
    h.token.value = null;
    h.rpc.start();
    expect(h.stops).toEqual(["no_token"]);
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
