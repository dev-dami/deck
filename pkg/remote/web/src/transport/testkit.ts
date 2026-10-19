// Test helpers for the v3 client: an in-memory pipe and a fake computer that
// terminates the Noise channel as the responder. Used by tests only; it speaks
// the same wire as the Go daemon, which the Noise vectors pin down.
import { FramedChannel, NoiseHandshake, type StaticKey } from "../e2e/noise";
import { prologueFor } from "../session3";
import type { SocketLike, Transport } from "./transport";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

class PipeEnd implements Transport {
  peer!: PipeEnd;
  private messageCb: ((m: Uint8Array) => void) | null = null;
  private closeCb: ((code: number, reason: string) => void) | null = null;
  private held: Uint8Array[] = [];
  closed: { code: number; reason: string } | null = null;
  sent = 0;

  send(msg: Uint8Array): void {
    if (this.closed !== null) throw new Error("closed");
    this.sent += 1;
    const copy = msg.slice();
    queueMicrotask(() => this.peer.deliver(copy));
  }

  deliver(m: Uint8Array): void {
    if (this.messageCb !== null) this.messageCb(m);
    else this.held.push(m);
  }

  onMessage(cb: (m: Uint8Array) => void): void {
    this.messageCb = cb;
    const held = this.held;
    this.held = [];
    for (const m of held) cb(m);
  }

  onClose(cb: (code: number, reason: string) => void): void {
    this.closeCb = cb;
    if (this.closed !== null) cb(this.closed.code, this.closed.reason);
  }

  close(code = 1000, reason = ""): void {
    if (this.closed !== null) return;
    this.closed = { code, reason };
    this.closeCb?.(code, reason);
    queueMicrotask(() => this.peer.remoteClosed(code, reason));
  }

  remoteClosed(code: number, reason: string): void {
    if (this.closed !== null) return;
    this.closed = { code, reason };
    this.closeCb?.(code, reason);
  }
}

// Two connected ends: what one sends the other receives, in order.
export function pipePair(): [Transport & { closed: unknown }, Transport & { closed: unknown }] {
  const a = new PipeEnd();
  const b = new PipeEnd();
  a.peer = b;
  b.peer = a;
  return [a, b];
}

export interface FakeComputerOptions {
  cid: string;
  staticKey: StaticKey;
  // Phones that may connect (their static public keys). Others are closed
  // with 4401, as the daemon does.
  allowed?: Uint8Array[];
  // IKpsk2 pairing instead of IK when set.
  psk?: Uint8Array;
  // Answers a request; throw FakeRpcError to refuse.
  handle?: (method: string, pj: string | undefined, params: unknown) => unknown | Promise<unknown>;
  // The pairing reply (PairWelcome) when psk is set.
  pairWelcome?: Record<string, unknown>;
}

export class FakeRpcError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
  }
}

