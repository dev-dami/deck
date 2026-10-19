// Minimal IndexedDB helper for the hosted v3 client (plan §8 B4).
// Falls back to memory when IndexedDB is unavailable (workers, private mode,
// bun tests), so logic stays testable without a browser.

export interface DBValue {
  // Marker interface: stores keep plain JSON-able records.
  readonly [key: string]: unknown;
}

export interface StoreBackend {
  getAll(store: string): Promise<Record<string, unknown>[]>;
  put(store: string, key: string, value: Record<string, unknown>): Promise<void>;
  del(store: string, key: string): Promise<void>;
  clear(store: string): Promise<void>;
}

class MemoryBackend implements StoreBackend {
  private tables = new Map<string, Map<string, Record<string, unknown>>>();

  private table(name: string): Map<string, Record<string, unknown>> {
    let t = this.tables.get(name);
    if (t === undefined) {
      t = new Map();
      this.tables.set(name, t);
    }
    return t;
  }

  async getAll(store: string): Promise<Record<string, unknown>[]> {
    return [...this.table(store).values()];
  }

  async put(store: string, key: string, value: Record<string, unknown>): Promise<void> {
    this.table(store).set(key, value);
  }

  async del(store: string, key: string): Promise<void> {
    this.table(store).delete(key);
  }

  async clear(store: string): Promise<void> {
    this.table(store).clear();
  }
}

// Each store and the field that keys its rows.
const STORES: Record<string, string> = {
  computers: "cid",
  outbox: "qid",
  snapshots: "cid",
  submitted: "qid",
};

class IndexedDBBackend implements StoreBackend {
  private db: IDBDatabase | null = null;
  private opening: Promise<IDBDatabase> | null = null;

  private open(): Promise<IDBDatabase> {
    if (this.db !== null) return Promise.resolve(this.db);
    if (this.opening !== null) return this.opening;
    this.opening = new Promise<IDBDatabase>((resolve, reject) => {
      const req = indexedDB.open("deck-hosted-v3", 3);
      req.onupgradeneeded = () => {
        const db = req.result;
        for (const [name, keyPath] of Object.entries(STORES)) {
          if (!db.objectStoreNames.contains(name)) db.createObjectStore(name, { keyPath });
        }
      };
      req.onsuccess = () => {
        this.db = req.result;
        resolve(req.result);
      };
      req.onerror = () => reject(req.error ?? new Error("indexeddb open failed"));
    });
    return this.opening;
  }

  private async run<T>(
    store: string,
    mode: IDBTransactionMode,
    fn: (store: IDBObjectStore) => IDBRequest<T>,
  ): Promise<T> {
    const db = await this.open();
    return new Promise<T>((resolve, reject) => {
      const tx = db.transaction(store, mode);
      const os = tx.objectStore(store);
      let req: IDBRequest<T>;
      try {
        req = fn(os);
      } catch (err) {
        reject(err);
        return;
      }
      req.onsuccess = () => resolve(req.result);
      req.onerror = () => reject(req.error ?? new Error("indexeddb request failed"));
    });
  }

  async getAll(store: string): Promise<Record<string, unknown>[]> {
    if (!(store in STORES)) return [];
    const rows = await this.run(store, "readonly", (s) => s.getAll());
    return (rows as unknown as Record<string, unknown>[]) ?? [];
  }

  async put(store: string, key: string, value: Record<string, unknown>): Promise<void> {
    const keyPath = STORES[store];
    if (keyPath === undefined) return;
    await this.run(store, "readwrite", (s) => s.put({ ...value, [keyPath]: key }));
  }

  async del(store: string, key: string): Promise<void> {
    if (!(store in STORES)) return;
    await this.run(store, "readwrite", (s) => s.delete(key));
  }

  async clear(store: string): Promise<void> {
    if (!(store in STORES)) return;
    await this.run(store, "readwrite", (s) => s.clear());
  }
}

function pickBackend(): StoreBackend {
  try {
    if (typeof indexedDB !== "undefined") return new IndexedDBBackend();
  } catch {
    // Fall through to memory.
  }
  return new MemoryBackend();
}

// One shared backend per page load; tests inject their own via setBackend.
let backend: StoreBackend = pickBackend();

export function setBackend(next: StoreBackend): void {
  backend = next;
}

export const setBackendForTests = setBackend;

export function memoryBackend(): StoreBackend {
  return new MemoryBackend();
}

export async function dbGetAll(store: string): Promise<Record<string, unknown>[]> {
  return backend.getAll(store);
}

export async function dbPut(
  store: string,
  key: string,
  value: Record<string, unknown>,
): Promise<void> {
  await backend.put(store, key, value);
}

export async function dbDelete(store: string, key: string): Promise<void> {
  await backend.del(store, key);
}
