import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { join } from "node:path";
import {
  bytesToHex,
  FramedChannel,
  hexToBytes,
  importStaticKey,
  NoiseHandshake,
  type NoisePatternName,
  REKEY_EVERY,
  sha256Hex,
} from "./noise";
import { open, seal } from "./seal";

const TESTDATA = join(import.meta.dir, "..", "..", "..", "wire", "testdata");

interface VectorMessage {
  from: string;
  payload_hex?: string;
  plaintext_hex?: string;
  ciphertext_hex: string;
}

interface NoiseVector {
  protocol: string;
  prologue_hex: string;
  psk_hex?: string;
  initiator_static_priv_hex: string;
  responder_static_priv_hex: string;
  initiator_ephemeral_priv_hex: string;
  responder_ephemeral_priv_hex?: string;
  messages: VectorMessage[];
  handshake_hash_hex: string;
  transport: VectorMessage[];
}

function readVector(name: string): NoiseVector {
  return JSON.parse(readFileSync(join(TESTDATA, `${name}.json`), "utf8")) as NoiseVector;
}

function vectorCiphertext(v: NoiseVector): string {
  const first = v.messages[0];
  if (first === undefined) throw new Error("vector has no messages");
  return first.ciphertext_hex;
}

const VECTORS: Array<{ file: string; pattern: NoisePatternName; payloads: string[] }> = [
  {
    file: "noise_ikpsk2",
    pattern: "IKpsk2",
    payloads: [`{"device_name":"Pixel 9"}`, `{"device_id":"dev_7a1f"}`],
  },
  {
    file: "noise_ik",
    pattern: "IK",
    payloads: [`{"t":"hello","v":3}`, `{"t":"welcome"}`],
  },
  {
    file: "noise_k",
    pattern: "K",
    payloads: [`{"qid":"0192f3a0","kind":"chat.send"}`],
  },
];

