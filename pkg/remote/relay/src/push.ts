// VAPID push fan-out (docs/mobile-contract.md §2): the computer encrypts the
// payload (RFC 8291) and the relay only adds auth and POSTs it. The push
// service sees ciphertext plus routing metadata.
export interface PushTarget {
  endpoint: string;
  payload: Uint8Array;
  ttl: number;
  urgency: "normal" | "high";
}

function b64u(b: Uint8Array): string {
  let s = "";
  for (let i = 0; i < b.length; i += 0x8000) s += String.fromCharCode(...b.subarray(i, i + 0x8000));
  return btoa(s).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

export async function importVapidPrivate(jwkB64url: string): Promise<CryptoKey> {
  const json = atob(jwkB64url.replaceAll("-", "+").replaceAll("_", "/"));
  const jwk = JSON.parse(json) as {
    kty?: unknown;
    crv?: unknown;
    x?: unknown;
    y?: unknown;
    d?: unknown;
  };
  if (
    jwk.kty !== "EC" ||
    jwk.crv !== "P-256" ||
    typeof jwk.x !== "string" ||
    typeof jwk.y !== "string" ||
    typeof jwk.d !== "string"
  ) {
    throw new Error("bad vapid key");
  }
  return crypto.subtle.importKey(
    "jwk",
    { kty: "EC", crv: "P-256", x: jwk.x, y: jwk.y, d: jwk.d },
    { name: "ECDSA", namedCurve: "P-256" },
    false,
    ["sign"],
  );
}

export async function signVapid(
  keys: { subject: string; privateKey: CryptoKey },
  audience: string,
  expiresAtSec: number,
): Promise<string> {
  const header = b64u(new TextEncoder().encode(`{"typ":"JWT","alg":"ES256"}`));
  const body = b64u(
    new TextEncoder().encode(
      `{"aud":${JSON.stringify(audience)},"exp":${expiresAtSec},"sub":${JSON.stringify(keys.subject)}}`,
    ),
  );
  const sig = new Uint8Array(
    await crypto.subtle.sign(
      { name: "ECDSA", hash: "SHA-256" },
      keys.privateKey,
      new TextEncoder().encode(`${header}.${body}`),
    ),
  );
  // JWS requires raw R||S; WebCrypto returns DER for ECDSA. Convert.
  const raw = derToRawSig(sig);
  return `${header}.${body}.${b64u(raw)}`;
}

function derToRawSig(der: Uint8Array): Uint8Array {
  // Some runtimes return IEEE P1363 (raw R||S) instead of DER; accept it.
  if (der.length === 64 && der[0] !== 0x30) return der.slice();
  // Minimal DER Sequence{Integer R, Integer S} parser for P-256 signatures.
  let at = 0;
  const take = (n: number): Uint8Array => {
    const s = der.slice(at, at + n);
    at += n;
    return s;
  };
  if (take(1)[0] !== 0x30) throw new Error("bad vapid signature");
  let len = take(1)[0] ?? 0;
  if (len > 0x80) {
    const n = len - 0x80;
    len = 0;
    for (const b of take(n)) len = len * 256 + (b ?? 0);
  }
  const ints: Uint8Array[] = [];
  for (let i = 0; i < 2; i++) {
    if (take(1)[0] !== 0x02) throw new Error("bad vapid signature");
    const l = take(1)[0] ?? 0;
    let v = take(l);
    while (v.length > 32 && v[0] === 0) v = v.slice(1);
    const padded = new Uint8Array(32);
    padded.set(v, 32 - Math.min(32, v.length));
    ints.push(padded);
  }
  const first = ints[0] ?? new Uint8Array(32);
  const second = ints[1] ?? new Uint8Array(32);
  const out = new Uint8Array(64);
  out.set(first, 0);
  out.set(second, 32);
  return out;
}

// The push services a browser can hand out an endpoint for. The relay POSTs to
// whatever endpoint a phone registers, so it must only be one of these: https,
// the default port, no credentials, and a known service's host.
const PUSH_HOSTS: readonly RegExp[] = [
  /^fcm\.googleapis\.com$/,
  /^updates\.push\.services\.mozilla\.com$/,
  /(^|\.)push\.apple\.com$/,
  /(^|\.)notify\.windows\.com$/,
  /^updates-autopush\.(stage|dev)\.mozaws\.net$/,
];

export function allowedPushEndpoint(u: URL): boolean {
  return (
    u.protocol === "https:" &&
    u.port === "" &&
    u.username === "" &&
    u.password === "" &&
    PUSH_HOSTS.some((re) => re.test(u.hostname))
  );
}

export async function postPush(
  vapid: { subject: string; privateKey: CryptoKey; publicB64url: string },
  target: PushTarget,
  fetchFn: typeof fetch = fetch,
): Promise<{ ok: boolean; status: number }> {
  const aud = new URL(target.endpoint).origin;
  const jwt = await signVapid(vapid, aud, Math.floor(Date.now() / 1000) + 12 * 3600);
  const res = await fetchFn(target.endpoint, {
    method: "POST",
    headers: {
      "content-type": "application/octet-stream",
      "content-encoding": "aes128gcm",
      ttl: String(Math.max(0, Math.min(86400, target.ttl))),
      urgency: target.urgency,
      authorization: `vapid t=${jwt}, k=${vapid.publicB64url}`,
    },
    body: target.payload,
    // A push service that redirects is not one to follow.
    redirect: "manual",
  });
  return { ok: res.ok, status: res.status };
}
