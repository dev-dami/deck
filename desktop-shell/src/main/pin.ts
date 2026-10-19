import { createHash } from "node:crypto";
import net from "node:net";
import tls from "node:tls";

// Where a Deck server listens and how this app trusts it.
export interface Target {
  host: string;
  port: number;
  tls: boolean;
  // "SHA256:<base64>" of the certificate; null over plain loopback HTTP.
  fingerprint: string | null;
}

export class PinError extends Error {
  constructor() {
    super("certificate fingerprint mismatch");
    this.name = "PinError";
  }
}

const B64_RE = /^SHA256:([A-Za-z0-9+/]{43})=?$/;
const HEX_RE = /^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){31}$/;

// Deck spells a fingerprint two ways: "SHA256:<base64>" in the invite and in
// --json output, and colon-separated hex pairs on the `deck remote` console.
// Both become the base64 form; anything else is null.
export function normalizeFingerprint(raw: string): string | null {
  const s = raw.trim();
  const b64 = B64_RE.exec(s);
  if (b64?.[1] !== undefined) {
    return Buffer.from(b64[1], "base64").length === 32 ? `SHA256:${b64[1]}` : null;
  }
  if (!HEX_RE.test(s)) return null;
  return `SHA256:${Buffer.from(s.replaceAll(":", ""), "hex").toString("base64").replace(/=+$/, "")}`;
}

export function fingerprintOf(der: Buffer): string {
  return `SHA256:${createHash("sha256").update(der).digest("base64").replace(/=+$/, "")}`;
}

const LOOPBACK = new Set(["127.0.0.1", "localhost", "::1"]);
const HOST_RE = /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/;

export function isLoopback(host: string): boolean {
  return LOOPBACK.has(host.toLowerCase());
}

// Why t is not a server this app may talk to, or null when it is. Plain
// HTTP is loopback only, as Deck serves it; TLS needs a pin.
export function targetError(t: Target): string | null {
  const ip = net.isIP(t.host) !== 0;
  if (!ip && !HOST_RE.test(t.host)) return "The address is not a valid host name.";
  if (!Number.isInteger(t.port) || t.port < 1 || t.port > 65535) return "The port is not valid.";
  if (!t.tls) {
    if (!isLoopback(t.host)) {
      return "Deck serves plain HTTP on this computer only. Use the invite line for another computer.";
    }
    return null;
  }
  if (t.fingerprint === null || normalizeFingerprint(t.fingerprint) !== t.fingerprint) {
    return "A secure address needs the certificate fingerprint that deck remote prints.";
  }
  return null;
}

// host:port as a URL or a Host header writes it.
export function authority(t: Target): string {
  const host = t.host.includes(":") ? `[${t.host}]` : t.host;
  return `${host}:${t.port}`;
}

// The Origin Deck accepts: the one its own page has (R7, R17).
export function originOf(t: Target): string {
  return `${t.tls ? "https" : "http"}://${authority(t).toLowerCase()}`;
}

// Opens a connection to t. Over TLS it compares the certificate with the pin
// before it resolves, so the caller writes nothing to a server it has not
// verified. Deck's certificate is self-signed, so the pin replaces the CA
// check: rejectUnauthorized is off and the fingerprint decides.
export function connectPinned(t: Target, timeoutMs = 10_000): Promise<net.Socket> {
  return new Promise((resolve, reject) => {
    const sock: net.Socket = t.tls
      ? tls.connect({
          host: t.host,
          port: t.port,
          rejectUnauthorized: false,
          ...(net.isIP(t.host) === 0 ? { servername: t.host } : {}),
        })
      : net.connect({ host: t.host, port: t.port });
    const timer = setTimeout(() => {
      sock.destroy();
      reject(new Error("connection timed out"));
    }, timeoutMs);
    // An error after the connection is handed over goes to its new owner;
    // this listener only keeps an early one from being unhandled.
    sock.once("error", (e) => {
      clearTimeout(timer);
      reject(e);
    });
    sock.once(t.tls ? "secureConnect" : "connect", () => {
      clearTimeout(timer);
      if (sock instanceof tls.TLSSocket) {
        const cert = sock.getPeerCertificate();
        if (cert.raw === undefined || fingerprintOf(cert.raw) !== t.fingerprint) {
          sock.destroy();
          reject(new PinError());
          return;
        }
      }
      resolve(sock);
    });
  });
}
