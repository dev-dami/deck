// One-shot Noise K seal/open for queued offline actions
// (docs/mobile-contract.md §3): both statics known, one message each way is
// never needed — the sender writes a single message, the receiver reads it.
import { NoiseHandshake, type StaticKey } from "./noise";

export interface SealOpts {
  senderStatic: StaticKey;
  recipientStaticPub: Uint8Array;
  prologue: Uint8Array;
  plaintext: Uint8Array;
  ephemeralPriv?: Uint8Array;
}

export async function seal(opts: SealOpts): Promise<Uint8Array> {
  const hs = await NoiseHandshake.create({
    pattern: "K",
    initiator: true,
    prologue: opts.prologue,
    staticKey: opts.senderStatic,
    peerStatic: opts.recipientStaticPub,
    ephemeralPriv: opts.ephemeralPriv,
  });
  const { bytes } = await hs.writeMessage(opts.plaintext);
  return bytes;
}

export interface OpenOpts {
  recipientStatic: StaticKey;
  senderStaticPub: Uint8Array;
  prologue: Uint8Array;
  message: Uint8Array;
}

export async function open(opts: OpenOpts): Promise<{ plaintext: Uint8Array }> {
  const hs = await NoiseHandshake.create({
    pattern: "K",
    initiator: false,
    prologue: opts.prologue,
    staticKey: opts.recipientStatic,
    peerStatic: opts.senderStaticPub,
  });
  const { payload } = await hs.readMessage(opts.message);
  return { plaintext: payload };
}
