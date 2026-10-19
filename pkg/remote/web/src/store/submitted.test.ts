import { beforeEach, expect, test } from "bun:test";
import { b64urlEncode } from "../b64";
import { generateStaticKey } from "../e2e/noise";
import { fakeComputerRecord } from "../fakes3";
import { flushOutbox, queueAction } from "../offline/queue";
import { dbGetAll, memoryBackend, setBackendForTests } from "./db";
import { outboxAdd, outboxList } from "./outbox";
import { listSubmitted, MAX_SUBMITTED_PER_COMPUTER, recordSubmitted } from "./submitted";

const NOW = Date.UTC(2026, 9, 15);
beforeEach(() => setBackendForTests(memoryBackend()));

async function computer() {
  const key = await generateStaticKey();
  const phoneKey = await generateStaticKey();
  return fakeComputerRecord({
    cid: "pc",
    relayUrl: "https://relay.example",
    relayToken: "secret",
    deviceId: "phone",
    computerStatic: b64urlEncode(key.pub),
    phoneKey,
  });
}

function response(status: number): typeof fetch {
  return (async (_input: RequestInfo | URL) => new Response("", { status })) as typeof fetch;
}

test("successful submission retains only allowlisted metadata, while outbox stays empty", async () => {
  const pc = await computer();
  const queued = await queueAction(
    pc,
    { kind: "chat.send", pj: "pj", sessionId: "session", payload: { text: "private prompt" } },
    { fetch: response(200), now: () => NOW },
  );
  expect(await listSubmitted(pc.cid)).toEqual([
    {
      qid: queued.qid,
      cid: pc.cid,
      kind: "chat.send",
      created_at: new Date(NOW).toISOString(),
      expires_at: queued.expiresAt,
    },
  ]);
  expect(await outboxList()).toEqual([]);
  const rows = await dbGetAll("submitted");
  expect(Object.keys(rows[0] ?? {}).sort()).toEqual([
    "cid",
    "created_at",
    "expires_at",
    "kind",
    "qid",
  ]);
  expect(JSON.stringify(rows)).not.toContain("private prompt");
  expect(JSON.stringify(rows)).not.toContain("secret");
});

test("outbox submissions enter history only when accepted by the relay", async () => {
  const pc = await computer();
  const queued = await queueAction(
    pc,
    { kind: "chat.continue", pj: "pj", sessionId: "session" },
    { fetch: response(503), now: () => NOW },
  );
  expect(await listSubmitted(pc.cid)).toEqual([]);
  expect(await outboxList(pc.cid)).toHaveLength(1);
  await flushOutbox(pc, { fetch: response(503), now: () => NOW + 1000 });
  expect(await listSubmitted(pc.cid)).toEqual([]);
  await flushOutbox(pc, { fetch: response(200), now: () => NOW + 2000 });
  expect((await listSubmitted(pc.cid))[0]).toMatchObject({
    qid: queued.qid,
    kind: "chat.continue",
    created_at: new Date(NOW).toISOString(),
  });
  expect(await outboxList(pc.cid)).toEqual([]);
});

test("legacy encrypted outbox records remain flushable and receive unknown kind history", async () => {
  const pc = await computer();
  await outboxAdd({
    qid: "legacy",
    cid: pc.cid,
    id: "legacy",
    b64: "opaque",
    createdAt: new Date(NOW).toISOString(),
    expires_at: new Date(NOW + 3600000).toISOString(),
    attempts: 0,
  });
  const waiting = await outboxList(pc.cid);
  expect(waiting[0]).not.toHaveProperty("kind");
  await flushOutbox(pc, { fetch: response(200), now: () => NOW + 1000 });
  expect((await listSubmitted(pc.cid))[0]).toMatchObject({ qid: "legacy", kind: "unknown" });
});

test("history is bounded per computer and ignores unexpected fields", async () => {
  const metadata = {
    cid: "pc",
    kind: "chat.send" as const,
    expires_at: new Date(NOW + 3600000).toISOString(),
  };
  await recordSubmitted({
    ...metadata,
    cid: "other",
    qid: "other",
    created_at: new Date(NOW).toISOString(),
  });
  for (let index = 0; index <= MAX_SUBMITTED_PER_COMPUTER; index++) {
    await recordSubmitted({
      ...metadata,
      qid: `q${index}`,
      created_at: new Date(NOW + index).toISOString(),
      ...{ payload: "must not be kept" },
    });
  }
  const history = await listSubmitted("pc");
  expect(history).toHaveLength(MAX_SUBMITTED_PER_COMPUTER);
  expect(history[0]?.qid).toBe(`q${MAX_SUBMITTED_PER_COMPUTER}`);
  expect(history.some((row) => row.qid === "q0")).toBe(false);
  expect(await listSubmitted("other")).toHaveLength(1);
  expect(JSON.stringify(await dbGetAll("submitted"))).not.toContain("must not be kept");
});
