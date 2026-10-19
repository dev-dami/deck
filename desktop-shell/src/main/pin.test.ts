import { execFileSync } from "node:child_process";
import { X509Certificate } from "node:crypto";
import { mkdtempSync, readFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import tls from "node:tls";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { pairWith } from "./pair";
import { connectPinned, fingerprintOf, normalizeFingerprint, PinError, targetError } from "./pin";

const B64 = `SHA256:${Buffer.alloc(32, 7).toString("base64").replace(/=+$/, "")}`;
const HEX = Array.from(Buffer.alloc(32, 7), (b) => b.toString(16).padStart(2, "0").toUpperCase()).join(":");

describe("fingerprints and targets", () => {
  test("both of Deck's spellings normalize to SHA256:<base64>", () => {
    expect(normalizeFingerprint(B64)).toBe(B64);
    expect(normalizeFingerprint(HEX)).toBe(B64);
    expect(normalizeFingerprint(HEX.toLowerCase())).toBe(B64);
    expect(normalizeFingerprint("SHA256:short")).toBeNull();
    expect(normalizeFingerprint("AB:CD")).toBeNull();
  });

  test("plain HTTP is loopback only and TLS needs a pin", () => {
    expect(targetError({ host: "127.0.0.1", port: 7878, tls: false, fingerprint: null })).toBeNull();
    expect(targetError({ host: "192.168.1.5", port: 7878, tls: false, fingerprint: null })).not.toBeNull();
    expect(targetError({ host: "192.168.1.5", port: 7878, tls: true, fingerprint: null })).not.toBeNull();
    expect(targetError({ host: "192.168.1.5", port: 7878, tls: true, fingerprint: B64 })).toBeNull();
    expect(targetError({ host: "bad host", port: 7878, tls: true, fingerprint: B64 })).not.toBeNull();
    expect(targetError({ host: "127.0.0.1", port: 70000, tls: false, fingerprint: null })).not.toBeNull();
  });
});

function haveOpenssl(): boolean {
  try {
    execFileSync("openssl", ["version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

describe.skipIf(!haveOpenssl())("connectPinned over TLS", () => {
  let server: tls.Server;
  let port = 0;
  let pin = "";
  const received: Buffer[] = [];

  beforeAll(async () => {
    const dir = mkdtempSync(join(tmpdir(), "deck-pin-"));
    execFileSync(
      "openssl",
      ["req", "-x509", "-newkey", "ec", "-pkeyopt", "ec_paramgen_curve:P-256", "-nodes", "-days", "1",
        "-subj", "/CN=deck-test", "-keyout", join(dir, "k.pem"), "-out", join(dir, "c.pem")],
      { stdio: "ignore" },
    );
    const cert = readFileSync(join(dir, "c.pem"));
    pin = fingerprintOf(new X509Certificate(cert).raw);
    server = tls.createServer({ key: readFileSync(join(dir, "k.pem")), cert }, (s) => {
      s.on("data", (d: Buffer) => received.push(d));
      s.on("error", () => {});
    });
    await new Promise<void>((r) => server.listen(0, "127.0.0.1", r));
    const addr = server.address();
    port = typeof addr === "object" && addr !== null ? addr.port : 0;
  });
  afterAll(() => {
    server.close();
  });

  test("the pinned certificate connects", async () => {
    const s = await connectPinned({ host: "127.0.0.1", port, tls: true, fingerprint: pin });
    expect(s.destroyed).toBe(false);
    s.destroy();
  });

  test("another certificate is refused before a byte of the code is sent", async () => {
    received.length = 0;
    await expect(connectPinned({ host: "127.0.0.1", port, tls: true, fingerprint: B64 })).rejects.toBeInstanceOf(PinError);
    const r = await pairWith({ host: "127.0.0.1", port, tls: true, fingerprint: B64 }, "SECRET-CODE", "test");
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.message).toMatch(/does not match/);
    await new Promise((res) => setTimeout(res, 100));
    expect(Buffer.concat(received).toString()).not.toContain("SECRET-CODE");
  });
});
