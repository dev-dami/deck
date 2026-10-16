// Noise for the v3 channel (docs/mobile-contract.md §3): IK for sessions,
// IKpsk2 for pairing, K for sealed offline actions. Every primitive is
// WebCrypto (X25519, AES-GCM, SHA-256, HMAC); the handshake transcript must
// replay pkg/remote/wire/testdata/noise_*.json byte for byte, which pins the
// flynn/noise behaviors this mirrors: big-endian AES-GCM nonces, MixKey(e)
// after every e token when a PSK is in play, psk2 mixed at the end of message
// 2, and Split order [initiator→responder, responder→initiator].
//
// Rekey mirrors flynn/noise exactly: the key rolls, the nonce does not reset.

export type NoisePatternName = "IK" | "IKpsk2" | "K";

type Token = "e" | "es" | "ee" | "se" | "ss" | "s" | "psk";

interface Pattern {
  base: "IK" | "K";
  initPre: Token[];
  respPre: Token[];
  messages: Token[][];
}

const PATTERNS: Record<NoisePatternName, Pattern> = {
  IK: {
    base: "IK",
    initPre: [],
    respPre: ["s"],
    messages: [
      ["e", "es", "s", "ss"],
      ["e", "ee", "se"],
    ],
  },
  IKpsk2: {
    base: "IK",
    initPre: [],
    respPre: ["s"],
    messages: [
      ["e", "es", "s", "ss"],
      ["e", "ee", "se", "psk"],
    ],
  },
  K: {
    base: "K",
    initPre: ["s"],
    respPre: ["s"],
    messages: [["e", "es", "ss"]],
  },
};

const MAX_NONCE = (1n << 64n) - 2n;
const REKEY_NONCE = (1n << 64n) - 1n;
const BASEPOINT = (() => {
  const b = new Uint8Array(32);
  b[0] = 9;
  return b;
})();

export function bytesToHex(b: Uint8Array): string {
  return Array.from(b, (x) => x.toString(16).padStart(2, "0")).join("");
}

export async function sha256Hex(data: Uint8Array): Promise<string> {
  return bytesToHex(owned(await crypto.subtle.digest("SHA-256", fresh(data))));
}

export function hexToBytes(h: string): Uint8Array {
  if (h.length % 2 !== 0 || !/^[0-9a-fA-F]*$/.test(h)) throw new Error("bad hex");
  const out = new Uint8Array(h.length / 2);
  for (let i = 0; i < out.length; i++) out[i] = Number.parseInt(h.slice(i * 2, i * 2 + 2), 16);
  return out;
}

function concat(...parts: Uint8Array[]): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(parts.reduce((n, p) => n + p.length, 0));
  let at = 0;
  for (const p of parts) {
    out.set(p, at);
    at += p.length;
  }
  return out;
}

// The DOM BufferSource type wants a concrete ArrayBuffer view; copies at the
// WebCrypto boundary keep every public signature as plain Uint8Array.
function fresh(data: Uint8Array): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(data.length);
  out.set(data);
  return out;
}

function owned(buf: ArrayBuffer): Uint8Array<ArrayBuffer> {
  const out = new Uint8Array(buf.byteLength);
  out.set(new Uint8Array(buf));
  return out;
}

async function sha256(data: Uint8Array): Promise<Uint8Array> {
  return owned(await crypto.subtle.digest("SHA-256", fresh(data)));
}

async function hmac(key: Uint8Array, data: Uint8Array): Promise<Uint8Array> {
  const k = await crypto.subtle.importKey(
    "raw",
    fresh(key),
    { name: "HMAC", hash: "SHA-256" },
    false,
    ["sign"],
  );
  return owned(await crypto.subtle.sign("HMAC", k, fresh(data)));
}

