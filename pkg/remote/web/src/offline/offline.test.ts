import { beforeEach, describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { b64Decode, b64Encode, b64urlEncode } from "../b64";
import { generateStaticKey, type StaticKey } from "../e2e/noise";
import { open, seal } from "../e2e/seal";
import { fakeComputerRecord } from "../fakes3";
import type { QueuedAction } from "../protocol3";
import { prologueFor } from "../session3";
import type { ComputerRecord } from "../store/computers";
import { memoryBackend, setBackendForTests } from "../store/db";
import { outboxList } from "../store/outbox";
import {
  DEFAULT_EXPIRY_MS,
  flushOutbox,
  MAX_QUEUED_PER_DEVICE,
  NEVER_QUEUED,
  QueueError,
  queueAction,
  validateAction,
} from "./queue";
import { loadSnapshot, offlineLabel, savedSnapshot } from "./snapshot";
import { uuidv7 } from "./uuid";

const HOUR = 3_600_000;
const NOW = Date.UTC(2026, 9, 14, 21, 0, 0);

interface World {
  computerKey: StaticKey;
  phoneKey: StaticKey;
  computer: ComputerRecord;
}

async function world(over: Partial<ComputerRecord> = {}): Promise<World> {
  const computerKey = await generateStaticKey();
  const phoneKey = await generateStaticKey();
  const computer = fakeComputerRecord({
    cid: "Zk3p0Jq8vYb1nT5cR2sWxA",
    relayUrl: "https://app.example.dev/",
    relayToken: "rt_secret",
    deviceId: "dev_7a1f",
    computerStatic: b64urlEncode(computerKey.pub),
    phoneKey: { pub: phoneKey.pub, priv: phoneKey.priv },
    ...over,
  });
  return { computerKey, phoneKey, computer };
}

interface Seen {
  url: string;
  auth: string | null;
  body: { id: string; expires_at: string; b64: string };
}

function relay(status: number | "network", seen: Seen[] = []): typeof fetch {
  return (async (input: RequestInfo | URL, init?: RequestInit) => {
    if (status === "network") throw new TypeError("network down");
    seen.push({
      url: String(input),
      auth: new Headers(init?.headers).get("authorization"),
      body: JSON.parse(String(init?.body)),
    });
    return new Response(JSON.stringify({ ok: status < 300 }), { status });
  }) as typeof fetch;
}

// The computer's side: open a mail with the phone's pinned key.
async function readMail(w: World, b64: string): Promise<QueuedAction> {
  const message = b64Decode(b64);
  if (message === null) throw new Error("bad base64");
  const { plaintext } = await open({
    recipientStatic: w.computerKey,
    senderStaticPub: w.phoneKey.pub,
    prologue: prologueFor(w.computer.cid),
    message,
  });
  return JSON.parse(new TextDecoder().decode(plaintext)) as QueuedAction;
}

beforeEach(() => setBackendForTests(memoryBackend()));

describe("queueAction", () => {
  test("seals the action for the computer and posts it to the mailbox", async () => {
    const w = await world();
    const seen: Seen[] = [];
    const res = await queueAction(
      w.computer,
      {
        kind: "chat.send",
        pj: "prj_a",
        sessionId: "ses_1",
        payload: { text: "finish the refactor" },
      },
      { fetch: relay(200, seen), now: () => NOW },
    );
    expect(res.state).toBe("sent");
    expect(seen).toHaveLength(1);
    expect(seen[0]?.url).toBe("https://app.example.dev/r/v1/c/Zk3p0Jq8vYb1nT5cR2sWxA/mail");
    expect(seen[0]?.auth).toBe("Bearer rt_secret");
    expect(seen[0]?.body.id).toBe(res.qid);
    expect(seen[0]?.body.expires_at).toBe(new Date(NOW + DEFAULT_EXPIRY_MS).toISOString());

    const action = await readMail(w, seen[0]?.body.b64 ?? "");
    expect(action).toMatchObject({
      qid: res.qid,
      device_id: "dev_7a1f",
      kind: "chat.send",
      pj: "prj_a",
      session_id: "ses_1",
      payload: { text: "finish the refactor" },
      created_at: new Date(NOW).toISOString(),
    });
    expect(validateAction(action)).toBeNull();
    // Nothing waits locally once it is on the relay.
    expect(await outboxList(w.computer.cid)).toHaveLength(0);
  });

  test("nobody else can read the mail, and the relay sees no content", async () => {
    const w = await world();
    const seen: Seen[] = [];
    await queueAction(
      w.computer,
      { kind: "chat.send", pj: "prj_a", sessionId: "s", payload: { text: "SECRET-PROMPT" } },
      { fetch: relay(200, seen), now: () => NOW },
    );
    const b64 = seen[0]?.body.b64 ?? "";
    expect(atob(b64)).not.toContain("SECRET-PROMPT");
    const stranger = await generateStaticKey();
    const message = b64Decode(b64);
    await expect(
      open({
        recipientStatic: stranger,
        senderStaticPub: w.phoneKey.pub,
        prologue: prologueFor(w.computer.cid),
        message: message ?? new Uint8Array(),
      }),
    ).rejects.toThrow();
  });

  test("expiry: default 8 hours, choices honoured, never over 24", async () => {
    const w = await world();
    const seen: Seen[] = [];
    const f = relay(200, seen);
    await queueAction(
      w.computer,
      { kind: "chat.continue", pj: "p", sessionId: "s", expiresInMs: 1 * HOUR },
      { fetch: f, now: () => NOW },
    );
    await queueAction(
      w.computer,
      { kind: "chat.continue", pj: "p", sessionId: "s", expiresInMs: 90 * HOUR },
      { fetch: f, now: () => NOW },
    );
    expect(seen[0]?.body.expires_at).toBe(new Date(NOW + 1 * HOUR).toISOString());
    expect(seen[1]?.body.expires_at).toBe(new Date(NOW + 24 * HOUR).toISOString());
  });

  test("a queued approval needs the id and digest of what the person saw", async () => {
    const w = await world();
    const seen: Seen[] = [];
    const f = relay(200, seen);
    await expect(
      queueAction(
        w.computer,
        { kind: "approvals.answer", pj: "p", sessionId: "s", payload: { decision: "allow" } },
        { fetch: f },
      ),
    ).rejects.toMatchObject({ code: "invalid" });
    await queueAction(
      w.computer,
      {
        kind: "approvals.answer",
        pj: "p",
        sessionId: "s",
        payload: { decision: "allow" },
        precond: { approval_id: "apr_1", digest: "a".repeat(64) },
      },
      { fetch: f, now: () => NOW },
    );
    const action = await readMail(w, seen[0]?.body.b64 ?? "");
    expect(action.precond).toEqual({ approval_id: "apr_1", digest: "a".repeat(64) });
  });

  test("interrupt, cancel and yolo can never be queued", async () => {
    const w = await world();
    for (const kind of NEVER_QUEUED) {
      const err = await queueAction(
        w.computer,
        { kind: kind as never, pj: "p", sessionId: "s" },
        { fetch: relay(200) },
      ).catch((e: unknown) => e);
      expect(err).toBeInstanceOf(QueueError);
      expect((err as QueueError).code).toBe("not_queueable");
    }
  });

  test("a computer that is not paired here cannot be queued for", async () => {
    const w = await world();
    for (const bad of [
      { relayToken: null },
      { deviceId: null },
      { computerStatic: "!!!" },
    ] as Array<Partial<ComputerRecord>>) {
      const c = { ...w.computer, ...bad };
      await expect(
        queueAction(c, { kind: "chat.continue", pj: "p", sessionId: "s" }, { fetch: relay(200) }),
      ).rejects.toMatchObject({
        code: "not_paired",
      });
    }
    const { phoneKey: _no, ...noKey } = w.computer;
    await expect(
      queueAction(noKey, { kind: "chat.continue", pj: "p", sessionId: "s" }, { fetch: relay(200) }),
    ).rejects.toMatchObject({
      code: "not_paired",
    });
  });

  test("an offline phone keeps the sealed action in its outbox, then sends it", async () => {
    const w = await world();
    const res = await queueAction(
      w.computer,
      { kind: "chat.send", pj: "p", sessionId: "s", payload: { text: "later" } },
      { fetch: relay("network"), now: () => NOW },
    );
    expect(res.state).toBe("outbox");
    const waiting = await outboxList(w.computer.cid);
    expect(waiting).toHaveLength(1);
    expect(waiting[0]?.attempts).toBe(1);
    // What is stored is ciphertext.
    expect(atob(waiting[0]?.b64 ?? "")).not.toContain("later");

    const stillOffline = await flushOutbox(w.computer, {
      fetch: relay("network"),
      now: () => NOW + 1000,
    });
    expect(stillOffline).toEqual({ sent: [], dropped: [], remaining: 1 });
    expect((await outboxList())[0]?.attempts).toBe(2);

    const seen: Seen[] = [];
    const back = await flushOutbox(w.computer, { fetch: relay(200, seen), now: () => NOW + 2000 });
    expect(back).toEqual({ sent: [res.qid], dropped: [], remaining: 0 });
    expect((await readMail(w, seen[0]?.body.b64 ?? "")).payload).toEqual({ text: "later" });
    expect(await outboxList()).toHaveLength(0);
  });

  test("outbox entries are sent oldest first and expired ones are dropped", async () => {
    const w = await world();
    const offline = relay("network");
    const a = await queueAction(
      w.computer,
      { kind: "chat.continue", pj: "p", sessionId: "s", expiresInMs: 1 * HOUR },
      { fetch: offline, now: () => NOW },
    );
    const b = await queueAction(
      w.computer,
      { kind: "chat.continue", pj: "p", sessionId: "s2", expiresInMs: 8 * HOUR },
      { fetch: offline, now: () => NOW + 1000 },
    );
    const seen: Seen[] = [];
    const res = await flushOutbox(w.computer, {
      fetch: relay(200, seen),
      now: () => NOW + 2 * HOUR,
    });
    expect(res.dropped).toEqual([a.qid]);
    expect(res.sent).toEqual([b.qid]);
    expect(seen.map((s) => s.body.id)).toEqual([b.qid]);
  });

  test("a rate limit or server error is retried; a definite no is explained", async () => {
    const w = await world();
    const input = { kind: "chat.continue", pj: "p", sessionId: "s" } as const;
    expect(
      (await queueAction(w.computer, input, { fetch: relay(429), now: () => NOW })).state,
    ).toBe("outbox");
    expect(
      (await queueAction(w.computer, input, { fetch: relay(503), now: () => NOW })).state,
    ).toBe("outbox");
    const before = (await outboxList()).length;
    await expect(queueAction(w.computer, input, { fetch: relay(401) })).rejects.toMatchObject({
      code: "revoked",
    });
    await expect(queueAction(w.computer, input, { fetch: relay(413) })).rejects.toMatchObject({
      code: "too_large",
    });
    await expect(queueAction(w.computer, input, { fetch: relay(400) })).rejects.toMatchObject({
      code: "invalid",
    });
    await expect(queueAction(w.computer, input, { fetch: relay(404) })).rejects.toMatchObject({
      code: "rejected",
    });
    expect(await outboxList()).toHaveLength(before); // refusals are not kept
  });

  test("twenty waiting actions is the limit", async () => {
    const w = await world();
    const input = { kind: "chat.continue", pj: "p", sessionId: "s" } as const;
    for (let i = 0; i < MAX_QUEUED_PER_DEVICE; i++) {
      await queueAction(w.computer, input, { fetch: relay("network"), now: () => NOW + i });
    }
    await expect(
      queueAction(w.computer, input, { fetch: relay("network"), now: () => NOW + 99 }),
    ).rejects.toMatchObject({
      code: "too_many",
    });
  });

  test("a revoked phone stops flushing and says so", async () => {
    const w = await world();
    await queueAction(
      w.computer,
      { kind: "chat.continue", pj: "p", sessionId: "s" },
      { fetch: relay("network"), now: () => NOW },
    );
    await expect(
      flushOutbox(w.computer, { fetch: relay(401), now: () => NOW }),
    ).rejects.toMatchObject({ code: "revoked" });
    expect(await outboxList()).toHaveLength(1); // kept: nothing was delivered
  });
});

describe("validateAction", () => {
  const base: QueuedAction = {
    qid: "q",
    device_id: "d",
    created_at: new Date(NOW).toISOString(),
    expires_at: new Date(NOW + HOUR).toISOString(),
    kind: "chat.send",
    payload: {},
    pj: "p",
    session_id: "s",
  };
  test("mirrors the computer's rules", () => {
    expect(validateAction(base)).toBeNull();
    expect(validateAction({ ...base, qid: "" })).not.toBeNull();
    expect(validateAction({ ...base, session_id: "" })).not.toBeNull();
    expect(validateAction({ ...base, kind: "session.new", session_id: "" })).toBeNull();
    expect(validateAction({ ...base, kind: "session.new", pj: "" })).not.toBeNull();
    expect(validateAction({ ...base, kind: "queue.cancel", pj: "", session_id: "" })).toBeNull();
    expect(
      validateAction({ ...base, expires_at: new Date(NOW + 25 * HOUR).toISOString() }),
    ).not.toBeNull();
    expect(validateAction({ ...base, expires_at: new Date(NOW - 1).toISOString() })).not.toBeNull();
  });
});

describe("snapshots", () => {
  const fixture = JSON.parse(
    readFileSync(
      new URL("../../../wire/testdata/v3_entity_snapshot.json", import.meta.url),
      "utf8",
    ),
  ) as unknown;

  async function sealedSnapshot(w: World, key?: StaticKey): Promise<string> {
    // The computer is the initiator; the phone opens it with the computer's pinned key.
    const sealed = await seal({
      senderStatic: key ?? w.computerKey,
      recipientStaticPub: w.phoneKey.pub,
      prologue: prologueFor(w.computer.cid),
      plaintext: new TextEncoder().encode(JSON.stringify(fixture)),
    });
    return b64Encode(sealed);
  }

  function snapRelay(status: number, body?: unknown, calls: string[] = []): typeof fetch {
    return (async (input: RequestInfo | URL, init?: RequestInit) => {
      calls.push(`${String(input)} ${new Headers(init?.headers).get("authorization")}`);
      return new Response(body === undefined ? null : JSON.stringify(body), { status });
    }) as typeof fetch;
  }

  test("opens the computer's snapshot, saves it, and reports its state", async () => {
    const w = await world();
    const calls: string[] = [];
    const got = await loadSnapshot(w.computer, {
      fetch: snapRelay(
        200,
        {
          b64: await sealedSnapshot(w),
          computer: { online: false, last_seen: "2026-10-14T21:04:55Z" },
        },
        calls,
      ),
      now: () => NOW,
    });
    expect(calls).toEqual([
      "https://app.example.dev/r/v1/c/Zk3p0Jq8vYb1nT5cR2sWxA/snap Bearer rt_secret",
    ]);
    expect(got?.stale).toBe(false);
    expect(got?.computer).toEqual({ online: false, lastSeen: "2026-10-14T21:04:55Z" });
    expect(got?.snapshot.inbox[0]?.title).toBe("Fix flaky queue test");
    expect(got?.snapshot.approvals).toHaveLength(1);
    const saved = await savedSnapshot(w.computer.cid);
    expect(saved?.snapshot.at).toBe(got?.snapshot.at ?? "");
    expect(saved?.stale).toBe(true);
  });

  test("falls back to the saved copy when the relay is unreachable", async () => {
    const w = await world();
    await loadSnapshot(w.computer, {
      fetch: snapRelay(200, {
        b64: await sealedSnapshot(w),
        computer: { online: true, last_seen: null },
      }),
      now: () => NOW,
    });
    const offline = (async () => {
      throw new TypeError("network down");
    }) as unknown as typeof fetch;
    const got = await loadSnapshot(w.computer, { fetch: offline });
    expect(got?.stale).toBe(true);
    expect(got?.snapshot.inbox).toHaveLength(1);
  });

  test("nothing to show before the first snapshot", async () => {
    const w = await world();
    expect(
      await loadSnapshot(w.computer, { fetch: snapRelay(404, { error: "not_found" }) }),
    ).toBeNull();
  });

  test("a snapshot not sealed by this computer is never shown", async () => {
    const w = await world();
    const impostor = await generateStaticKey();
    const got = await loadSnapshot(w.computer, {
      fetch: snapRelay(200, {
        b64: await sealedSnapshot(w, impostor),
        computer: { online: true, last_seen: null },
      }),
    });
    expect(got).toBeNull();
    expect(await savedSnapshot(w.computer.cid)).toBeNull();
  });

  test("garbage from the relay is ignored", async () => {
    const w = await world();
    for (const body of [{ b64: "!!!" }, { b64: "AAAA" }, { nope: true }]) {
      expect(await loadSnapshot(w.computer, { fetch: snapRelay(200, body) })).toBeNull();
    }
    expect(await loadSnapshot(w.computer, { fetch: snapRelay(500) })).toBeNull();
    expect(
      await loadSnapshot({ ...w.computer, relayToken: null }, { fetch: snapRelay(200, {}) }),
    ).toBeNull();
  });
});

describe("offlineLabel", () => {
  test("reads the way the inbox shows it", () => {
    const at = (minsAgo: number) => new Date(NOW - minsAgo * 60_000).toISOString();
    expect(offlineLabel(null, NOW)).toBe("offline");
    expect(offlineLabel("nonsense", NOW)).toBe("offline");
    expect(offlineLabel(at(0), NOW)).toBe("offline just now");
    expect(offlineLabel(at(12), NOW)).toBe("offline 12m");
    expect(offlineLabel(at(60 * 3), NOW)).toBe("offline 3h");
    expect(offlineLabel(at(60 * 24 * 3), NOW)).toBe("offline 3d");
    expect(offlineLabel(new Date(NOW + 5000).toISOString(), NOW)).toBe("offline just now"); // clock skew
  });
});

describe("uuidv7", () => {
  test("has the right shape and sorts by time", () => {
    const a = uuidv7(NOW);
    const b = uuidv7(NOW + 1);
    expect(a).toMatch(/^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/);
    expect(a < b).toBe(true);
    expect(a.slice(0, 13).replace("-", "")).toBe(NOW.toString(16).padStart(12, "0"));
    expect(new Set(Array.from({ length: 200 }, () => uuidv7(NOW))).size).toBe(200);
  });
});
