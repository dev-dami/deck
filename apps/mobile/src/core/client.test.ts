import { Database } from "bun:sqlite";
import { afterEach, expect, test } from "bun:test";
import { b64Decode, b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import { generateStaticKey } from "../../../../pkg/remote/web/src/e2e/noise";
import { open as openSealed } from "../../../../pkg/remote/web/src/e2e/seal";
import type { ApprovalV3 } from "../../../../pkg/remote/web/src/protocol3";
import { prologueFor, Session3 } from "../../../../pkg/remote/web/src/session3";
import type { ComputerRecord } from "../../../../pkg/remote/web/src/store/computers";
import { memoryBackend, setBackend } from "../../../../pkg/remote/web/src/store/db";
import { FakeComputer, pipePair } from "../../../../pkg/remote/web/src/transport/testkit";
import { NativeStoreBackend, type SecretVault, type SQLConnection } from "../storage/backend";
import { NativeClient } from "./client";

const originalFetch = globalThis.fetch;
afterEach(() => {
  globalThis.fetch = originalFetch;
  setBackend(memoryBackend());
});

const approval: ApprovalV3 = {
  id: "approval_1",
  session_id: "session_1",
  agent: "claude",
  kind: "command",
  description: "Read source",
  tier: "normal",
  digest: "digest_1",
  created_at: "2026-10-15T00:00:00Z",
};

async function setup() {
  const sql = new Database(":memory:");
  const db: SQLConnection = {
    async execAsync(query) {
      sql.exec(query);
    },
    async runAsync(query, ...params) {
      return sql.query(query).run(...params);
    },
    async getAllAsync<T>(query: string, ...params: string[]): Promise<T[]> {
      return sql.query(query).all(...params) as T[];
    },
  };
  const secure = new Map<string, string>();
  const vault: SecretVault = {
    get: async (key) => secure.get(key) ?? null,
    set: async (key, value) => {
      secure.set(key, value);
    },
    remove: async (key) => {
      secure.delete(key);
    },
  };
  const backend = new NativeStoreBackend(db, vault);
  await backend.initialize();
  const hostKey = await generateStaticKey();
  let allowed: Uint8Array[] = [];
  const hosts: FakeComputer[] = [];
  const calls: Array<{ method: string; params: unknown; pj: string | undefined }> = [];
  const open = async (record: ComputerRecord) => {
    if (!record.phoneKey) throw new Error("missing identity");
    const [phone, host] = pipePair();
    hosts.push(
      new FakeComputer(host, {
        cid: record.cid,
        staticKey: hostKey,
        allowed,
        async handle(method, pj, params) {
          calls.push({ method, params, pj });
          return method === "approvals.list" ? [approval] : [];
        },
      }),
    );
    return Session3.open({
      transport: phone,
      phoneKey: record.phoneKey,
      computerStatic: hostKey.pub,
      cid: record.cid,
    });
  };
  const psk = new Uint8Array(32).fill(17);
  const [pairPhone, pairHost] = pipePair();
  const pairingHost = new FakeComputer(pairHost, {
    cid: "computer_1",
    staticKey: hostKey,
    psk,
    pairWelcome: {
      device_id: "dev_1",
      relay_token: "NATIVE-RELAY-SECRET",
      expires_at: "2027-01-01T00:00:00Z",
      name: "Office",
    },
  });
  const client = new NativeClient(backend, open, async () => pairPhone);
  const blob = {
    v: 1,
    cid: "computer_1",
    relay: "https://relay.example",
    s: b64urlEncode(hostKey.pub),
    psk: b64urlEncode(psk),
    exp: Math.floor(Date.now() / 1000) + 300,
    name: "Office",
  };
  const link = `https://relay.example/#p=${b64urlEncode(new TextEncoder().encode(JSON.stringify(blob)))}`;
  const pair = async () => {
    const record = await client.pair(link, "My phone");
    await new Promise((resolve) => setTimeout(resolve, 10));
    if (pairingHost.phoneStatic) allowed = [pairingHost.phoneStatic];
    return record;
  };
  return {
    client,
    backend,
    db,
    vault,
    secure,
    sql,
    open,
    pair,
    pairingHost,
    hosts,
    calls,
    hostKey,
  };
}

test("native pairing persists a secure identity and reconnects after restart with the same Noise static", async () => {
  const f = await setup();
  const paired = await f.pair();
  expect(f.pairingHost.helloSeen).toEqual({ t: "hello", v: 3 });
  expect(paired.phoneKey?.pub ?? null).toEqual(f.pairingHost.phoneStatic);
  expect(JSON.stringify(f.sql.query("SELECT value FROM deck_rows").all())).not.toContain(
    "NATIVE-RELAY-SECRET",
  );
  const restarted = new NativeClient(new NativeStoreBackend(f.db, f.vault), f.open);
  await restarted.reload();
  await restarted.connect(paired.cid);
  expect(restarted.get(paired.cid)?.status).toBe("online");
  expect(f.hosts.at(-1)?.phoneStatic).toEqual(f.pairingHost.phoneStatic);
  expect(restarted.session(paired.cid)).not.toBeNull();
  restarted.stop();
  expect(restarted.session(paired.cid)).toBeNull();
});

test("native approval boundary allows deny reasons and refuses destructive allows", async () => {
  const f = await setup();
  await f.pair();
  await f.client.connect("computer_1");
  expect(await f.client.approvals("computer_1", "project_1", "session_1")).toEqual([approval]);
  await f.client.answer("computer_1", "project_1", approval, "deny", "  Keep these files.  ");
  expect(f.calls.at(-1)).toEqual({
    method: "approvals.answer",
    pj: "project_1",
    params: {
      id: "approval_1",
      session_id: "session_1",
      decision: "deny",
      reason: "Keep these files.",
    },
  });
  const before = f.calls.length;
  await expect(
    f.client.answer("computer_1", "project_1", { ...approval, tier: "destructive" }, "allow"),
  ).rejects.toThrow("passkey");
  expect(f.calls.length).toBe(before);
  f.client.stop();
});

test("offline prompt remains sealed in SQLite and moves to history only after relay delivery", async () => {
  const f = await setup();
  await f.pair();
  globalThis.fetch = Object.assign(
    async (): Promise<Response> => {
      throw new Error("phone offline");
    },
    { preconnect: originalFetch.preconnect },
  );
  const result = await f.client.queue("computer_1", {
    kind: "chat.send",
    pj: "project_1",
    sessionId: "session_1",
    payload: { text: "PRIVATE OFFLINE PROMPT" },
  });
  expect(result.state).toBe("outbox");
  expect((await f.client.outbox("computer_1")).map((row) => row.qid)).toEqual([result.qid]);
  expect(await f.client.history("computer_1")).toEqual([]);
  const disk = JSON.stringify(f.sql.query("SELECT value FROM deck_rows").all());
  expect(disk).not.toContain("PRIVATE OFFLINE PROMPT");
  expect(disk).not.toContain("NATIVE-RELAY-SECRET");
  globalThis.fetch = Object.assign(async () => new Response("{}", { status: 200 }), {
    preconnect: originalFetch.preconnect,
  });
  const flushed = await f.client.flush("computer_1");
  expect(flushed.sent).toEqual([result.qid]);
  expect(await f.client.outbox("computer_1")).toEqual([]);
  expect((await f.client.history("computer_1")).map((row) => row.qid)).toEqual([result.qid]);
});

test("forget removes the connection identity and pending local actions", async () => {
  const f = await setup();
  await f.pair();
  globalThis.fetch = Object.assign(
    async (): Promise<Response> => {
      throw new Error("offline");
    },
    { preconnect: originalFetch.preconnect },
  );
  await f.client.queue("computer_1", {
    kind: "chat.send",
    pj: "project_1",
    sessionId: "session_1",
    payload: { text: "hello" },
  });
  await f.client.forget("computer_1");
  expect(f.secure.size).toBe(0);
  expect(f.client.views()).toEqual([]);
  expect(await f.client.outbox("computer_1")).toEqual([]);
});

test("cancel removes unsent mail without a cancellation request", async () => {
  const f = await setup();
  await f.pair();
  let requests = 0;
  globalThis.fetch = Object.assign(
    async (): Promise<Response> => {
      requests++;
      throw new Error("offline");
    },
    { preconnect: originalFetch.preconnect },
  );
  const queued = await f.client.queue("computer_1", {
    kind: "chat.send",
    pj: "project_1",
    sessionId: "session_1",
    payload: { text: "hello" },
  });
  const before = requests;
  expect(await f.client.cancel("computer_1", queued.qid)).toEqual({
    state: "removed",
    qid: queued.qid,
  });
  expect(requests).toBe(before);
  expect(await f.client.outbox("computer_1")).toEqual([]);
});

test("cancel waits for an in-flight flush then queues a bound computer-side cancellation", async () => {
  const f = await setup();
  await f.pair();
  globalThis.fetch = Object.assign(
    async (): Promise<Response> => {
      throw new Error("offline");
    },
    { preconnect: originalFetch.preconnect },
  );
  const queued = await f.client.queue("computer_1", {
    kind: "chat.send",
    pj: "project_1",
    sessionId: "session_1",
    payload: { text: "hello" },
  });
  let release: ((response: Response) => void) | undefined;
  let began: (() => void) | undefined;
  const started = new Promise<void>((resolve) => {
    began = resolve;
  });
  const bodies: Array<{ id: string; b64: string }> = [];
  globalThis.fetch = Object.assign(
    async (
      _input: Parameters<typeof fetch>[0],
      init?: Parameters<typeof fetch>[1],
    ): Promise<Response> => {
      bodies.push(JSON.parse(String(init?.body)));
      if (bodies.length === 1) {
        began?.();
        return new Promise<Response>((resolve) => {
          release = resolve;
        });
      }
      return new Response("{}", { status: 200 });
    },
    { preconnect: originalFetch.preconnect },
  );
  const flushing = f.client.flush("computer_1");
  await started;
  const cancelling = f.client.cancel("computer_1", queued.qid);
  await Promise.resolve();
  expect(bodies.length).toBe(1);
  expect(release).toBeDefined();
  release?.(new Response("{}", { status: 200 }));
  expect((await flushing).sent).toEqual([queued.qid]);
  const result = await cancelling;
  expect(result.state).toBe("requested");
  expect(bodies.length).toBe(2);
  const cancellation = bodies[1];
  if (!cancellation) throw new Error("no cancellation mail");
  const message = b64Decode(cancellation.b64);
  const sender = f.client.get("computer_1")?.record.phoneKey;
  if (!message || !sender) throw new Error("bad test pairing");
  const opened = await openSealed({
    recipientStatic: f.hostKey,
    senderStaticPub: sender.pub,
    prologue: prologueFor("computer_1"),
    message,
  });
  const action = JSON.parse(new TextDecoder().decode(opened.plaintext));
  expect(action.kind).toBe("queue.cancel");
  expect(action.payload).toEqual({ qid: queued.qid });
  expect(action.qid).not.toBe(queued.qid);
});
