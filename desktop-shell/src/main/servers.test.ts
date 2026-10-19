import { mkdtempSync, readFileSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { type Sealer, ServerStore } from "./servers";

// Reverses the string and tags it: enough to show what reaches the disk.
const fakeKeychain: Sealer = {
  available: () => true,
  seal: (p) => `sealed:${[...p].reverse().join("")}`,
  open: (s) => [...s.replace(/^sealed:/, "")].reverse().join(""),
};
const noKeychain: Sealer = { ...fakeKeychain, available: () => false };

const server = {
  id: "srv_1",
  host: "127.0.0.1",
  port: 7878,
  tls: false,
  fingerprint: null,
  deviceId: "dev_1",
  expiresAt: "2027-01-01T00:00:00Z",
};

describe("ServerStore", () => {
  test("seals the token on disk and reads it back", () => {
    const file = join(mkdtempSync(join(tmpdir(), "deck-store-")), "servers.json");
    const a = new ServerStore(file, fakeKeychain);
    expect(a.save(server, "tok-123").persisted).toBe(true);
    const raw = readFileSync(file, "utf8");
    expect(raw).not.toContain("tok-123");
    if (process.platform !== "win32") expect(statSync(file).mode & 0o077).toBe(0);
    const b = new ServerStore(file, fakeKeychain);
    expect(b.get("srv_1")?.token).toBe("tok-123");
    expect(b.list()).toEqual([{ ...server, persisted: true }]);
    b.remove("srv_1");
    expect(new ServerStore(file, fakeKeychain).list()).toEqual([]);
  });

  test("without a keychain the token lives in memory only", () => {
    const file = join(mkdtempSync(join(tmpdir(), "deck-store-")), "servers.json");
    const a = new ServerStore(file, noKeychain);
    expect(a.save(server, "tok-123").persisted).toBe(false);
    expect(a.get("srv_1")?.token).toBe("tok-123");
    expect(() => statSync(file)).toThrow();
  });

  test("the newest pairing comes first", () => {
    const s = new ServerStore(null, noKeychain);
    s.save(server, "a");
    s.save({ ...server, id: "srv_2" }, "b");
    s.save(server, "c");
    expect(s.list().map((x) => x.id)).toEqual(["srv_1", "srv_2"]);
    expect(s.get("srv_1")?.token).toBe("c");
  });
});
