// Shared relay/v1 envelope (docs/mobile-contract.md §5): limits, close codes,
// hashing, and validation. The relay sees sizes, timing and hashes only.
export const RELAY_CHALLENGE_CTX = "deck-relay-v1|";

export const MAX_RELAY_FRAME_BYTES = 1 << 20;
export const MAX_MAIL_BYTES = 64 << 10;
export const MAX_SNAPSHOT_BYTES = 256 << 10;
export const MAX_MAILS_PER_CID = 1000;
export const MAX_MAIL_BYTES_PER_CID = 16 << 20;
export const MAX_LANES_PER_DEVICE = 8;
export const MAX_MAIL_TTL_MS = 24 * 3600 * 1000;
export const RECEIPT_MAIL_TTL_MS = 7 * 24 * 3600 * 1000;
export const CLOCK_SKEW_MS = 5 * 60 * 1000;
export const MAX_PAIR_ATTEMPTS = 10;

export const CLOSE_UNKNOWN_CID = 4404;
export const CLOSE_HOST_REPLACED = 4409;
export const CLOSE_TOKEN_INVALID = 4401;
export const CLOSE_REVOKED = 4403;
export const CLOSE_RATE_LIMITED = 4429;
// A phone whose lane died with the host connection: reconnect and try again.
export const CLOSE_HOST_RESTARTED = 1012;

export function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

export function bytesToB64(b: Uint8Array): string {
  let s = "";
  for (let i = 0; i < b.length; i += 0x8000) {
    s += String.fromCharCode(...b.subarray(i, i + 0x8000));
  }
  return btoa(s);
}

// Standard base64 with padding, the encoding of binary payloads in JSON frames.
export function b64ToBytes(s: string): Uint8Array | null {
  try {
    const bin = atob(s.trim());
    return Uint8Array.from(bin, (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
}

// Unpadded base64url, the encoding of keys and signatures (contract §5).
export function b64urlToBytes(s: string): Uint8Array | null {
  if (!/^[A-Za-z0-9_-]*$/.test(s) || s.length % 4 === 1) return null;
  const std = s.replaceAll("-", "+").replaceAll("_", "/") + "=".repeat((4 - (s.length % 4)) % 4);
  return b64ToBytes(std);
}

export function b64urlEncode(b: Uint8Array): string {
  return bytesToB64(b).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

export async function sha256Hex(data: Uint8Array | string): Promise<string> {
  const bytes = typeof data === "string" ? new TextEncoder().encode(data) : data;
  const digest = await crypto.subtle.digest("SHA-256", bytes);
  return Array.from(new Uint8Array(digest), (x) => x.toString(16).padStart(2, "0")).join("");
}

// The relay's only view of a secret: its SHA-256 hex.
export function tokenHash(token: string): Promise<string> {
  return sha256Hex(token);
}

// The computer id: the first 22 characters of the unpadded base64url of
// SHA-256 over the raw 32-byte Ed25519 public key.
export function cidFromPub(rawKey: Uint8Array): Promise<string> {
  return crypto.subtle
    .digest("SHA-256", rawKey)
    .then((d) => b64urlEncode(new Uint8Array(d)).slice(0, 22));
}

export const ED25519_RAW_BYTES = 32;

export function isCid(s: string): boolean {
  return /^[A-Za-z0-9_-]{22}$/.test(s);
}

export function isLaneId(s: string): boolean {
  return typeof s === "string" && s.length >= 1 && s.length <= 64 && /^[A-Za-z0-9_-]+$/.test(s);
}

// Minimal HostFrame validation: the frame type plus the fields that type
// needs. Unknown types are rejected so a buggy host fails loudly.
export type HostFrameType =
  | "challenge"
  | "auth"
  | "ready"
  | "devices"
  | "pair_open"
  | "pair_close"
  | "open"
  | "data"
  | "close"
  | "mail"
  | "ack"
  | "snap"
  | "push";

const HOST_TYPES: ReadonlySet<string> = new Set([
  "challenge",
  "auth",
  "ready",
  "devices",
  "pair_open",
  "pair_close",
  "open",
  "data",
  "close",
  "mail",
  "ack",
  "snap",
  "push",
]);

export function parseHostFrame(
  text: string,
): { t: HostFrameType; p: Record<string, unknown> } | null {
  if (text.length > MAX_RELAY_FRAME_BYTES) return null;
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch {
    return null;
  }
  if (!isRecord(v) || typeof v["t"] !== "string" || !HOST_TYPES.has(v["t"])) return null;
  return { t: v["t"] as HostFrameType, p: v };
}

export function strField(p: Record<string, unknown>, k: string): string | null {
  const v = p[k];
  return typeof v === "string" ? v : null;
}

export function intField(p: Record<string, unknown>, k: string): number | null {
  const v = p[k];
  return typeof v === "number" && Number.isInteger(v) ? v : null;
}
