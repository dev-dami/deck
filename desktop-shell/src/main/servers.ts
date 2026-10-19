import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import type { SavedServer } from "../shared/bridge";

// Encrypts a token for disk. Electron's safeStorage in the app; a fake in
// tests. available() is false when the system has no keychain, and then no
// token is written to disk at all.
export interface Sealer {
  available(): boolean;
  seal(plain: string): string;
  open(sealed: string): string;
}

interface Entry {
  server: SavedServer;
  token: string;
}

interface FileShape {
  v: 1;
  servers: Array<{ server: Omit<SavedServer, "persisted">; sealed: string }>;
}

// The paired servers and their tokens. The token stays in the main process:
// list() and every result the renderer sees carry SavedServer only.
export class ServerStore {
  private entries = new Map<string, Entry>();

  // file is null for a store that lives in memory only.
  constructor(
    private readonly file: string | null,
    private readonly sealer: Sealer,
  ) {
    this.load();
  }

  list(): SavedServer[] {
    return [...this.entries.values()].map((e) => e.server);
  }

  get(id: string): Entry | null {
    return this.entries.get(id) ?? null;
  }

  save(server: Omit<SavedServer, "persisted">, token: string): SavedServer {
    const saved: SavedServer = { ...server, persisted: this.persists() };
    // The newest pairing comes first: the app opens it on the next start.
    const rest = [...this.entries].filter(([id]) => id !== server.id);
    this.entries = new Map([[server.id, { server: saved, token }], ...rest]);
    this.flush();
    return saved;
  }

  remove(id: string): void {
    if (this.entries.delete(id)) this.flush();
  }

  private persists(): boolean {
    return this.file !== null && this.sealer.available();
  }

  private load(): void {
    if (!this.persists() || this.file === null) return;
    let data: FileShape;
    try {
      data = JSON.parse(readFileSync(this.file, "utf8")) as FileShape;
    } catch {
      return;
    }
    if (data.v !== 1 || !Array.isArray(data.servers)) return;
    for (const { server, sealed } of data.servers) {
      try {
        this.entries.set(server.id, {
          server: { ...server, persisted: true },
          token: this.sealer.open(sealed),
        });
      } catch {
        // A token sealed by another user or keychain cannot be opened; that
        // server needs a new pairing.
      }
    }
  }

  private flush(): void {
    if (!this.persists() || this.file === null) return;
    const data: FileShape = {
      v: 1,
      servers: [...this.entries.values()].map(({ server, token }) => {
        const { persisted: _p, ...plain } = server;
        return { server: plain, sealed: this.sealer.seal(token) };
      }),
    };
    mkdirSync(dirname(this.file), { recursive: true, mode: 0o700 });
    const tmp = `${this.file}.tmp`;
    writeFileSync(tmp, `${JSON.stringify(data, null, 2)}\n`, { mode: 0o600 });
    renameSync(tmp, this.file);
  }
}
