import { describe, expect, test } from "bun:test";
import { generateStaticKey } from "./e2e/noise";
import { RpcError, Session3 } from "./session3";
import { FakeComputer, FakeRpcError, pipePair } from "./transport/testkit";
import { TransportError } from "./transport/transport";

const CID = "Zk3p0Jq8vYb1nT5cR2sWxA";

async function connect(
  over: {
    allowed?: "phone" | "none";
    handle?: (m: string, pj: string | undefined, p: unknown) => unknown;
    wrongComputerKey?: boolean;
  } = {},
) {
  const phone = await generateStaticKey();
  const computerKey = await generateStaticKey();
  const [phoneEnd, computerEnd] = pipePair();
  const computer = new FakeComputer(computerEnd, {
    cid: CID,
    staticKey: computerKey,
    ...(over.allowed === "none" ? { allowed: [] } : { allowed: [phone.pub] }),
    ...(over.handle !== undefined ? { handle: over.handle } : {}),
  });
  const pinned = over.wrongComputerKey ? (await generateStaticKey()).pub : computerKey.pub;
  const session = Session3.open({
    transport: phoneEnd,
    phoneKey: phone,
    computerStatic: pinned,
    cid: CID,
    handshakeTimeoutMs: 2000,
  });
  return { session, computer, phone, phoneEnd, computerEnd };
}

