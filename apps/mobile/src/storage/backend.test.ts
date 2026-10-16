import { Database } from "bun:sqlite";
import { expect, test } from "bun:test";
import { b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import { importStaticKey } from "../../../../pkg/remote/web/src/e2e/noise";
import type { ComputerRecord } from "../../../../pkg/remote/web/src/store/computers";
import { NativeStoreBackend, type SecretVault, type SQLConnection } from "./backend";

class Vault implements SecretVault {
  readonly data = new Map<string, string>();
  locked = false;
  async get(key: string): Promise<string | null> {
    if (this.locked) throw new Error("KEYCHAIN-SECRET-ERROR");
    return this.data.get(key) ?? null;
  }
  async set(key: string, value: string): Promise<void> {
    this.data.set(key, value);
  }
  async remove(key: string): Promise<void> {
    this.data.delete(key);
  }
}

async function setup() {
  const sql = new Database(":memory:");
  let failWrite = false;
  const db: SQLConnection = {
    async execAsync(query) {
      sql.exec(query);
    },
    async runAsync(query, ...params) {
      if (failWrite) {
        failWrite = false;
        throw new Error("DISK WRITE FAILED");
      }
      return sql.query(query).run(...params);
    },
    async getAllAsync<T>(query: string, ...params: string[]): Promise<T[]> {
      return sql.query(query).all(...params) as T[];
    },
  };
  const vault = new Vault();
  const backend = new NativeStoreBackend(db, vault);
  await backend.initialize();
  const raw = new Uint8Array(32).fill(7);
  const record = {
    cid: "computer_1",
    name: "Office",
    relayUrl: "https://relay.example",
    lan: [],
    deviceId: "device_1",
    relayToken: "VERY-SECRET-RELAY-TOKEN",
    computerStatic: b64urlEncode(new Uint8Array(32).fill(9)),
    phoneKey: await importStaticKey(raw),
    createdAt: "2026-10-15T00:00:00Z",
  } satisfies ComputerRecord;
  return {
    sql,
    db,
    vault,
    backend,
    raw,
    record,
    failNextWrite() {
      failWrite = true;
    },
  };
}

test("SQLite never receives the raw private key, CryptoKey or relay token", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  const rows = f.sql.query("SELECT value FROM deck_rows").all();
  const disk = JSON.stringify(rows);
  expect(disk).not.toContain(f.record.relayToken);
  expect(disk).not.toContain(b64urlEncode(f.raw));
  expect(disk).not.toContain("phoneKey");
  expect(disk).not.toContain("privateKey");
  const vaultValue = [...f.vault.data.values()][0];
  expect(vaultValue).toContain(f.record.relayToken);
  expect(vaultValue).toContain(b64urlEncode(f.raw));
});

test("restart reconstructs the same non-extractable private identity", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  f.raw.fill(0);
  const restarted = new NativeStoreBackend(f.db, f.vault);
  const [restored] = await restarted.getAll("computers");
  expect(restored?.["relayToken"]).toBe(f.record.relayToken);
  expect(restored?.["deviceId"]).toBe("device_1");
  const key = restored?.["phoneKey"] as ComputerRecord["phoneKey"];
  expect(key?.pub).toEqual(f.record.phoneKey?.pub);
  expect(key?.priv.extractable).toBe(false);
  expect(key?.priv).not.toBe(f.record.phoneKey?.priv);
});

test("SQLite tampering cannot change the pinned peer or token destination", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  const value = JSON.stringify({
    ...f.record,
    phoneKey: undefined,
    relayToken: "attacker-token",
    relayUrl: "https://attacker.example",
    computerStatic: "attacker-key",
  });
  f.sql.query("UPDATE deck_rows SET value=? WHERE store='computers'").run(value);
  const [restored] = await f.backend.getAll("computers");
  expect(restored?.["relayUrl"]).toBe("https://relay.example");
  expect(restored?.["computerStatic"]).toBe(f.record.computerStatic);
  expect(restored?.["relayToken"]).toBe(f.record.relayToken);
});

test("revocation removes SecureStore credentials and restores an unpaired row", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  const { phoneKey: _removed, ...rest } = f.record;
  await f.backend.put("computers", f.record.cid, { ...rest, relayToken: null, deviceId: null });
  expect(f.vault.data.size).toBe(0);
  const [restored] = await f.backend.getAll("computers");
  expect(restored?.["relayToken"]).toBeNull();
  expect(restored?.["phoneKey"]).toBeUndefined();
});

test("failed metadata write rolls the vault back to the prior pairing", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  const before = [...f.vault.data.values()][0];
  f.failNextWrite();
  await expect(
    f.backend.savePaired({ ...f.record, relayToken: "NEW-SECRET-TOKEN" }, f.raw),
  ).rejects.toThrow("Could not save");
  expect([...f.vault.data.values()][0]).toBe(before);
  expect((await f.backend.getAll("computers"))[0]?.["relayToken"]).toBe(f.record.relayToken);
});

test("locked vault fails closed and does not expose provider details", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  f.vault.locked = true;
  await expect(f.backend.getAll("computers")).rejects.toThrow("Unlock your phone");
  try {
    await f.backend.getAll("computers");
  } catch (error) {
    expect(String(error)).not.toContain("KEYCHAIN-SECRET-ERROR");
  }
});

