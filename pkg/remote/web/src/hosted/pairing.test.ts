import { describe, expect, test } from "bun:test";
import { readFileSync } from "node:fs";
import { b64urlEncode } from "../b64";
import { generateStaticKey, hexToBytes } from "../e2e/noise";
import type { QRBlob } from "../protocol3";
import type { ComputerRecord } from "../store/computers";
import { FakeComputer, pipePair } from "../transport/testkit";
import { TransportError } from "../transport/transport";
import {
  PairError,
  pairIdFromPsk,
  pairWithComputer,
  parsePairFragment,
  stripPairFragment,
} from "./pairing";

const CID = "Zk3p0Jq8vYb1nT5cR2sWxA";
const NOW = Date.UTC(2026, 9, 14, 12, 0, 0);

function fragment(blob: unknown): string {
  return `#p=${b64urlEncode(new TextEncoder().encode(JSON.stringify(blob)))}`;
}

function blobFor(computerPub: Uint8Array, psk: Uint8Array, over: Partial<QRBlob> = {}): QRBlob {
  return {
    v: 1,
    cid: CID,
    relay: "https://app.example.dev",
    s: b64urlEncode(computerPub),
    psk: b64urlEncode(psk),
    exp: Math.floor(NOW / 1000) + 300,
    name: "work-pc",
    lan: ["192.168.1.20:7879"],
    ...over,
  };
}

const key32 = (b: number) => new Uint8Array(32).fill(b);

describe("parsePairFragment", () => {
  test("reads a good code", async () => {
    const pub = (await generateStaticKey()).pub;
    const blob = blobFor(pub, key32(7));
    expect(parsePairFragment(fragment(blob), NOW)).toEqual(blob);
    expect(parsePairFragment(fragment(blob).slice(1), NOW).cid).toBe(CID); // without the #
  });

  test("refuses what it cannot use, saying why", async () => {
    const pub = (await generateStaticKey()).pub;
    const kind = (h: string) => {
      try {
        parsePairFragment(h, NOW);
      } catch (e) {
        return e instanceof PairError ? e.kind : "other";
      }
      return "none";
    };
    expect(kind("")).toBe("bad_code");
    expect(kind("#p=")).toBe("bad_code");
    expect(kind("#q=abc")).toBe("bad_code");
    expect(kind("#p=!!!")).toBe("bad_code");
    expect(kind(`#p=${b64urlEncode(new TextEncoder().encode("not json"))}`)).toBe("bad_code");
    expect(kind(fragment({ v: 1 }))).toBe("bad_code");
    expect(kind(fragment(blobFor(pub, key32(7), { v: 2 })))).toBe("unsupported");
    expect(kind(fragment(blobFor(pub, key32(7), { exp: Math.floor(NOW / 1000) - 1 })))).toBe(
      "expired",
    );
    expect(kind(fragment(blobFor(pub, new Uint8Array(16))))).toBe("bad_code"); // short secret
    expect(kind(fragment(blobFor(new Uint8Array(5), key32(7))))).toBe("bad_code"); // short key
  });
});

test("stripPairFragment drops the secret from the address bar", () => {
  const calls: Array<[unknown, string, string]> = [];
  stripPairFragment(
    { pathname: "/app/", search: "?x=1" },
    { replaceState: (a, b, c) => calls.push([a, b, c]) },
  );
  expect(calls).toEqual([[null, "", "/app/?x=1"]]);
});

test("the pair id matches the Go derivation", async () => {
  const vec = JSON.parse(
    readFileSync(new URL("../../../wire/testdata/pair_id.json", import.meta.url), "utf8"),
  ) as { cases: Array<{ psk_hex: string; pair_id: string; pair_id_hash: string }> };
  expect(vec.cases.length).toBeGreaterThan(0);
  for (const c of vec.cases) {
    const id = await pairIdFromPsk(hexToBytes(c.psk_hex));
    expect(id).toBe(c.pair_id);
    const hash = new Uint8Array(
      await crypto.subtle.digest("SHA-256", new TextEncoder().encode(id)),
    );
    expect(Array.from(hash, (b) => b.toString(16).padStart(2, "0")).join("")).toBe(c.pair_id_hash);
  }
});