describe("Session3", () => {
  test("handshake, hello and welcome", async () => {
    const c = await connect();
    const s = await c.session;
    expect(s.welcome.server.cid).toBe(CID);
    expect(s.welcome.server.projects[0]?.name).toBe("deck");
    expect(s.welcome.device.id).toBe("dev_1");
    expect(c.computer.helloSeen).toEqual({ t: "hello", v: 3 });
    expect(c.computer.phoneStatic).toEqual(c.phone.pub);
  });

  test("a resume point is sent in the hello", async () => {
    const phone = await generateStaticKey();
    const key = await generateStaticKey();
    const [a, b] = pipePair();
    const computer = new FakeComputer(b, { cid: CID, staticKey: key });
    const s = await Session3.open({
      transport: a,
      phoneKey: phone,
      computerStatic: key.pub,
      cid: CID,
      resume: { epoch: "ep_old", seq: 41, focus: { project_id: "prj_a", session_id: "ses_1" } },
    });
    expect(computer.helloSeen).toEqual({
      t: "hello",
      v: 3,
      resume: { epoch: "ep_old", seq: 41, focus: { project_id: "prj_a", session_id: "ses_1" } },
    });
    expect(s.epoch).toBe("ep_test");
  });

  test("requests carry pj only for project methods and return results", async () => {
    const c = await connect({
      handle: (m, pj, p) => ({ m, pj: pj ?? null, p: p ?? null }),
    });
    const s = await c.session;
    expect(await s.call("inbox.get")).toEqual({ m: "inbox.get", pj: null, p: null });
    expect(await s.call("chat.send", { session_id: "s1", text: "hi" }, "prj_a")).toEqual({
      m: "chat.send",
      pj: "prj_a",
      p: { session_id: "s1", text: "hi" },
    });
  });

  test("a refused request rejects with its code", async () => {
    const c = await connect({
      handle: () => {
        throw new FakeRpcError("assertion_required", "This needs your fingerprint or face.");
      },
    });
    const s = await c.session;
    const err = await s.call("approvals.answer", {}, "prj_a").catch((e: unknown) => e);
    expect(err).toBeInstanceOf(RpcError);
    expect((err as RpcError).code).toBe("assertion_required");
    expect((err as RpcError).message).toContain("fingerprint");
  });

  test("concurrent calls are answered to the right caller, and frames stay in order", async () => {
    const seen: string[] = [];
    const c = await connect({
      handle: async (m, _pj, p) => {
        seen.push(String((p as { n: number }).n));
        await new Promise((r) => setTimeout(r, m === "slow" ? 30 : 0));
        return { n: (p as { n: number }).n };
      },
    });
    const s = await c.session;
    const results = await Promise.all([
      s.call("slow", { n: 1 }),
      s.call("fast", { n: 2 }),
      s.call("fast", { n: 3 }),
      s.call("fast", { n: 4 }),
    ]);
    expect(results).toEqual([{ n: 1 }, { n: 2 }, { n: 3 }, { n: 4 }]);
    expect(seen).toEqual(["1", "2", "3", "4"]);
  });

  test("a result larger than one Noise message arrives whole", async () => {
    const big = "x".repeat(300_000);
    const c = await connect({ handle: () => ({ big }) });
    const s = await c.session;
    expect(((await s.call("changes.get")) as { big: string }).big.length).toBe(300_000);
    // And a large request goes out in chunks the computer reassembles.
    await s.call("chat.send", { text: big }, "prj_a");
    const last = c.computer.received[c.computer.received.length - 1] as { p: { text: string } };
    expect(last.p.text.length).toBe(300_000);
  });

  test("events reach listeners with their sequence, and track the resume point", async () => {
    const c = await connect();
    const s = await c.session;
    const got: Array<{ seq: number; name: string; pj: string | undefined }> = [];
    s.onEvent((ev) => got.push({ seq: ev.seq, name: ev.name, pj: ev.pj }));
    await c.computer.send({
      t: "ev",
      seq: 7,
      e: "busy",
      pj: "prj_a",
      p: { session_id: "s1", busy: true },
    });
    await c.computer.send({ t: "ev", seq: 8, e: "inbox.changed", p: [] });
    await new Promise((r) => setTimeout(r, 20));
    expect(got).toEqual([
      { seq: 7, name: "busy", pj: "prj_a" },
      { seq: 8, name: "inbox.changed", pj: undefined },
    ]);
    expect(s.resumePoint()).toEqual({ epoch: "ep_test", seq: 8 });
    expect(s.resumePoint({ project_id: "prj_a", session_id: "s1" }).focus?.session_id).toBe("s1");
  });

  test("a resync frame is flagged and announced", async () => {
    const c = await connect();
    const s = await c.session;
    const names: string[] = [];
    s.onEvent((ev) => names.push(ev.name));
    await c.computer.send({ t: "resync" });
    await new Promise((r) => setTimeout(r, 20));
    expect(s.resyncNeeded).toBe(true);
    expect(names).toContain("resync");
  });

  test("event subscriptions unsubscribe independently without skipping active listeners", async () => {
    const c = await connect();
    const s = await c.session;
    const names: string[] = [];
    const removed = s.onEvent(() => names.push("removed"));
    removed();
    removed();
    let removeSelf = () => {};
    removeSelf = s.onEvent(() => {
      names.push("self");
      removeSelf();
    });
    s.onEvent((event) => names.push(event.name));
    await c.computer.send({ t: "ev", seq: 1, e: "inbox.changed", p: [] });
    await c.computer.send({ t: "resync" });
    await new Promise((r) => setTimeout(r, 20));
    expect(names).toEqual(["self", "inbox.changed", "resync"]);
    s.close();
  });

  test("close subscriptions detach and late registrations fire once", async () => {
    const c = await connect();
    const s = await c.session;
    const closed: string[] = [];
    const remove = s.onClose(() => closed.push("removed"));
    remove();
    s.onClose(() => closed.push("active"));
    s.close();
    const removeLate = s.onClose(() => closed.push("late"));
    removeLate();
    s.close();
    expect(closed).toEqual(["active", "late"]);
  });

  test("the computer closing fails pending calls with the close code", async () => {
    const c = await connect({ handle: () => new Promise(() => undefined) });
    const s = await c.session;
    const closes: number[] = [];
    s.onClose((i) => closes.push(i.code));
    const pending = s.call("never").catch((e: unknown) => e);
    await new Promise((r) => setTimeout(r, 10));
    c.computerEnd.close(4403, "revoked");
    const err = await pending;
    expect(err).toBeInstanceOf(TransportError);
    expect((err as TransportError).code).toBe(4403);
    expect(closes).toEqual([4403]);
    expect(s.isClosed).toBe(true);
    await expect(s.call("later")).rejects.toBeInstanceOf(TransportError);
  });

  test("a computer that does not know this phone refuses with 4401", async () => {
    const c = await connect({ allowed: "none" });
    const err = await c.session.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(TransportError);
    expect((err as TransportError).code).toBe(4401);
  });

  test("a phone that pinned a different computer key cannot finish the handshake", async () => {
    const c = await connect({ wrongComputerKey: true });
    const err = await c.session.catch((e: unknown) => e);
    expect(err).toBeInstanceOf(Error);
    expect(c.phoneEnd.closed).not.toBeNull();
  });

  test("a call that is never answered times out", async () => {
    const phone = await generateStaticKey();
    const key = await generateStaticKey();
    const [a, b] = pipePair();
    new FakeComputer(b, { cid: CID, staticKey: key, handle: () => new Promise(() => undefined) });
    const s = await Session3.open({
      transport: a,
      phoneKey: phone,
      computerStatic: key.pub,
      cid: CID,
      callTimeoutMs: 30,
    });
    const err = await s.call("stuck").catch((e: unknown) => e);
    expect((err as RpcError).code).toBe("timeout");
  });

  test("close() ends the session and the transport", async () => {
    const c = await connect();
    const s = await c.session;
    s.close();
    expect(s.isClosed).toBe(true);
    expect(c.phoneEnd.closed).not.toBeNull();
  });
});
