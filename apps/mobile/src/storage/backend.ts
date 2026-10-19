import { b64urlDecode, b64urlEncode } from "../../../../pkg/remote/web/src/b64";
import { importStaticKey } from "../../../../pkg/remote/web/src/e2e/noise";
import { decodeSnapshot } from "../../../../pkg/remote/web/src/protocol3";
import type { ComputerRecord } from "../../../../pkg/remote/web/src/store/computers";
import type { StoreBackend } from "../../../../pkg/remote/web/src/store/db";

export interface SQLConnection {
  execAsync(sql: string): Promise<void>;
  runAsync(sql: string, ...params: string[]): Promise<unknown>;
  getAllAsync<T>(sql: string, ...params: string[]): Promise<T[]>;
}

export interface SecretVault {
  get(key: string): Promise<string | null>;
  set(key: string, value: string): Promise<void>;
  remove(key: string): Promise<void>;
}

interface Credentials {
  version: 1;
  privateKey: string;
  cacheKey: string;
  relayToken: string;
  deviceId: string;
  computerStatic: string;
  relayUrl: string;
}

const STORES = new Set(["computers", "outbox", "snapshots", "submitted"]);
const SELECT = "SELECT key, value FROM deck_rows WHERE store = ?";
const UPSERT = "INSERT OR REPLACE INTO deck_rows (store, key, value) VALUES (?, ?, ?)";

function storeName(store: string): void {
  if (!STORES.has(store)) throw new Error("Unknown native data store.");
}

function vaultKey(cid: string): string {
  if (!/^[A-Za-z0-9_-]{1,128}$/.test(cid)) throw new Error("Invalid computer identity.");
  return `deck.mobile.identity.${cid}`;
}

