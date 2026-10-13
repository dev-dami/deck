import { decodePairError, decodePairResponse, type PairResponse } from "./protocol";

// ---- token store (§3.5) -----------------------------------------------------
//
// The token is keyed by the server_id that issued it. The phone cannot know
// the server_id before its first hello, so the id of the install this origin
// last paired with is kept under a pointer key and resolved on load.

const TOKEN_PREFIX = "deck.remote.token.";
const CURRENT_KEY = "deck.remote.server";

// The subset of Storage the token store uses; length/key enumerate it.
export interface KV {
  readonly length: number;
  key(i: number): string | null;
  getItem(k: string): string | null;
  setItem(k: string, v: string): void;
  removeItem(k: string): void;
}

export interface StoredToken {
  serverId: string;
  token: string;
}

// localStorage throws in private modes and when storage is blocked; the app
// then works for this page load but cannot stay paired.
export function loadToken(store: KV | null): StoredToken | null {
  if (store === null) return null;
  try {
    const serverId = store.getItem(CURRENT_KEY);
    if (serverId === null || serverId === "") return null;
    const token = store.getItem(TOKEN_PREFIX + serverId);
    return token === null || token === "" ? null : { serverId, token };
  } catch {
    return null;
  }
}

// Keeps one token per origin: every other install's token is removed, so a
// key that is no longer in use cannot linger in storage.
export function saveToken(store: KV | null, serverId: string, token: string): boolean {
  if (store === null) return false;
  try {
    const stale: string[] = [];
    for (let i = 0; i < store.length; i++) {
      const k = store.key(i);
      if (k?.startsWith(TOKEN_PREFIX) && k !== TOKEN_PREFIX + serverId) stale.push(k);
    }
    for (const k of stale) store.removeItem(k);
    store.setItem(TOKEN_PREFIX + serverId, token);
    store.setItem(CURRENT_KEY, serverId);
    return true;
  } catch {
    return false;
  }
}

// Forgets the token of one install (R1: only on token_invalid or revoked).
export function clearToken(store: KV | null, serverId: string): void {
  if (store === null) return;
  try {
    store.removeItem(TOKEN_PREFIX + serverId);
    if (store.getItem(CURRENT_KEY) === serverId) store.removeItem(CURRENT_KEY);
  } catch {
    // Storage is unavailable, so there is no token to clear either.
  }
}

export function defaultStorage(): KV | null {
  try {
    return globalThis.localStorage ?? null;
  } catch {
    return null;
  }
}

// ---- pairing input: QR fragment and invite line (R5, R9, §3.5) ----------------

// What the pairing screen needs before it may send the code: the code, the
// certificate fingerprint to compare (absent on plain loopback HTTP), and
// where the invite says the server is.
export interface PairOffer {
  code: string;
  fingerprint: string | null;
  host: string | null;
  port: string | null;
  name: string | null;
  // Unix seconds, from an invite's exp.
  expires: number | null;
}

// The QR URL carries `#pair=<code>&fp=<fingerprint>`. A fingerprint that is
// present but malformed makes the whole offer invalid: it must never fall
// through to the no-fingerprint trust gate (R9).
export function pairOfferFromHash(hash: string): PairOffer | null {
  if (!hash.startsWith("#pair=")) return null;
  const q = new URLSearchParams(hash.slice(1));
  const code = (q.get("pair") ?? "").trim();
  if (code === "" || code.length > MAX_CODE_LEN || hasControl(code)) return null;
  const fp = (q.get("fp") ?? "").trim();
  if (fp !== "" && !FINGERPRINT_RE.test(fp)) return null;
  return {
    code,
    fingerprint: fp === "" ? null : fp,
    host: null,
    port: null,
    name: null,
    expires: null,
  };
}

export function isPairHash(hash: string): boolean {
  return hash.startsWith("#pair=");
}

export const INVITE_PREFIX = "deck://pair?";
const MAX_INVITE_LEN = 1024;
const MAX_INVITE_ALTS = 3;
const MAX_CODE_LEN = 64;
const INVITE_HOST_RE = /^[A-Za-z0-9]([A-Za-z0-9.-]{0,251}[A-Za-z0-9])?$/;
const FINGERPRINT_RE = /^SHA256:[A-Za-z0-9+/]{43}$/;

export function looksLikeInvite(s: string): boolean {
  return s.trim().toLowerCase().startsWith("deck://");
}