test("mismatched raw private identity never commits", async () => {
  const f = await setup();
  await expect(f.backend.savePaired(f.record, new Uint8Array(32).fill(10))).rejects.toThrow(
    "does not match",
  );
  expect(f.vault.data.size).toBe(0);
  expect(await f.backend.getAll("computers")).toEqual([]);
});

test("sealed outbox and submission writes drop unexpected credential fields", async () => {
  const f = await setup();
  for (const store of ["outbox", "submitted"]) {
    await f.backend.put(store, "qid_1", {
      qid: "qid_1",
      cid: "computer_1",
      id: "qid_1",
      kind: "chat.send",
      expires_at: "2026-10-16T00:00:00Z",
      b64: "sealed-ciphertext",
      createdAt: "2026-10-15T00:00:00Z",
      created_at: "2026-10-15T00:00:00Z",
      attempts: 0,
      relayToken: "SECRET-TOKEN",
      privateKey: "SECRET-PRIVATE",
      phoneKey: f.record.phoneKey,
    });
  }
  const disk = JSON.stringify(f.sql.query("SELECT value FROM deck_rows").all());
  expect(disk).not.toContain("SECRET-TOKEN");
  expect(disk).not.toContain("SECRET-PRIVATE");
  expect(disk).not.toContain("phoneKey");
});

test("deleting a computer also deletes its secure credentials", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  await f.backend.del("computers", f.record.cid);
  expect(f.vault.data.size).toBe(0);
  expect(await f.backend.getAll("computers")).toEqual([]);
});

function snapshotRow(cid: string) {
  return {
    cid,
    snapshot: {
      at: "2026-10-15T00:00:00Z",
      inbox: [],
      approvals: [
        {
          pj: "project_1",
          approval: {
            id: "approval_1",
            session_id: "session_1",
            agent: "claude",
            kind: "command",
            description: "PRIVATE APPROVAL CONTENT",
            command: "echo PRIVATE COMMAND CONTENT",
            created_at: "2026-10-15T00:00:00Z",
            tier: "normal",
            digest: "digest_1",
          },
        },
      ],
      receipts: [],
    },
    fetchedAt: "2026-10-15T00:00:00Z",
    computer: { online: false, lastSeen: "2026-10-14T23:59:00Z" },
  };
}

test("snapshots are encrypted at rest and restored through the shared cache shape", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  const row = snapshotRow(f.record.cid);
  await f.backend.put("snapshots", f.record.cid, row);
  const disk = JSON.stringify(
    f.sql.query("SELECT value FROM deck_rows WHERE store='snapshots'").all(),
  );
  expect(disk).not.toContain("PRIVATE APPROVAL CONTENT");
  expect(disk).not.toContain("PRIVATE COMMAND CONTENT");
  expect(disk).not.toContain("session_1");
  const reopened = new NativeStoreBackend(f.db, f.vault);
  expect(await reopened.getAll("snapshots")).toEqual([row]);
  // Updating public metadata must not destroy access to the encrypted cache.
  await reopened.put("computers", f.record.cid, { ...f.record, name: "Renamed" });
  expect(await reopened.getAll("snapshots")).toEqual([row]);
});

test("tampered snapshot ciphertext fails closed", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  await f.backend.put("snapshots", f.record.cid, snapshotRow(f.record.cid));
  const row = f.sql.query("SELECT value FROM deck_rows WHERE store='snapshots'").get() as {
    value: string;
  };
  const envelope = JSON.parse(row.value) as { ciphertext: string };
  envelope.ciphertext =
    (envelope.ciphertext.startsWith("A") ? "B" : "A") + envelope.ciphertext.slice(1);
  f.sql.query("UPDATE deck_rows SET value=? WHERE store='snapshots'").run(JSON.stringify(envelope));
  await expect(f.backend.getAll("snapshots")).rejects.toThrow("cannot be authenticated");
});

test("copying encrypted cache to another computer does not authenticate", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  await f.backend.put("snapshots", f.record.cid, snapshotRow(f.record.cid));
  const raw2 = new Uint8Array(32).fill(12);
  const other = { ...f.record, cid: "computer_2", phoneKey: await importStaticKey(raw2) };
  await f.backend.savePaired(other, raw2);
  const row = f.sql.query("SELECT value FROM deck_rows WHERE store='snapshots'").get() as {
    value: string;
  };
  const envelope = { ...JSON.parse(row.value), cid: "computer_2" };
  f.sql
    .query("UPDATE deck_rows SET key=?, value=? WHERE store='snapshots'")
    .run("computer_2", JSON.stringify(envelope));
  await expect(f.backend.getAll("snapshots")).rejects.toThrow("cannot be authenticated");
});

test("deleting secure identity makes cached content inaccessible", async () => {
  const f = await setup();
  await f.backend.savePaired(f.record, f.raw);
  await f.backend.put("snapshots", f.record.cid, snapshotRow(f.record.cid));
  await f.backend.del("computers", f.record.cid);
  expect(await f.backend.getAll("snapshots")).toEqual([]);
});