// Noise HKDF(ck, input, n): temp = HMAC(ck, input), then chained outputs.
async function hkdf(ck: Uint8Array, input: Uint8Array, n: 2): Promise<[Uint8Array, Uint8Array]>;
async function hkdf(
  ck: Uint8Array,
  input: Uint8Array,
  n: 3,
): Promise<[Uint8Array, Uint8Array, Uint8Array]>;
async function hkdf(ck: Uint8Array, input: Uint8Array, n: 2 | 3): Promise<Uint8Array[]> {
  const temp = await hmac(ck, input);
  const o1 = await hmac(temp, new Uint8Array([1]));
  const o2 = await hmac(temp, concat(o1, new Uint8Array([2])));
  if (n === 2) return [o1, o2];
  const o3 = await hmac(temp, concat(o2, new Uint8Array([3])));
  return [o1, o2, o3];
}

function nonce12(n: bigint): Uint8Array<ArrayBuffer> {
  const iv = new Uint8Array(12);
  const view = new DataView(iv.buffer);
  view.setBigUint64(4, n, false);
  return iv;
}

async function importAes(key: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey("raw", fresh(key), { name: "AES-GCM" }, false, [
    "encrypt",
    "decrypt",
  ]);
}

async function aesEncrypt(
  key: CryptoKey,
  n: bigint,
  ad: Uint8Array,
  pt: Uint8Array,
): Promise<Uint8Array> {
  const ct = await crypto.subtle.encrypt(
    { name: "AES-GCM", iv: nonce12(n), additionalData: fresh(ad) },
    key,
    fresh(pt),
  );
  return owned(ct);
}

async function aesDecrypt(
  key: CryptoKey,
  n: bigint,
  ad: Uint8Array,
  ct: Uint8Array,
): Promise<Uint8Array> {
  const pt = await crypto.subtle.decrypt(
    { name: "AES-GCM", iv: nonce12(n), additionalData: fresh(ad) },
    key,
    fresh(ct),
  );
  return owned(pt);
}

const b64u = (b: Uint8Array): string =>
  btoa(String.fromCharCode(...b))
    .replaceAll("+", "-")
    .replaceAll("/", "_")
    .replaceAll("=", "");

async function importX25519Priv(privBytes: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey(
    "jwk",
    { kty: "OKP", crv: "X25519", x: b64u(new Uint8Array(32)), d: b64u(privBytes) },
    { name: "X25519" },
    false,
    ["deriveBits"],
  );
}

async function importX25519Pub(pubBytes: Uint8Array): Promise<CryptoKey> {
  return crypto.subtle.importKey("raw", fresh(pubBytes), { name: "X25519" }, true, []);
}

async function dh(priv: CryptoKey, pubBytes: Uint8Array): Promise<Uint8Array> {
  const pub = await importX25519Pub(pubBytes);
  return owned(await crypto.subtle.deriveBits({ name: "X25519", public: pub }, priv, 256));
}

let cachedBasepoint: CryptoKey | null = null;

async function basepoint(): Promise<CryptoKey> {
  if (cachedBasepoint === null) cachedBasepoint = await importX25519Pub(BASEPOINT);
  return cachedBasepoint;
}

export async function x25519PublicFromPrivate(priv: CryptoKey): Promise<Uint8Array> {
  return owned(
    await crypto.subtle.deriveBits({ name: "X25519", public: await basepoint() }, priv, 256),
  );
}

// A static keypair. The private key is non-extractable; the public bytes are
// derived via the basepoint, so imports never trust a caller-supplied public.
export interface StaticKey {
  pub: Uint8Array;
  priv: CryptoKey;
}

export async function importStaticKey(privBytes: Uint8Array): Promise<StaticKey> {
  if (privBytes.length !== 32) throw new Error("static key must be 32 bytes");
  const priv = await importX25519Priv(privBytes);
  return { pub: await x25519PublicFromPrivate(priv), priv };
}

export async function generateStaticKey(): Promise<StaticKey> {
  return importStaticKey(crypto.getRandomValues(new Uint8Array(32)));
}

interface SymmetricSnapshot {
  ck: Uint8Array;
  h: Uint8Array;
  key: CryptoKey | null;
  n: bigint;
  hasK: boolean;
}

