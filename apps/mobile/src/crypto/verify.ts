import {
  bytesToHex,
  hexToBytes,
  importStaticKey,
  NoiseHandshake,
  type NoisePatternName,
  type SplitCiphers,
} from "../../../../pkg/remote/web/src/e2e/noise";
import { pairIdFromPsk } from "../../../../pkg/remote/web/src/hosted/pairing";
import ik from "../../../../pkg/remote/wire/testdata/noise_ik.json";
import ikpsk2 from "../../../../pkg/remote/wire/testdata/noise_ikpsk2.json";
import k from "../../../../pkg/remote/wire/testdata/noise_k.json";
import pairIds from "../../../../pkg/remote/wire/testdata/pair_id.json";

interface Vector {
  prologue_hex: string;
  initiator_static_priv_hex: string;
  responder_static_priv_hex: string;
  initiator_ephemeral_priv_hex: string;
  responder_ephemeral_priv_hex?: string;
  initiator_static_pub_hex: string;
  responder_static_pub_hex: string;
  psk_hex?: string;
  messages: Array<{ from: string; payload_hex: string; ciphertext_hex: string }>;
  handshake_hash_hex: string;
  transport: Array<{ from: string; plaintext_hex: string; ciphertext_hex: string }>;
}

function equal(actual: string, expected: string): void {
  if (actual !== expected)
    throw new Error("Native cryptography failed its interoperability check.");
}

export async function verifyNoiseVector(vector: Vector, pattern: NoisePatternName): Promise<void> {
  const initiatorStatic = await importStaticKey(hexToBytes(vector.initiator_static_priv_hex));
  const responderStatic = await importStaticKey(hexToBytes(vector.responder_static_priv_hex));
  equal(bytesToHex(initiatorStatic.pub), vector.initiator_static_pub_hex);
  equal(bytesToHex(responderStatic.pub), vector.responder_static_pub_hex);
  if (initiatorStatic.priv.extractable || responderStatic.priv.extractable) {
    throw new Error("Native private keys must be non-extractable.");
  }
  const psk = vector.psk_hex === undefined ? {} : { psk: hexToBytes(vector.psk_hex) };
  const initiator = await NoiseHandshake.create({
    pattern,
    initiator: true,
    prologue: hexToBytes(vector.prologue_hex),
    staticKey: initiatorStatic,
    peerStatic: responderStatic.pub,
    ephemeralPriv: hexToBytes(vector.initiator_ephemeral_priv_hex),
    ...psk,
  });
  const responder = await NoiseHandshake.create({
    pattern,
    initiator: false,
    prologue: hexToBytes(vector.prologue_hex),
    staticKey: responderStatic,
    ...(pattern === "K" ? { peerStatic: initiatorStatic.pub } : {}),
    ...(vector.responder_ephemeral_priv_hex === undefined
      ? {}
      : { ephemeralPriv: hexToBytes(vector.responder_ephemeral_priv_hex) }),
    ...psk,
  });
  let iniSplit: SplitCiphers | undefined;
  let rspSplit: SplitCiphers | undefined;
  for (const message of vector.messages) {
    const forward = message.from === "initiator";
    const writer = forward ? initiator : responder;
    const reader = forward ? responder : initiator;
    const written = await writer.writeMessage(hexToBytes(message.payload_hex));
    equal(bytesToHex(written.bytes), message.ciphertext_hex);
    const read = await reader.readMessage(written.bytes);
    equal(bytesToHex(read.payload), message.payload_hex);
    if (written.split && read.split) {
      iniSplit = forward ? written.split : read.split;
      rspSplit = forward ? read.split : written.split;
    }
  }
  equal(bytesToHex(initiator.handshakeHash()), vector.handshake_hash_hex);
  equal(bytesToHex(responder.handshakeHash()), vector.handshake_hash_hex);
  if (!iniSplit || !rspSplit) throw new Error("Native Noise handshake did not complete.");
  for (const message of vector.transport) {
    const forward = message.from === "initiator";
    const writer = forward ? iniSplit.send : rspSplit.send;
    const reader = forward ? rspSplit.recv : iniSplit.recv;
    const ciphertext = await writer.encrypt(hexToBytes(message.plaintext_hex));
    equal(bytesToHex(ciphertext), message.ciphertext_hex);
    equal(bytesToHex(await reader.decrypt(ciphertext)), message.plaintext_hex);
  }
}

// Run on the actual installed C++/JSI backend before opening a native session.
// No capability table or JS substitute counts as proof of native support.
// Fixed public test vectors here are unrelated to any user's identity.
export async function verifyNativeCrypto(): Promise<void> {
  let stage = "WebCrypto";
  try {
    if (!globalThis.crypto?.subtle || typeof globalThis.crypto.getRandomValues !== "function") {
      throw new Error("Crypto is unavailable.");
    }
    stage = "Noise IK";
    await verifyNoiseVector(ik as Vector, "IK");
    stage = "Noise IKpsk2";
    await verifyNoiseVector(ikpsk2 as Vector, "IKpsk2");
    stage = "Noise K";
    await verifyNoiseVector(k as Vector, "K");
    stage = "HKDF";
    for (const row of pairIds.cases)
      equal(await pairIdFromPsk(hexToBytes(row.psk_hex)), row.pair_id);
  } catch {
    // Do not echo provider errors: a provider can include its key inputs.
    throw new Error(
      `Native encryption is unavailable or incompatible (${stage}). Use a Deck development build with X25519, AES-GCM and HKDF support.`,
    );
  }
}