function object(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function credentials(text: string): Credentials {
  try {
    const value: unknown = JSON.parse(text);
    if (!object(value) || value["version"] !== 1) throw new Error();
    for (const field of [
      "privateKey",
      "cacheKey",
      "relayToken",
      "deviceId",
      "computerStatic",
      "relayUrl",
    ]) {
      if (typeof value[field] !== "string" || value[field] === "") throw new Error();
    }
    const result = value as unknown as Credentials;
    const privateBytes = b64urlDecode(result.privateKey);
    const cacheBytes = b64urlDecode(result.cacheKey);
    const publicBytes = b64urlDecode(result.computerStatic);
    const valid =
      privateBytes?.length === 32 && cacheBytes?.length === 32 && publicBytes?.length === 32;
    privateBytes?.fill(0);
    cacheBytes?.fill(0);
    if (!valid) throw new Error();
    return result;
  } catch {
    throw new Error("The saved secure identity is invalid. Pair this computer again.");
  }
}

function metadata(record: Record<string, unknown>, key: string): Record<string, unknown> {
  // Never serialize a CryptoKey, raw private key, relay token or unknown field.
  return {
    cid: key,
    name: record["name"],
    relayUrl: record["relayUrl"],
    lan: record["lan"],
    createdAt: record["createdAt"],
    ...(typeof record["lastSeen"] === "string" ? { lastSeen: record["lastSeen"] } : {}),
    ...(typeof record["online"] === "boolean" ? { online: record["online"] } : {}),
  };
}

function select(value: Record<string, unknown>, fields: string[]): Record<string, unknown> {
  return Object.fromEntries(
    fields.filter((field) => value[field] !== undefined).map((field) => [field, value[field]]),
  );
}

function safeData(store: string, value: Record<string, unknown>): Record<string, unknown> {
  if (store === "outbox") {
    return select(value, [
      "qid",
      "cid",
      "id",
      "expires_at",
      "b64",
      "createdAt",
      "attempts",
      "kind",
    ]);
  }
  if (store === "submitted") {
    return select(value, ["qid", "cid", "kind", "created_at", "expires_at"]);
  }
  const snapshot = decodeSnapshot(value["snapshot"]);
  if (snapshot === null) throw new Error("Cannot save an invalid native snapshot.");
  const computer = object(value["computer"]) ? value["computer"] : {};
  return {
    cid: value["cid"],
    snapshot,
    fetchedAt: value["fetchedAt"],
    computer: {
      online: computer["online"] === true,
      lastSeen: typeof computer["lastSeen"] === "string" ? computer["lastSeen"] : null,
    },
  };
}

// Only public metadata and encrypted outbox/snapshot content reach SQLite. Connection
// secrets, the pinned computer key and relay URL are restored from SecureStore.
// The public pin/URL live there too so changing SQLite cannot redirect tokens.
export class NativeStoreBackend implements StoreBackend {
  private writes: Promise<unknown> = Promise.resolve();

  constructor(
    private readonly db: SQLConnection,
    private readonly vault: SecretVault,
  ) {}

  async initialize(): Promise<void> {
    await this.db.execAsync(
      "PRAGMA journal_mode = WAL; CREATE TABLE IF NOT EXISTS deck_rows (store TEXT NOT NULL, key TEXT NOT NULL, value TEXT NOT NULL, PRIMARY KEY (store, key));",
    );
  }

  private write<T>(operation: () => Promise<T>): Promise<T> {
    const result = this.writes.then(operation, operation);
    this.writes = result.then(
      () => undefined,
      () => undefined,
    );
    return result;
  }

  private async readCredentials(cid: string): Promise<Credentials | null> {
    let value: string | null;
    try {
      value = await this.vault.get(vaultKey(cid));
    } catch {
      throw new Error("Could not read the secure identity. Unlock your phone and try again.");
    }
    return value === null ? null : credentials(value);
  }

  async getAll(store: string): Promise<Record<string, unknown>[]> {
    storeName(store);
    await this.writes;
    const rows = await this.db.getAllAsync<{ key: string; value: string }>(SELECT, store);
    const result: Record<string, unknown>[] = [];
    for (const row of rows) {
      let value: unknown;
      try {
        value = JSON.parse(row.value);
      } catch {
        continue;
      }
      if (!object(value)) continue;
      if (store === "snapshots") {
        const opened = await this.openSnapshot(row.key, value);
        if (opened !== null) result.push(opened);
        continue;
      }
      if (store !== "computers") {
        result.push(value);
        continue;
      }
      const safe = metadata(value, row.key);
      const secret = await this.readCredentials(row.key);
      if (secret === null) {
        result.push({ ...safe, deviceId: null, relayToken: null });
        continue;
      }
      const raw = b64urlDecode(secret.privateKey);
      if (raw === null) throw new Error("The secure identity could not be restored.");
      try {
        const phoneKey = await importStaticKey(raw);
        result.push({
          ...safe,
          relayUrl: secret.relayUrl,
          relayToken: secret.relayToken,
          deviceId: secret.deviceId,
          computerStatic: secret.computerStatic,
          phoneKey,
        });
      } finally {
        raw.fill(0);
      }
    }
    return result;
  }

  private async cacheKey(cid: string): Promise<CryptoKey | null> {
    const secret = await this.readCredentials(cid);
    if (secret === null) return null;
    const raw = b64urlDecode(secret.cacheKey);
    if (raw === null) throw new Error("The secure snapshot key is invalid.");
    try {
      return await crypto.subtle.importKey("raw", new Uint8Array(raw), "AES-GCM", false, [
        "encrypt",
        "decrypt",
      ]);
    } finally {
      raw.fill(0);
    }
  }

  private async sealSnapshot(
    cid: string,
    value: Record<string, unknown>,
  ): Promise<Record<string, unknown>> {
    const key = await this.cacheKey(cid);
    if (key === null) throw new Error("Pair this computer before saving its snapshot.");
    const iv = crypto.getRandomValues(new Uint8Array(12));
    const plaintext = new TextEncoder().encode(
      JSON.stringify(safeData("snapshots", { ...value, cid })),
    );
    try {
      const ciphertext = await crypto.subtle.encrypt(
        {
          name: "AES-GCM",
          iv,
          additionalData: new TextEncoder().encode(`deck-mobile-cache-v1\0snapshots\0${cid}`),
        },
        key,
        plaintext,
      );
      return {
        version: 1,
        cid,
        iv: b64urlEncode(iv),
        ciphertext: b64urlEncode(new Uint8Array(ciphertext)),
      };
    } finally {
      plaintext.fill(0);
    }
  }

  private async openSnapshot(
    cid: string,
    value: Record<string, unknown>,
  ): Promise<Record<string, unknown> | null> {
    const key = await this.cacheKey(cid);
    if (key === null) return null;
    let plaintext: Uint8Array | undefined;
    try {
      if (value["version"] !== 1 || value["cid"] !== cid) throw new Error();
      const iv = typeof value["iv"] === "string" ? b64urlDecode(value["iv"]) : null;
      const ciphertext =
        typeof value["ciphertext"] === "string" ? b64urlDecode(value["ciphertext"]) : null;
      if (iv?.length !== 12 || ciphertext === null) throw new Error();
      plaintext = new Uint8Array(
        await crypto.subtle.decrypt(
          {
            name: "AES-GCM",
            iv: new Uint8Array(iv),
            additionalData: new TextEncoder().encode(`deck-mobile-cache-v1\0snapshots\0${cid}`),
          },
          key,
          new Uint8Array(ciphertext),
        ),
      );
      const decoded: unknown = JSON.parse(new TextDecoder().decode(plaintext));
      if (!object(decoded) || decoded["cid"] !== cid) throw new Error();
      return safeData("snapshots", decoded);
    } catch {
      throw new Error("This saved snapshot cannot be authenticated. Reconnect to refresh it.");
    } finally {
      plaintext?.fill(0);
    }
  }

  async savePaired(record: ComputerRecord, rawPrivateKey: Uint8Array): Promise<void> {
    // Copy before the asynchronous write: the caller may wipe its own buffer.
    const copy = new Uint8Array(rawPrivateKey);
    try {
      await this.write(() => this.saveIdentity(record, copy));
    } finally {
      copy.fill(0);
    }
  }

  private async saveIdentity(record: ComputerRecord, raw: Uint8Array): Promise<void> {
    const key = vaultKey(record.cid);
    if (!record.relayToken || !record.deviceId || !record.computerStatic || !record.phoneKey) {
      throw new Error("Cannot save an incomplete pairing.");
    }
    const imported = await importStaticKey(raw);
    if (b64urlEncode(imported.pub) !== b64urlEncode(record.phoneKey.pub)) {
      throw new Error("The private identity does not match this pairing.");
    }
    const previous = await this.vault.get(key);
    const old = previous === null ? null : credentials(previous);
    const cacheRaw = crypto.getRandomValues(new Uint8Array(32));
    const cacheKey = old?.privateKey === b64urlEncode(raw) ? old.cacheKey : b64urlEncode(cacheRaw);
    cacheRaw.fill(0);
    const secret: Credentials = {
      version: 1,
      privateKey: b64urlEncode(raw),
      cacheKey,
      relayToken: record.relayToken,
      deviceId: record.deviceId,
      computerStatic: record.computerStatic,
      relayUrl: record.relayUrl,
    };
    // Validate before committing either half. Keychain commits first; roll it
    // back if metadata cannot commit so an existing pairing remains coherent.
    credentials(JSON.stringify(secret));
    await this.vault.set(key, JSON.stringify(secret));
    try {
      await this.db.runAsync(
        UPSERT,
        "computers",
        record.cid,
        JSON.stringify(metadata({ ...record }, record.cid)),
      );
    } catch {
      if (previous === null) await this.vault.remove(key);
      else await this.vault.set(key, previous);
      throw new Error("Could not save this pairing on the phone.");
    }
  }

  async put(store: string, key: string, value: Record<string, unknown>): Promise<void> {
    storeName(store);
    await this.write(async () => {
      if (store !== "computers") {
        const safe =
          store === "snapshots" ? await this.sealSnapshot(key, value) : safeData(store, value);
        await this.db.runAsync(UPSERT, store, key, JSON.stringify(safe));
        return;
      }
      if (
        value["relayToken"] === null ||
        value["deviceId"] === null ||
        value["phoneKey"] === undefined
      ) {
        await this.vault.remove(vaultKey(key));
        await this.db.runAsync(UPSERT, store, key, JSON.stringify(metadata(value, key)));
        return;
      }
      const existing = await this.readCredentials(key);
      if (existing === null)
        throw new Error("Pair this computer before saving its secure identity.");
      const raw = b64urlDecode(existing.privateKey);
      if (raw === null) throw new Error("The secure identity could not be restored.");
      try {
        await this.saveIdentity({ ...value, cid: key } as unknown as ComputerRecord, raw);
      } finally {
        raw.fill(0);
      }
    });
  }

  async del(store: string, key: string): Promise<void> {
    storeName(store);
    await this.write(async () => {
      if (store === "computers") await this.vault.remove(vaultKey(key));
      await this.db.runAsync("DELETE FROM deck_rows WHERE store = ? AND key = ?", store, key);
    });
  }

  async clear(store: string): Promise<void> {
    storeName(store);
    await this.write(async () => {
      if (store === "computers") {
        const rows = await this.db.getAllAsync<{ key: string; value: string }>(SELECT, store);
        for (const row of rows) await this.vault.remove(vaultKey(row.key));
      }
      await this.db.runAsync("DELETE FROM deck_rows WHERE store = ?", store);
    });
  }
}