export interface TransportCipher {
  encrypt(pt: Uint8Array): Promise<Uint8Array>;
  decrypt(ct: Uint8Array): Promise<Uint8Array>;
  rekey(): Promise<void>;
}

class Transport implements TransportCipher {
  private key: CryptoKey;
  private n = 0n;

  constructor(key: CryptoKey) {
    this.key = key;
  }

  async encrypt(pt: Uint8Array): Promise<Uint8Array> {
    if (this.n > MAX_NONCE) throw new Error("noise: nonce exhausted, re-handshake");
    const ct = await aesEncrypt(this.key, this.n, new Uint8Array(0), pt);
    this.n += 1n;
    return ct;
  }

  async decrypt(ct: Uint8Array): Promise<Uint8Array> {
    if (this.n > MAX_NONCE) throw new Error("noise: nonce exhausted, re-handshake");
    const pt = await aesDecrypt(this.key, this.n, new Uint8Array(0), ct);
    this.n += 1n;
    return pt;
  }

  async rekey(): Promise<void> {
    const zeros = new Uint8Array(32);
    const out = await aesEncrypt(this.key, REKEY_NONCE, new Uint8Array(0), zeros);
    this.key = await importAes(out.slice(0, 32));
  }
}

export interface SplitCiphers {
  // CipherStates from Split: index 0 flows initiator→responder, 1 the reverse.
  send: TransportCipher;
  recv: TransportCipher;
}

// Framing over the transport (contract §3, Go pkg/remote/e2e Session): a
// Noise message carries at most 65535 bytes, so a frame is cut into chunks of
// at most 65518 data bytes, each prefixed with a flag byte (0 more, 1 last)
// and encrypted as its own message. Rekey is count-only: after every 10000th
// message in a direction. Frames over 12 MiB are refused before encrypting.
export const CHUNK_DATA_BYTES = 65518;
export const MAX_FRAME_BYTES = 12 << 20;
export const REKEY_EVERY = 10000;

export class FramedChannel {
  private sent = 0;
  private got = 0;
  private partial: Uint8Array | null = null;

  constructor(
    private send: TransportCipher,
    private recv: TransportCipher,
  ) {}

  async sealFrame(frame: Uint8Array): Promise<Uint8Array[]> {
    if (frame.length > MAX_FRAME_BYTES) throw new Error("noise: frame too large");
    const out: Uint8Array[] = [];
    let at = 0;
    let first = true;
    while (first || at < frame.length) {
      first = false;
      const n = Math.min(frame.length - at, CHUNK_DATA_BYTES);
      const last = at + n >= frame.length;
      const pt = new Uint8Array(1 + n);
      pt[0] = last ? 1 : 0;
      pt.set(frame.subarray(at, at + n), 1);
      out.push(await this.send.encrypt(pt));
      at += n;
      this.sent += 1;
      if (this.sent % REKEY_EVERY === 0) await this.send.rekey();
    }
    return out;
  }

  // Returns the whole frame once the last chunk arrives, null before that.
  // A message that fails to authenticate throws without advancing the nonce.
  async openMessage(msg: Uint8Array): Promise<Uint8Array | null> {
    const pt = await this.recv.decrypt(msg);
    this.got += 1;
    if (this.got % REKEY_EVERY === 0) await this.recv.rekey();
    const flag = pt.length === 0 ? 2 : (pt[0] ?? 2);
    if (flag > 1) throw new Error("noise: message did not authenticate");
    const acc = this.partial ?? new Uint8Array(0);
    if (acc.length + pt.length - 1 > MAX_FRAME_BYTES) {
      this.partial = null;
      throw new Error("noise: frame too large");
    }
    const next = new Uint8Array(acc.length + pt.length - 1);
    next.set(acc, 0);
    next.set(pt.subarray(1), acc.length);
    if (flag === 0) {
      this.partial = next;
      return null;
    }
    this.partial = null;
    return next;
  }
}