export function sameBytes(a: Uint8Array, b: Uint8Array): boolean {
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

export const welcomeFrame = (
  cid: string,
  over: Record<string, unknown> = {},
): Record<string, unknown> => ({
  t: "welcome",
  epoch: "ep_test",
  replay: false,
  server: {
    version: "9.9",
    cid,
    name: "Test PC",
    projects: [{ id: "prj_a", name: "deck", online: true }],
    kinds: ["claude"],
    allow_yolo: false,
    features: ["inbox"],
    protocol: 3,
    tunnel: "",
    theme: { palette: { accent: "#7dd3c0" }, kinds: { claude: { mark: "✻", tint: "#d97757" } } },
  },
  device: { id: "dev_1", name: "Pixel", expires_at: "2027-01-12T09:30:00Z" },
  ...over,
});

// A computer on the far end of a Transport.
export class FakeComputer {
  readonly received: unknown[] = [];
  phoneStatic: Uint8Array | null = null;
  private channel: FramedChannel | null = null;
  private chain: Promise<void> = Promise.resolve();
  private stage: "handshake" | "hello" | "frames" = "handshake";
  helloSeen: unknown = null;
  private sendChain: Promise<void> = Promise.resolve();

  constructor(
    private readonly t: Transport,
    private readonly opts: FakeComputerOptions,
  ) {
    t.onMessage((m) => {
      this.chain = this.chain.then(() => this.onMessage(m)).catch(() => undefined);
    });
  }

  private async onMessage(m: Uint8Array): Promise<void> {
    if (this.stage === "handshake") {
      const hs = await NoiseHandshake.create({
        pattern: this.opts.psk !== undefined ? "IKpsk2" : "IK",
        initiator: false,
        prologue: prologueFor(this.opts.cid),
        staticKey: this.opts.staticKey,
        psk: this.opts.psk,
      });
      let read: Awaited<ReturnType<NoiseHandshake["readMessage"]>>;
      try {
        read = await hs.readMessage(m);
      } catch {
        this.t.close(4400, "bad hello");
        return;
      }
      this.phoneStatic = read.peerStatic ?? null;
      const allowed = this.opts.allowed;
      if (
        allowed !== undefined &&
        (this.phoneStatic === null ||
          !allowed.some((a) => sameBytes(a, this.phoneStatic as Uint8Array)))
      ) {
        this.t.close(4401, "unknown device");
        return;
      }
      const reply =
        this.opts.psk !== undefined
          ? encoder.encode(JSON.stringify(this.opts.pairWelcome ?? {}))
          : new Uint8Array(0);
      const out = await hs.writeMessage(reply);
      this.t.send(out.bytes);
      if (out.split === undefined) throw new Error("handshake did not finish");
      this.channel = new FramedChannel(out.split.send, out.split.recv);
      this.stage = "hello";
      return;
    }
    const channel = this.channel;
    if (channel === null) return;
    const raw = await channel.openMessage(m);
    if (raw === null) return;
    const frame = JSON.parse(decoder.decode(raw)) as Record<string, unknown>;
    this.received.push(frame);
    if (this.stage === "hello") {
      this.helloSeen = frame;
      this.stage = "frames";
      if (this.opts.psk === undefined) await this.send(welcomeFrame(this.opts.cid));
      return;
    }
    if (frame["t"] === "req") await this.answer(frame);
  }

  private async answer(req: Record<string, unknown>): Promise<void> {
    const id = String(req["id"]);
    try {
      const handle = this.opts.handle;
      const result =
        handle === undefined
          ? {}
          : await handle(
              String(req["m"]),
              typeof req["pj"] === "string" ? req["pj"] : undefined,
              req["p"],
            );
      await this.send({ t: "res", id, ok: true, r: result });
    } catch (err) {
      if (err instanceof FakeRpcError) {
        await this.send({ t: "res", id, ok: false, err: { code: err.code, msg: err.message } });
      } else throw err;
    }
  }

  send(frame: unknown): Promise<void> {
    const channel = this.channel;
    if (channel === null) return Promise.reject(new Error("no channel yet"));
    const run = this.sendChain.then(async () => {
      for (const m of await channel.sealFrame(encoder.encode(JSON.stringify(frame)))) {
        this.t.send(m);
      }
    });
    this.sendChain = run.catch(() => undefined);
    return run;
  }
}

// A socket whose far end is scripted by the test.
export class ScriptedSocket implements SocketLike {
  binaryType = "blob";
  readyState = 0;
  onopen: ((ev: unknown) => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  onerror: ((ev: unknown) => void) | null = null;
  readonly sent: Array<string | ArrayBufferLike | ArrayBufferView> = [];
  closedWith: { code?: number; reason?: string } | null = null;

  constructor(readonly url: string) {}

  open(): void {
    this.readyState = 1;
    this.onopen?.({});
  }

  receive(data: unknown): void {
    this.onmessage?.({ data });
  }

  fail(code: number, reason = ""): void {
    this.readyState = 3;
    this.onclose?.({ code, reason });
  }

  send(data: string | ArrayBufferLike | ArrayBufferView): void {
    this.sent.push(data);
  }

  close(code?: number, reason?: string): void {
    this.closedWith = {
      ...(code !== undefined ? { code } : {}),
      ...(reason !== undefined ? { reason } : {}),
    };
    this.readyState = 3;
  }
}