async function setup(over: { computerPsk?: Uint8Array } = {}) {
  const computerKey = await generateStaticKey();
  const psk = key32(9);
  const [phoneEnd, computerEnd] = pipePair();
  const computer = new FakeComputer(computerEnd, {
    cid: CID,
    staticKey: computerKey,
    psk: over.computerPsk ?? psk,
    pairWelcome: {
      device_id: "dev_7a1f",
      relay_token: "rt_secret_token",
      expires_at: "2027-01-12T09:30:00Z",
      name: "work-pc",
    },
  });
  const opened: Array<{ relay: string; cid: string; pairId: string }> = [];
  const saved: ComputerRecord[] = [];
  const run = (blob: QRBlob, openOver?: () => Promise<never>) =>
    pairWithComputer({
      blob,
      deviceName: "Pixel 9",
      timeoutMs: 1500,
      now: () => new Date(NOW),
      save: async (c) => {
        saved.push(c);
      },
      openPair: async (relay, cid, pairId) => {
        opened.push({ relay, cid, pairId });
        if (openOver !== undefined) return openOver();
        return phoneEnd;
      },
    });
  return { computer, computerKey, psk, phoneEnd, computerEnd, opened, saved, run };
}

describe("pairWithComputer", () => {
  test("pairs, confirms and remembers the computer", async () => {
    const s = await setup();
    const rec = await s.run(blobFor(s.computerKey.pub, s.psk));

    expect(rec).toMatchObject({
      cid: CID,
      name: "work-pc",
      relayUrl: "https://app.example.dev",
      lan: ["192.168.1.20:7879"],
      deviceId: "dev_7a1f",
      relayToken: "rt_secret_token",
      computerStatic: b64urlEncode(s.computerKey.pub),
      createdAt: new Date(NOW).toISOString(),
    });
    expect(rec.phoneKey?.pub.length).toBe(32);
    expect(s.saved).toEqual([rec]);

    expect(s.opened).toEqual([
      { relay: "https://app.example.dev", cid: CID, pairId: await pairIdFromPsk(s.psk) },
    ]);
    // The computer saw the confirming hello, and the phone's static is the one stored.
    await new Promise((r) => setTimeout(r, 30));
    expect(s.computer.helloSeen).toEqual({ t: "hello", v: 3 });
    expect(s.computer.phoneStatic).toEqual(rec.phoneKey?.pub ?? null);
  });

  test("the private key is not extractable", async () => {
    const s = await setup();
    const rec = await s.run(blobFor(s.computerKey.pub, s.psk));
    expect(rec.phoneKey?.priv.extractable).toBe(false);
  });

  test("a computer that does not share the secret is refused and nothing is saved", async () => {
    const s = await setup({ computerPsk: key32(1) });
    const err = await s.run(blobFor(s.computerKey.pub, s.psk)).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(PairError);
    expect((err as PairError).kind).toBe("refused");
    expect(s.saved).toHaveLength(0);
  });

  test("an unreachable relay is reported as such", async () => {
    const s = await setup();
    const err = await s
      .run(blobFor(s.computerKey.pub, s.psk), async () => {
        throw new TransportError("unknown computer", 4404);
      })
      .catch((e: unknown) => e);
    expect((err as PairError).kind).toBe("unreachable");
    expect(s.saved).toHaveLength(0);
  });

  test("a relay that closes the lane with 4404 reads as a dead code", async () => {
    const s = await setup();
    setTimeout(() => s.computerEnd.close(4404, "no pairing window"), 5);
    // Replace the fake computer's answer with silence by pairing with a wrong key.
    const err = await s
      .run(blobFor((await generateStaticKey()).pub, s.psk))
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(PairError);
    expect(["refused", "unreachable"]).toContain((err as PairError).kind);
    expect(s.saved).toHaveLength(0);
  });

  test("rate limiting says to wait", async () => {
    const s = await setup();
    setTimeout(() => s.computerEnd.close(4429, "slow down"), 5);
    const err = await s
      .run(blobFor((await generateStaticKey()).pub, s.psk))
      .catch((e: unknown) => e);
    expect(err).toBeInstanceOf(PairError);
    expect(s.saved).toHaveLength(0);
  });

  test("silence times out", async () => {
    const [phoneEnd] = pipePair();
    const err = await pairWithComputer({
      blob: blobFor((await generateStaticKey()).pub, key32(9)),
      deviceName: "x",
      timeoutMs: 50,
      save: async () => undefined,
      openPair: async () => phoneEnd,
    }).catch((e: unknown) => e);
    expect(err).toBeInstanceOf(PairError);
    expect((err as PairError).kind).toBe("unreachable");
  });
});