export interface HandshakeOpts {
  pattern: NoisePatternName;
  initiator: boolean;
  prologue: Uint8Array;
  staticKey: StaticKey;
  // The peer static the initiator knows upfront (responder key for IK/IKpsk2,
  // initiator key for K). The responder learns it from the S token instead.
  peerStatic?: Uint8Array | undefined;
  psk?: Uint8Array | undefined;
  // Fixed ephemeral private bytes for vector tests; random otherwise.
  ephemeralPriv?: Uint8Array | undefined;
}

export class NoiseHandshake {
  private initiator: boolean;
  private tokens: Token[][];
  private msgIdx = 0;
  private shouldWrite: boolean;

  private ck: Uint8Array;
  private h: Uint8Array;
  private key: CryptoKey | null = null;
  private n = 0n;
  private hasK = false;
  private willPsk: boolean;
  private psk: Uint8Array | null;

  private sPub: Uint8Array;
  private sPriv: CryptoKey;
  private ePub: Uint8Array | null = null;
  private ePriv: CryptoKey | null = null;
  private rs: Uint8Array | null;
  private re: Uint8Array | null = null;
  private ephemeralPriv: Uint8Array | undefined;
  private done = false;

  private constructor(
    tokens: Token[][],
    initiator: boolean,
    ck: Uint8Array,
    h: Uint8Array,
    sPub: Uint8Array,
    sPriv: CryptoKey,
    rs: Uint8Array | null,
    willPsk: boolean,
    psk: Uint8Array | null,
    ephemeralPriv: Uint8Array | undefined,
  ) {
    this.tokens = tokens;
    this.initiator = initiator;
    this.ck = ck;
    this.h = h;
    this.sPub = sPub;
    this.sPriv = sPriv;
    this.rs = rs;
    this.willPsk = willPsk;
    this.psk = psk;
    this.ephemeralPriv = ephemeralPriv;
    this.shouldWrite = initiator;
  }

  static async create(opts: HandshakeOpts): Promise<NoiseHandshake> {
    const pattern = PATTERNS[opts.pattern];
    if (opts.psk !== undefined && opts.psk.length !== 32) {
      throw new Error("noise: preshared key must be 32 bytes");
    }
    const suite = "25519_AESGCM_SHA256";
    const base = pattern.base;
    const modifier = opts.pattern === "IKpsk2" ? "psk2" : "";
    const name = new TextEncoder().encode(`Noise_${base}${modifier}_${suite}`);
    let h: Uint8Array;
    if (name.length <= 32) {
      h = new Uint8Array(32);
      h.set(name);
    } else {
      h = await sha256(name);
    }
    const ck = h.slice();
    const mixHash = async (data: Uint8Array): Promise<void> => {
      h = await sha256(concat(h, data));
    };
    await mixHash(opts.prologue);

    const rs = opts.peerStatic !== undefined ? opts.peerStatic.slice() : null;
    const pre = async (tokens: Token[], ours: boolean): Promise<void> => {
      for (const t of tokens) {
        if (t !== "s") throw new Error("noise: unsupported pre-message token");
        if (ours) {
          await mixHash(opts.staticKey.pub);
        } else {
          if (rs === null) throw new Error("noise: peer static unknown for pre-message");
          await mixHash(rs);
        }
      }
    };
    // Initiator pre-messages first, then responder's, on both sides.
    await pre(pattern.initPre, opts.initiator);
    await pre(pattern.respPre, !opts.initiator);

    return new NoiseHandshake(
      pattern.messages,
      opts.initiator,
      ck,
      h,
      opts.staticKey.pub.slice(),
      opts.staticKey.priv,
      rs,
      opts.pattern === "IKpsk2",
      opts.psk !== undefined ? opts.psk.slice() : null,
      opts.ephemeralPriv,
    );
  }

  private snapshot(): SymmetricSnapshot {
    return { ck: this.ck.slice(), h: this.h.slice(), key: this.key, n: this.n, hasK: this.hasK };
  }

  private restore(s: SymmetricSnapshot): void {
    this.ck = s.ck;
    this.h = s.h;
    this.key = s.key;
    this.n = s.n;
    this.hasK = s.hasK;
  }

