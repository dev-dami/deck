// Pairing from the QR code (docs/mobile-contract.md §4): read the blob from
// the URL fragment, run Noise IKpsk2 against the computer through the relay's
// pair lane, confirm with a hello, and remember the computer.
import { b64urlDecode, b64urlEncode } from "../b64";
import { FramedChannel, generateStaticKey, NoiseHandshake, type StaticKey } from "../e2e/noise";
import {
  decodePairWelcome,
  decodeQRBlob,
  type PairWelcome,
  PROTOCOL_V3,
  type QRBlob,
} from "../protocol3";
import { prologueFor } from "../session3";
import { addComputer, type ComputerRecord } from "../store/computers";
import { openRelayPair } from "../transport/relay";
import type { Transport } from "../transport/transport";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export type PairFailure =
  | "bad_code" // the QR could not be read
  | "expired" // older than its five minutes
  | "unsupported" // a QR from a newer Deck
  | "refused" // wrong or spent code
  | "unreachable" // the relay or computer could not be reached
  | "failed";

export class PairError extends Error {
  constructor(
    readonly kind: PairFailure,
    message: string,
  ) {
    super(message);
    this.name = "PairError";
  }
}

// Reads "#p=<base64url JSON>" and checks it can still be used. Returns the
// blob or throws a PairError saying why not.
export function parsePairFragment(hash: string, nowMs: number = Date.now()): QRBlob {
  const m = /^#?p=([A-Za-z0-9_-]+)$/.exec(hash);
  if (m === null || m[1] === undefined) throw new PairError("bad_code", "That is not a Deck code.");
  const raw = b64urlDecode(m[1]);
  if (raw === null) throw new PairError("bad_code", "That is not a Deck code.");
  let json: unknown;
  try {
    json = JSON.parse(decoder.decode(raw));
  } catch {
    throw new PairError("bad_code", "That is not a Deck code.");
  }
  const blob = decodeQRBlob(json);
  if (blob === null) throw new PairError("bad_code", "That is not a Deck code.");
  if (blob.v !== 1) {
    throw new PairError(
      "unsupported",
      "This code is from a newer Deck. Update the app and try again.",
    );
  }
  if (blob.exp * 1000 <= nowMs) {
    throw new PairError("expired", "This code has expired. Run 'deck mobile pair' again.");
  }
  const psk = b64urlDecode(blob.psk);
  const s = b64urlDecode(blob.s);
  if (psk === null || psk.length !== 32 || s === null || s.length !== 32) {
    throw new PairError("bad_code", "That is not a Deck code.");
  }
  return blob;
}

// The code is secret, so it must not stay in the address bar or history.
export function stripPairFragment(
  loc: { pathname: string; search: string } = window.location,
  history: { replaceState(data: unknown, unused: string, url: string): void } = window.history,
): void {
  history.replaceState(null, "", loc.pathname + loc.search);
}

// HKDF-SHA256(ikm = psk, salt = none, info = "deck-pair-id", 32 bytes),
// unpadded base64url. The relay is given the SHA-256 of it, never the secret.
export async function pairIdFromPsk(psk: Uint8Array): Promise<string> {
  const key = await crypto.subtle.importKey("raw", psk as BufferSource, "HKDF", false, [
    "deriveBits",
  ]);
  const bits = await crypto.subtle.deriveBits(
    {
      name: "HKDF",
      hash: "SHA-256",
      salt: new Uint8Array(0),
      info: encoder.encode("deck-pair-id"),
    },
    key,
    256,
  );
  return b64urlEncode(new Uint8Array(bits));
}

export interface PairOptions {
  blob: QRBlob;
  deviceName: string;
  // Native clients persist the raw identity in their OS-backed vault before
  // supplying its non-extractable key handle to this shared handshake.
  phoneKey?: StaticKey;
  openPair?: (relayUrl: string, cid: string, pairId: string) => Promise<Transport>;
  timeoutMs?: number;
  now?: () => Date;
  save?: (c: ComputerRecord) => Promise<void>;
}