// Control, format (bidi overrides, zero-width), line/paragraph separator and
// private-use characters have no place in an invite or a code.
function hasControl(s: string): boolean {
  return /[\p{Cc}\p{Cf}\p{Zl}\p{Zp}\p{Co}�]/u.test(s);
}

function validHost(h: string): boolean {
  if (h === "" || h.length > 253) return false;
  if (/^\d{1,3}(\.\d{1,3}){3}$/.test(h)) {
    return h.split(".").every((o) => Number(o) <= 255) && h !== "0.0.0.0";
  }
  // Bracketless IPv6 as peer.Invite writes it.
  if (h.includes(":")) return /^[0-9A-Fa-f:.]{2,45}$/.test(h) && h !== "::";
  return INVITE_HOST_RE.test(h);
}

// Parses a `deck://pair?v=1&host&port&code&fp&name&exp` line the way
// internal/deck/peer.ParseInvite does: version 1 only, every field checked,
// no unknown or repeated fields. The code is passed to the server as written;
// the server decides whether it is valid.
export function parseInvite(s: string): PairOffer | null {
  const text = s.trim();
  if (text.length > MAX_INVITE_LEN || hasControl(text)) return null;
  if (!text.toLowerCase().startsWith(INVITE_PREFIX)) return null;
  let q: URLSearchParams;
  try {
    q = new URLSearchParams(text.slice(INVITE_PREFIX.length));
  } catch {
    return null;
  }
  const fields = new Map<string, string[]>();
  for (const [k, v] of q) fields.set(k, [...(fields.get(k) ?? []), v]);
  const one = (k: string): string | null => {
    const vs = fields.get(k);
    return vs === undefined ? null : vs.length === 1 ? (vs[0] ?? null) : null;
  };
  for (const [k, vs] of fields) {
    if (k !== "alt" && vs.length !== 1) return null;
    if (!["v", "host", "port", "code", "fp", "name", "exp", "alt"].includes(k)) return null;
  }
  if (one("v") !== "1") return null;
  const host = one("host") ?? "";
  const port = one("port") ?? "";
  const code = (one("code") ?? "").trim();
  const fp = one("fp") ?? "";
  const name = one("name");
  const exp = one("exp");
  if (!validHost(host)) return null;
  const alts = fields.get("alt") ?? [];
  if (alts.length > MAX_INVITE_ALTS || !alts.every(validHost)) return null;
  if (!/^[1-9]\d{0,4}$/.test(port) || Number(port) > 65535) return null;
  if (code === "") return null;
  if (!FINGERPRINT_RE.test(fp)) return null;
  let expires: number | null = null;
  if (exp !== null) {
    if (!/^[1-9]\d*$/.test(exp)) return null;
    expires = Number(exp);
  }
  return { code, fingerprint: fp, host, port, name: name === "" ? null : name, expires };
}

// A pasted line is an invite or a bare code.
export function offerFromInput(s: string): PairOffer | null {
  if (looksLikeInvite(s)) return parseInvite(s);
  const code = s.trim();
  if (code === "" || hasControl(code) || code.length > MAX_CODE_LEN) return null;
  return { code, fingerprint: null, host: null, port: null, name: null, expires: null };
}

// The page is loaded from the server (R7: same origin only), so an invite
// naming another host or port cannot be used here; the right page is this
// one's URL on that address, with the offer carried in the fragment.
export function inviteOrigin(
  offer: PairOffer,
  loc: { protocol: string; hostname: string; port: string },
): { matches: boolean; url: string | null } {
  if (offer.host === null || offer.port === null) return { matches: true, url: null };
  const defaultPort = loc.protocol === "https:" ? "443" : "80";
  // location.hostname brackets an IPv6 address; the invite does not.
  const hostname = loc.hostname.toLowerCase().replace(/^\[|\]$/g, "");
  const here = `${hostname}:${loc.port === "" ? defaultPort : loc.port}`;
  const host = offer.host.includes(":") ? `[${offer.host}]` : offer.host;
  const there = `${offer.host.toLowerCase()}:${offer.port}`;
  if (here === there) return { matches: true, url: null };
  const fp = offer.fingerprint === null ? "" : `&fp=${encodeURIComponent(offer.fingerprint)}`;
  return {
    matches: false,
    url: `https://${host}:${offer.port}/#pair=${encodeURIComponent(offer.code)}${fp}`,
  };
}