  private async mixHash(data: Uint8Array): Promise<void> {
    this.h = await sha256(concat(this.h, data));
  }

  private async mixKey(input: Uint8Array): Promise<void> {
    const [ck, hk] = await hkdf(this.ck, input, 2);
    this.ck = ck;
    this.key = await importAes(hk);
    this.n = 0n;
    this.hasK = true;
  }

  private async mixKeyAndHash(input: Uint8Array): Promise<void> {
    const [ck, temp, hk] = await hkdf(this.ck, input, 3);
    this.ck = ck;
    await this.mixHash(temp);
    this.key = await importAes(hk);
    this.n = 0n;
    this.hasK = true;
  }

  private async encryptAndHash(pt: Uint8Array): Promise<Uint8Array> {
    if (this.key === null) {
      await this.mixHash(pt);
      return pt.slice();
    }
    if (this.n > MAX_NONCE) throw new Error("noise: nonce exhausted, re-handshake");
    const ct = await aesEncrypt(this.key, this.n, this.h, pt);
    this.n += 1n;
    await this.mixHash(ct);
    return ct;
  }

  private async decryptAndHash(ct: Uint8Array): Promise<Uint8Array> {
    if (this.key === null) {
      await this.mixHash(ct);
      return ct.slice();
    }
    if (this.n > MAX_NONCE) throw new Error("noise: nonce exhausted, re-handshake");
    const pt = await aesDecrypt(this.key, this.n, this.h, ct);
    this.n += 1n;
    await this.mixHash(ct);
    return pt;
  }

  private async ensureEphemeral(): Promise<void> {
    if (this.ePriv !== null) return;
    const privBytes = this.ephemeralPriv ?? crypto.getRandomValues(new Uint8Array(32));
    this.ePriv = await importX25519Priv(privBytes);
    this.ePub = await x25519PublicFromPrivate(this.ePriv);
  }

  async writeMessage(payload: Uint8Array): Promise<{ bytes: Uint8Array; split?: SplitCiphers }> {
    if (!this.shouldWrite || this.done) throw new Error("noise: unexpected write");
    if (this.msgIdx >= this.tokens.length) throw new Error("noise: no handshake messages left");
    const tokens = this.tokens[this.msgIdx];
    if (tokens === undefined) throw new Error("noise: no handshake messages left");
    let out = new Uint8Array(0);
    for (const t of tokens) {
      switch (t) {
        case "e": {
          await this.ensureEphemeral();
          const pub = this.ePub;
          if (pub === null) throw new Error("noise: ephemeral missing");
          out = concat(out, pub);
          await this.mixHash(pub);
          if (this.willPsk) await this.mixKey(pub);
          break;
        }
        case "s": {
          out = concat(out, await this.encryptAndHash(this.sPub));
          break;
        }
        case "es": {
          if (this.ePriv === null || this.rs === null) throw new Error("noise: es without keys");
          await this.mixKey(await dh(this.ePriv, this.rs));
          break;
        }
        case "ee": {
          if (this.ePriv === null || this.re === null) throw new Error("noise: ee without keys");
          await this.mixKey(await dh(this.ePriv, this.re));
          break;
        }
        case "se": {
          if (this.re === null) throw new Error("noise: se without peer ephemeral");
          if (this.initiator) {
            await this.mixKey(await dh(this.sPriv, this.re));
          } else {
            if (this.ePriv === null || this.rs === null) throw new Error("noise: se without keys");
            await this.mixKey(await dh(this.ePriv, this.rs));
          }
          break;
        }
        case "ss": {
          if (this.rs === null) throw new Error("noise: ss without peer static");
          await this.mixKey(await dh(this.sPriv, this.rs));
          break;
        }
        case "psk": {
          if (this.psk === null) throw new Error("noise: psk message without psk");
          await this.mixKeyAndHash(this.psk);
          break;
        }
      }
    }
    this.shouldWrite = false;
    this.msgIdx += 1;
    out = concat(out, await this.encryptAndHash(payload));
    if (this.msgIdx >= this.tokens.length) {
      this.done = true;
      return { bytes: out, split: await this.split() };
    }
    return { bytes: out };
  }