// Runs the pairing and stores the computer. Resolves with what was stored.
export async function pairWithComputer(opts: PairOptions): Promise<ComputerRecord> {
  const { blob } = opts;
  const timeoutMs = opts.timeoutMs ?? 15_000;
  const psk = b64urlDecode(blob.psk);
  const computerStatic = b64urlDecode(blob.s);
  if (psk === null || computerStatic === null)
    throw new PairError("bad_code", "That is not a Deck code.");
  const open = opts.openPair ?? openRelayPair;

  let t: Transport;
  try {
    t = await open(blob.relay, blob.cid, await pairIdFromPsk(psk));
  } catch (err) {
    throw new PairError(
      "unreachable",
      err instanceof Error ? err.message : "Could not reach the relay.",
    );
  }

  const inbox: Uint8Array[] = [];
  let waiter: ((m: Uint8Array | { code: number; reason: string }) => void) | null = null;
  let closed: { code: number; reason: string } | null = null;
  t.onMessage((m) => {
    if (waiter !== null) {
      const w = waiter;
      waiter = null;
      w(m);
    } else inbox.push(m);
  });
  t.onClose((code, reason) => {
    closed = { code, reason };
    if (waiter !== null) {
      const w = waiter;
      waiter = null;
      w({ code, reason });
    }
  });
  const next = (): Promise<Uint8Array> =>
    new Promise<Uint8Array>((resolve, reject) => {
      const held = inbox.shift();
      if (held !== undefined) return resolve(held);
      const gone = closed as { code: number; reason: string } | null;
      if (gone !== null) return reject(refusal(gone.code, gone.reason));
      const timer = setTimeout(() => {
        waiter = null;
        reject(new PairError("unreachable", "The computer did not answer."));
      }, timeoutMs);
      waiter = (m) => {
        clearTimeout(timer);
        if (m instanceof Uint8Array) resolve(m);
        else reject(refusal(m.code, m.reason));
      };
    });

  try {
    const phoneKey = opts.phoneKey ?? (await generateStaticKey());
    const hs = await NoiseHandshake.create({
      pattern: "IKpsk2",
      initiator: true,
      prologue: prologueFor(blob.cid),
      staticKey: phoneKey,
      peerStatic: computerStatic,
      psk,
    });
    const hello = encoder.encode(JSON.stringify({ device_name: opts.deviceName }));
    t.send((await hs.writeMessage(hello)).bytes);

    let welcome: PairWelcome | null = null;
    let channel: FramedChannel | null = null;
    try {
      const second = await hs.readMessage(await next());
      if (second.split === undefined) throw new Error("incomplete handshake");
      welcome = decodePairWelcome(JSON.parse(decoder.decode(second.payload)));
      channel = new FramedChannel(second.split.send, second.split.recv);
    } catch (err) {
      if (err instanceof PairError) throw err;
      // The computer answers a wrong code with a reply this side cannot open.
      throw new PairError(
        "refused",
        "The computer did not accept this code. Run 'deck mobile pair' again.",
      );
    }
    if (welcome === null || channel === null) {
      throw new PairError("failed", "The computer's answer was not understood.");
    }

    // Only a phone that read message 2 can send this, and only it makes the
    // computer trust the new device.
    const confirm = encoder.encode(JSON.stringify({ t: "hello", v: PROTOCOL_V3 }));
    for (const m of await channel.sealFrame(confirm)) t.send(m);

    const record: ComputerRecord = {
      cid: blob.cid,
      name: welcome.name,
      relayUrl: blob.relay,
      lan: blob.lan ?? [],
      deviceId: welcome.device_id,
      relayToken: welcome.relay_token,
      computerStatic: blob.s,
      phoneKey,
      online: true,
      lastSeen: null,
      createdAt: (opts.now?.() ?? new Date()).toISOString(),
    };
    await (opts.save ?? addComputer)(record);
    return record;
  } catch (err) {
    if (err instanceof PairError) throw err;
    // The lane closing under us (the relay or computer hung up) reads as a
    // refusal or an unreachable computer, not as a bare error.
    const gone = closed as { code: number; reason: string } | null;
    if (gone !== null) throw refusal(gone.code, gone.reason);
    throw new PairError("failed", err instanceof Error ? err.message : "Pairing failed.");
  } finally {
    // Give the confirming frame a moment to leave before the lane closes.
    setTimeout(() => t.close(), 200);
  }
}

function refusal(code: number, reason: string): PairError {
  if (code === 4404 || code === 4401 || code === 4400) {
    return new PairError(
      "refused",
      "The computer did not accept this code. Run 'deck mobile pair' again.",
    );
  }
  if (code === 4429)
    return new PairError("refused", "Too many tries. Wait a minute and try again.");
  return new PairError("unreachable", reason || "The connection closed.");
}