// The fingerprint as a browser's certificate viewer shows it: upper-case hex
// pairs separated by colons. Null when the value is not SHA256:<base64>.
export function fingerprintHex(fp: string): string | null {
  const m = /^SHA256:([A-Za-z0-9+/]{43}=?)$/.exec(fp);
  if (m?.[1] === undefined) return null;
  let bytes: Uint8Array;
  try {
    const bin = atob(m[1].endsWith("=") ? m[1] : `${m[1]}=`);
    bytes = Uint8Array.from(bin, (c) => c.charCodeAt(0));
  } catch {
    return null;
  }
  if (bytes.length !== 32) return null;
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0").toUpperCase()).join(":");
}

export function wsUrl(loc: { protocol: string; host: string }): string {
  const scheme = loc.protocol === "https:" ? "wss:" : "ws:";
  return `${scheme}//${loc.host}/api/v1/ws`;
}

export function deviceName(ua: string): string {
  if (/iPhone/i.test(ua)) return "iPhone";
  if (/iPad/i.test(ua)) return "iPad";
  if (/Android/i.test(ua)) {
    const model = /Android[^;)]*;\s*([^;)]+?)(?:\sBuild|\)|;|$)/i.exec(ua)?.[1]?.trim();
    // Chrome's reduced UA reports the model as a bare "K".
    return model !== undefined && model !== "" && model !== "K" ? model : "Android phone";
  }
  if (/Windows/i.test(ua)) return "Windows browser";
  if (/Macintosh|Mac OS X/i.test(ua)) return "Mac browser";
  if (/Linux/i.test(ua)) return "Linux browser";
  return "Phone";
}

// ---- POST /api/v1/pair (§3.5, R7) ---------------------------------------------

export type PairResult =
  | { ok: true; response: PairResponse }
  // retryAfterMs: how long the server asked us to wait (429 Retry-After).
  | { ok: false; message: string; retryable: boolean; retryAfterMs: number | null };

// Retry-After as delay-seconds or an HTTP date, in milliseconds from now.
export function parseRetryAfter(value: string | null, now: number): number | null {
  if (value === null || value.trim() === "") return null;
  const s = value.trim();
  if (/^\d+$/.test(s)) return Number(s) * 1000;
  const at = Date.parse(s);
  return Number.isNaN(at) ? null : Math.max(0, at - now);
}

export type FetchFn = (input: string, init: RequestInit) => Promise<Response>;

const PAIR_ERRORS: Record<string, string> = {
  rate_limited: "Too many attempts. Wait a minute and try again.",
  invalid: "That code is invalid or has expired. Show a new code in Deck.",
  not_found: "That code is invalid or has expired. Show a new code in Deck.",
  unauthorized: "That code is invalid or has expired. Show a new code in Deck.",
  forbidden: "Deck refused this page's origin. Open the address Deck shows.",
  too_large: "The pairing request was too large.",
  internal: "Deck hit an error while pairing. Try again.",
};

export async function pair(fetchFn: FetchFn, code: string, name: string): Promise<PairResult> {
  let res: Response;
  try {
    // Same origin only, no credentials: the token is never a cookie (R7, §3.5).
    res = await fetchFn("/api/v1/pair", {
      method: "POST",
      mode: "same-origin",
      credentials: "omit",
      cache: "no-store",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ code, device_name: name }),
    });
  } catch {
    return {
      ok: false,
      message: "Could not reach Deck. Check that you are on the same network.",
      retryable: true,
      retryAfterMs: null,
    };
  }
  let body: unknown = null;
  try {
    body = await res.json();
  } catch {
    body = null;
  }
  if (!res.ok) {
    const err = decodePairError(body);
    const code = err?.error ?? "";
    const retryable = res.status === 429 || code === "rate_limited" || res.status >= 500;
    const retryAfterMs =
      res.status === 429 ? parseRetryAfter(res.headers.get("retry-after"), Date.now()) : null;
    const byStatus: Record<number, string> = {
      429: PAIR_ERRORS["rate_limited"] ?? "",
      400: PAIR_ERRORS["invalid"] ?? "",
      // R7: the server wants application/json; a 415 means this page or a
      // proxy in between sent something else, not that the code was wrong.
      415: "Deck refused the request's content type. Reload this page and try again.",
    };
    const message =
      PAIR_ERRORS[code] ?? byStatus[res.status] ?? `Pairing failed (HTTP ${res.status}).`;
    return { ok: false, message, retryable, retryAfterMs };
  }
  const response = decodePairResponse(body);
  if (response === null || response.token === "") {
    return {
      ok: false,
      message: "Deck sent an unexpected pairing response.",
      retryable: false,
      retryAfterMs: null,
    };
  }
  return { ok: true, response };
}