  async readMessage(message: Uint8Array): Promise<{
    payload: Uint8Array;
    split?: SplitCiphers;
    peerStatic?: Uint8Array;
  }> {
    if (this.shouldWrite || this.done) throw new Error("noise: unexpected read");
    if (this.msgIdx >= this.tokens.length) throw new Error("noise: no handshake messages left");
    const snap = this.snapshot();
    const rsPrev = this.rs === null ? null : this.rs.slice();
    const tokens = this.tokens[this.msgIdx];
    if (tokens === undefined) throw new Error("noise: no handshake messages left");
    let rest = message;
    let sawStatic = false;
    try {
      for (const t of tokens) {
        switch (t) {
          case "e": {
            if (rest.length < 32) throw new Error("noise: message too short");
            this.re = rest.slice(0, 32);
            rest = rest.slice(32);
            await this.mixHash(this.re);
            if (this.willPsk) await this.mixKey(this.re);
            break;
          }
          case "s": {
            const want = this.hasK ? 48 : 32;
            if (rest.length < want) throw new Error("noise: message too short");
            if (this.rs !== null) throw new Error("noise: peer static already set");
            this.rs = await this.decryptAndHash(rest.slice(0, want));
            sawStatic = true;
            rest = rest.slice(want);
            break;
          }
          case "es": {
            if (this.re === null) throw new Error("noise: es without peer ephemeral");
            if (this.initiator) {
              if (this.ePriv === null || this.rs === null) {
                throw new Error("noise: es without keys");
              }
              await this.mixKey(await dh(this.ePriv, this.rs));
            } else {
              await this.mixKey(await dh(this.sPriv, this.re));
            }
            break;
          }
          case "ee": {
            if (this.ePriv === null || this.re === null) throw new Error("noise: ee without keys");
            await this.mixKey(await dh(this.ePriv, this.re));
            break;
          }
          case "se": {
            if (this.re === null) throw new Error("noise: se without peer ephemeral");
            if (this.initiator) {
              await this.mixKey(await dh(this.sPriv, this.re));
            } else {
              if (this.ePriv === null || this.rs === null) {
                throw new Error("noise: se without keys");
              }
              await this.mixKey(await dh(this.ePriv, this.rs));
            }
            break;
          }
          case "ss": {
            if (this.rs === null) throw new Error("noise: ss without peer static");
            await this.mixKey(await dh(this.sPriv, this.rs));
            break;
          }
          case "psk": {
            // The responder may not know the code yet at message 1; by the
            // time a psk token arrives it must be set, as on the Go side.
            if (this.psk === null) throw new Error("noise: psk message without psk");
            await this.mixKeyAndHash(this.psk);
            break;
          }
        }
      }
      const payload = await this.decryptAndHash(rest);
      this.shouldWrite = true;
      this.msgIdx += 1;
      const out: { payload: Uint8Array; split?: SplitCiphers; peerStatic?: Uint8Array } = {
        payload,
      };
      if (sawStatic && this.rs !== null) out.peerStatic = this.rs.slice();
      if (this.msgIdx >= this.tokens.length) {
        this.done = true;
        out.split = await this.split();
      }
      return out;
    } catch (err) {
      // Roll back the chaining state like the Go side; rs reverts when this
      // read set it, while re (peer ephemeral) stays.
      this.restore(snap);
      this.rs = rsPrev;
      throw err;
    }
  }

  private async split(): Promise<SplitCiphers> {
    const [k1, k2] = await hkdf(this.ck, new Uint8Array(0), 2);
    const c1 = new Transport(await importAes(k1));
    const c2 = new Transport(await importAes(k2));
    // Cipher 0 flows initiator→responder, 1 the reverse.
    if (this.initiator) return { send: c1, recv: c2 };
    return { send: c2, recv: c1 };
  }

  handshakeHash(): Uint8Array {
    if (!this.done) throw new Error("noise: handshake not complete");
    return this.h.slice();
  }
}