describe("noise vectors", () => {
  for (const { file, pattern, payloads } of VECTORS) {
    test(`${file} replays byte for byte`, async () => {
      const v = readVector(file);
      const prologue = hexToBytes(v.prologue_hex);
      const initStatic = await importStaticKey(hexToBytes(v.initiator_static_priv_hex));
      const respStatic = await importStaticKey(hexToBytes(v.responder_static_priv_hex));
      const psk = v.psk_hex !== undefined ? hexToBytes(v.psk_hex) : undefined;
      const respPub = (await importStaticKey(hexToBytes(v.responder_static_priv_hex))).pub;
      const initPub = (await importStaticKey(hexToBytes(v.initiator_static_priv_hex))).pub;
      const ini = await NoiseHandshake.create({
        pattern,
        initiator: true,
        prologue,
        staticKey: initStatic,
        peerStatic: respPub,
        psk,
        ephemeralPriv: hexToBytes(v.initiator_ephemeral_priv_hex),
      });
      const rsp = await NoiseHandshake.create({
        pattern,
        initiator: false,
        prologue,
        staticKey: respStatic,
        peerStatic: pattern === "K" ? initPub : undefined,
        psk,
        ephemeralPriv:
          v.responder_ephemeral_priv_hex !== undefined
            ? hexToBytes(v.responder_ephemeral_priv_hex)
            : undefined,
      });
      let iniSplit = null;
      let rspSplit = null;
      for (let i = 0; i < payloads.length; i++) {
        const text = payloads[i];
        if (text === undefined) throw new Error("missing payload");
        const payload = new TextEncoder().encode(text);
        const fromInitiator = i % 2 === 0;
        const w = fromInitiator ? ini : rsp;
        const r = fromInitiator ? rsp : ini;
        const written = await w.writeMessage(payload);
        const wantMsg = v.messages[i];
        if (wantMsg === undefined) throw new Error("vector missing message");
        expect(bytesToHex(written.bytes)).toBe(wantMsg.ciphertext_hex);
        const read = await r.readMessage(written.bytes);
        expect(new TextDecoder().decode(read.payload)).toBe(text);
        // The responder learns the initiator static from the S token.
        if (pattern !== "K" && !fromInitiator) {
          expect(read.peerStatic !== undefined).toBe(false);
        }
        if (written.split !== undefined && read.split !== undefined) {
          iniSplit = fromInitiator ? written.split : read.split;
          rspSplit = fromInitiator ? read.split : written.split;
        }
      }
      expect(bytesToHex(ini.handshakeHash())).toBe(v.handshake_hash_hex);
      expect(bytesToHex(rsp.handshakeHash())).toBe(v.handshake_hash_hex);
      if (iniSplit === null || rspSplit === null) throw new Error("handshake did not split");
      for (const t of v.transport) {
        const pt = hexToBytes(t.plaintext_hex ?? "");
        if (t.from === "initiator") {
          const ct = await iniSplit.send.encrypt(pt);
          expect(bytesToHex(ct)).toBe(t.ciphertext_hex);
          expect(await rspSplit.recv.decrypt(ct)).toEqual(pt);
        } else {
          const ct = await rspSplit.send.encrypt(pt);
          expect(bytesToHex(ct)).toBe(t.ciphertext_hex);
          expect(await iniSplit.recv.decrypt(ct)).toEqual(pt);
        }
      }
    });
  }

  test("wrong pairing PSK fails on message 2", async () => {
    const v = readVector("noise_ikpsk2");
    const prologue = hexToBytes(v.prologue_hex);
    const initStatic = await importStaticKey(hexToBytes(v.initiator_static_priv_hex));
    const respStatic = await importStaticKey(hexToBytes(v.responder_static_priv_hex));
    const respPub = (await importStaticKey(hexToBytes(v.responder_static_priv_hex))).pub;
    const good = hexToBytes(v.psk_hex ?? "");
    const bad = good.slice();
    bad[0] = (bad[0] ?? 0) ^ 1;
    const ini = await NoiseHandshake.create({
      pattern: "IKpsk2",
      initiator: true,
      prologue,
      staticKey: initStatic,
      peerStatic: respPub,
      psk: good,
      ephemeralPriv: hexToBytes(v.initiator_ephemeral_priv_hex),
    });
    const rsp = await NoiseHandshake.create({
      pattern: "IKpsk2",
      initiator: false,
      prologue,
      staticKey: respStatic,
      psk: bad,
      ephemeralPriv: hexToBytes(v.responder_ephemeral_priv_hex ?? ""),
    });
    // Message 1 still opens; the initiator rejects message 2.
    const m1 = await ini.writeMessage(new TextEncoder().encode("hi"));
    await rsp.readMessage(m1.bytes);
    const m2 = await rsp.writeMessage(new TextEncoder().encode("ok"));
    await expect(ini.readMessage(m2.bytes)).rejects.toThrow();
  });

  test("K seal/open round-trips a queued action", async () => {
    const v = readVector("noise_k");
    const prologue = hexToBytes(v.prologue_hex);
    const sender = await importStaticKey(hexToBytes(v.initiator_static_priv_hex));
    const recipient = await importStaticKey(hexToBytes(v.responder_static_priv_hex));
    const plaintext = new TextEncoder().encode(`{"qid":"0192f3a0","kind":"chat.send"}`);
    const sealed = await seal({
      senderStatic: sender,
      recipientStaticPub: recipient.pub,
      prologue,
      plaintext,
      ephemeralPriv: hexToBytes(v.initiator_ephemeral_priv_hex),
    });
    expect(bytesToHex(sealed)).toBe(vectorCiphertext(v));
    const { plaintext: opened } = await open({
      recipientStatic: recipient,
      senderStaticPub: sender.pub,
      prologue,
      message: sealed,
    });
    expect(opened).toEqual(plaintext);
  });

  test("noise_frames.json chunking replays byte for byte", async () => {
    interface FramesVector {
      frames: Array<{
        from: string;
        plaintext_length: number;
        plaintext_sha256_hex: string;
        chunk_ciphertext_lengths: number[];
        chunk_ciphertext_sha256_hex: string[];
      }>;
      rekey_every: number;
    }
    const fv = JSON.parse(
      readFileSync(join(TESTDATA, "noise_frames.json"), "utf8"),
    ) as FramesVector;
    expect(REKEY_EVERY).toBe(fv.rekey_every);
    const v = readVector("noise_ik");
    const prologue = hexToBytes(v.prologue_hex);
    const initStatic = await importStaticKey(hexToBytes(v.initiator_static_priv_hex));
    const respStatic = await importStaticKey(hexToBytes(v.responder_static_priv_hex));
    const respPub = (await importStaticKey(hexToBytes(v.responder_static_priv_hex))).pub;
    const ini = await NoiseHandshake.create({
      pattern: "IK",
      initiator: true,
      prologue,
      staticKey: initStatic,
      peerStatic: respPub,
      ephemeralPriv: hexToBytes(v.initiator_ephemeral_priv_hex),
    });
    const rsp = await NoiseHandshake.create({
      pattern: "IK",
      initiator: false,
      prologue,
      staticKey: respStatic,
      ephemeralPriv: hexToBytes(v.responder_ephemeral_priv_hex ?? ""),
    });
    const hello = new TextEncoder().encode(`{"t":"hello","v":3}`);
    const m1 = await ini.writeMessage(hello);
    await rsp.readMessage(m1.bytes);
    const m2 = await rsp.writeMessage(new TextEncoder().encode(`{"t":"welcome"}`));
    const r2 = await ini.readMessage(m2.bytes);
    if (m2.split === undefined || r2.split === undefined) {
      throw new Error("handshake did not split");
    }
    const phone = new FramedChannel(r2.split.send, r2.split.recv);
    const computer = new FramedChannel(m2.split.send, m2.split.recv);
    const big = new Uint8Array(140000);
    for (let i = 0; i < big.length; i++) big[i] = (i * 7) % 256;
    const frames: Array<{ from: "initiator" | "responder"; frame: Uint8Array }> = [
      {
        from: "initiator",
        frame: new TextEncoder().encode(`{"t":"req","id":"1","m":"inbox.get"}`),
      },
      { from: "initiator", frame: new Uint8Array(0) },
      { from: "initiator", frame: big },
      {
        from: "responder",
        frame: new TextEncoder().encode(`{"t":"res","id":"1","ok":true,"r":[]}`),
      },
    ];
    expect(fv.frames).toHaveLength(frames.length);
    for (let i = 0; i < frames.length; i++) {
      const f = frames[i];
      const want = fv.frames[i];
      if (f === undefined || want === undefined) throw new Error("vector/frame mismatch");
      expect(want.from).toBe(f.from);
      expect(want.plaintext_length).toBe(f.frame.length);
      const sender = f.from === "initiator" ? phone : computer;
      const receiver = f.from === "initiator" ? computer : phone;
      const chunks = await sender.sealFrame(f.frame);
      expect(chunks.map((c) => c.length)).toEqual(want.chunk_ciphertext_lengths);
      let got: Uint8Array | null = null;
      for (let j = 0; j < chunks.length; j++) {
        const chunk = chunks[j];
        if (chunk === undefined) throw new Error("missing chunk");
        const wantHash = want.chunk_ciphertext_sha256_hex[j];
        if (wantHash === undefined) throw new Error("vector missing chunk hash");
        expect(await sha256Hex(chunk)).toBe(wantHash);
        got = await receiver.openMessage(chunk);
        if (j < chunks.length - 1) expect(got).toBeNull();
      }
      expect(got).not.toBeNull();
      expect(got?.length).toBe(f.frame.length);
      expect(got).toEqual(f.frame);
    }
  });
});
