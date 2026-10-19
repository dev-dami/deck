// A v3 session with one computer: Noise IK over a Transport, then the
// hello/welcome exchange and request/response/event frames of
// docs/mobile-contract.md §2.
import { FramedChannel, NoiseHandshake, type StaticKey } from "./e2e/noise";
import {
  decodeEv3Frame,
  decodeRes3Frame,
  decodeWelcome3Frame,
  PROTOCOL_V3,
  type Resume3,
  type Welcome3Frame,
} from "./protocol3";
import { type Transport, TransportError } from "./transport/transport";

const encoder = new TextEncoder();
const decoder = new TextDecoder();

export function prologueFor(cid: string): Uint8Array {
  return encoder.encode(`deck-v3|${cid}`);
}

// The computer refused a request: a code from the contract and a message for
// the person.
export class RpcError extends Error {
  constructor(
    readonly code: string,
    message: string,
  ) {
    super(message);
    this.name = "RpcError";
  }
}

export interface SessionEvent {
  seq: number;
  name: string;
  pj: string | undefined;
  payload: unknown;
}

export interface Session3Options {
  transport: Transport;
  phoneKey: StaticKey;
  computerStatic: Uint8Array;
  cid: string;
  resume?: Resume3;
  // How long the handshake and hello may take.
  handshakeTimeoutMs?: number;
  callTimeoutMs?: number;
}

export interface CloseInfo {
  code: number;
  reason: string;
}

type Pending = {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
  timer: ReturnType<typeof setTimeout>;
};

export class Session3 {
  readonly welcome: Welcome3Frame;
  // True when the computer says the phone's position cannot be replayed and
  // everything must be fetched again.
  resyncNeeded = false;
  // Where to resume from on a reconnect.
  epoch: string;
  lastSeq: number;

  private readonly pending = new Map<string, Pending>();
  private readonly eventCbs: Array<(ev: SessionEvent) => void> = [];
  private readonly closeCbs: Array<(info: CloseInfo) => void> = [];
  private closed: CloseInfo | null = null;
  private nextId = 1;
  private sendChain: Promise<void> = Promise.resolve();
  private recvChain: Promise<void> = Promise.resolve();

  private constructor(
    private readonly transport: Transport,
    private readonly channel: FramedChannel,
    welcome: Welcome3Frame,
    private readonly callTimeoutMs: number,
  ) {
    this.welcome = welcome;
    this.epoch = welcome.epoch;
    this.lastSeq = 0;
  }

  // Runs the handshake and the hello/welcome exchange. Rejects with a
  // TransportError carrying the close code if the computer refuses the phone
  // (4401 unknown device, 4403 revoked) and with Error for anything else.
  static async open(opts: Session3Options): Promise<Session3> {
    const timeoutMs = opts.handshakeTimeoutMs ?? 15_000;
    const t = opts.transport;
    const inbox = new Inbox(t);

    try {
      const hs = await NoiseHandshake.create({
        pattern: "IK",
        initiator: true,
        prologue: prologueFor(opts.cid),
        staticKey: opts.phoneKey,
        peerStatic: opts.computerStatic,
      });
      const first = await hs.writeMessage(new Uint8Array(0));
      t.send(first.bytes);

      const msg2 = await inbox.next(timeoutMs);
      const second = await hs.readMessage(msg2);
      if (second.split === undefined) throw new Error("the handshake did not finish");
      const channel = new FramedChannel(second.split.send, second.split.recv);

      const hello = {
        t: "hello",
        v: PROTOCOL_V3,
        ...(opts.resume !== undefined ? { resume: opts.resume } : {}),
      };
      for (const m of await channel.sealFrame(encoder.encode(JSON.stringify(hello)))) t.send(m);

      const welcome = decodeWelcome3Frame(await readFrame(channel, inbox, timeoutMs));
      if (welcome === null) throw new Error("the computer did not answer the hello");
      // A resync frame may follow the welcome immediately; the receive loop
      // below picks it up, since it takes over before anything else is read.
      const session = new Session3(t, channel, welcome, opts.callTimeoutMs ?? 120_000);
      session.start(inbox);
      return session;
    } catch (err) {
      inbox.detach();
      t.close();
      throw err;
    }
  }

  private start(inbox: Inbox): void {
    inbox.handoff(
      (msg) => {
        this.recvChain = this.recvChain.then(() => this.receive(msg));
      },
      (code, reason) => this.finish({ code, reason }),
    );
  }

  private async receive(msg: Uint8Array): Promise<void> {
    let raw: Uint8Array | null;
    try {
      raw = await this.channel.openMessage(msg);
    } catch {
      // A message that fails to authenticate means the pipe is not to be trusted.
      this.transport.close(4400, "bad message");
      this.finish({ code: 4400, reason: "bad message" });
      return;
    }
    if (raw === null) return;
    let json: unknown;
    try {
      json = JSON.parse(decoder.decode(raw));
    } catch {
      return;
    }
    this.dispatch(json);
  }

  private dispatch(json: unknown): void {
    if (this.closed !== null) return;
    if (typeof json !== "object" || json === null) return;
    const t = (json as { t?: unknown }).t;
    if (t === "res") {
      const res = decodeRes3Frame(json);
      if (res === null) return;
      const p = this.pending.get(res.id);
      if (p === undefined) return;
      this.pending.delete(res.id);
      clearTimeout(p.timer);
      if (res.ok) p.resolve(res.r);
      else p.reject(new RpcError(res.err.code, res.err.msg));
    } else if (t === "ev") {
      const ev = decodeEv3Frame(json);
      if (ev === null) return;
      this.lastSeq = ev.seq;
      const out: SessionEvent = { seq: ev.seq, name: ev.e, pj: ev.pj, payload: ev.p };
      for (const cb of [...this.eventCbs]) cb(out);
    } else if (t === "resync") {
      this.resyncNeeded = true;
      for (const cb of [...this.eventCbs]) {
        cb({ seq: this.lastSeq, name: "resync", pj: undefined, payload: undefined });
      }
    }
  }

  onEvent(cb: (ev: SessionEvent) => void): () => void {
    if (this.closed !== null) return () => {};
    // A wrapper gives each registration its own identity, including callers
    // which deliberately register the same callback more than once.
    const listener = (event: SessionEvent): void => cb(event);
    this.eventCbs.push(listener);
    return () => {
      const index = this.eventCbs.indexOf(listener);
      if (index !== -1) this.eventCbs.splice(index, 1);
    };
  }

  onClose(cb: (info: CloseInfo) => void): () => void {
    if (this.closed !== null) {
      cb(this.closed);
      return () => {};
    }
    const listener = (info: CloseInfo): void => cb(info);
    this.closeCbs.push(listener);
    return () => {
      const index = this.closeCbs.indexOf(listener);
      if (index !== -1) this.closeCbs.splice(index, 1);
    };
  }

  get isClosed(): boolean {
    return this.closed !== null;
  }

  // The position to hand to the next connection so missed events replay.
  resumePoint(focus?: Resume3["focus"]): Resume3 {
    return { epoch: this.epoch, seq: this.lastSeq, ...(focus !== undefined ? { focus } : {}) };
  }

  // Sends a request and resolves with its result. pj names the project for
  // project methods and is left out for computer-level ones.
  call<T = unknown>(method: string, params?: unknown, pj?: string): Promise<T> {
    if (this.closed !== null) {
      return Promise.reject(new TransportError("the connection is closed", this.closed.code));
    }
    const id = String(this.nextId++);
    const frame: Record<string, unknown> = { t: "req", id, m: method };
    if (pj !== undefined && pj !== "") frame["pj"] = pj;
    if (params !== undefined) frame["p"] = params;
    return new Promise<T>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.pending.delete(id);
        reject(new RpcError("timeout", "the computer did not answer in time"));
      }, this.callTimeoutMs);
      this.pending.set(id, { resolve: (v) => resolve(v as T), reject, timer });
      this.sendFrame(encoder.encode(JSON.stringify(frame))).catch((err: unknown) => {
        const p = this.pending.get(id);
        if (p === undefined) return;
        this.pending.delete(id);
        clearTimeout(p.timer);
        p.reject(err instanceof Error ? err : new Error("could not send"));
      });
    });
  }

  // Frames are sealed one at a time: Noise nonces follow send order, so two
  // frames sealed concurrently could reach the wire swapped.
  private sendFrame(bytes: Uint8Array): Promise<void> {
    const run = this.sendChain.then(async () => {
      for (const m of await this.channel.sealFrame(bytes)) this.transport.send(m);
    });
    this.sendChain = run.catch(() => undefined);
    return run;
  }

  close(): void {
    this.transport.close();
    this.finish({ code: 1000, reason: "" });
  }

  private finish(info: CloseInfo): void {
    if (this.closed !== null) return;
    this.closed = info;
    const err = new TransportError(info.reason || "the connection closed", info.code);
    for (const [id, p] of this.pending) {
      clearTimeout(p.timer);
      p.reject(err);
      this.pending.delete(id);
    }
    const callbacks = [...this.closeCbs];
    this.closeCbs.length = 0;
    this.eventCbs.length = 0;
    for (const cb of callbacks) cb(info);
  }
}

async function readFrame(
  channel: FramedChannel,
  inbox: Inbox,
  timeoutMs: number,
): Promise<unknown> {
  for (;;) {
    const msg = await inbox.next(timeoutMs);
    const raw = await channel.openMessage(msg);
    if (raw !== null) return JSON.parse(decoder.decode(raw));
  }
}

// Collects transport messages for the handshake's request/reply steps, then
// hands the stream over to the session's receive loop without dropping or
// reordering anything that arrived in between.
class Inbox {
  private queue: Uint8Array[] = [];
  private waiter: { resolve: (m: Uint8Array) => void; reject: (e: Error) => void } | null = null;
  private closedWith: CloseInfo | null = null;
  private forward: ((msg: Uint8Array) => void) | null = null;
  private closeForward: ((code: number, reason: string) => void) | null = null;

  constructor(t: Transport) {
    t.onMessage((m) => {
      if (this.forward !== null) this.forward(m);
      else if (this.waiter !== null) {
        const w = this.waiter;
        this.waiter = null;
        w.resolve(m);
      } else this.queue.push(m);
    });
    t.onClose((code, reason) => {
      this.closedWith = { code, reason };
      if (this.closeForward !== null) this.closeForward(code, reason);
      else if (this.waiter !== null) {
        const w = this.waiter;
        this.waiter = null;
        w.reject(new TransportError(reason || "the connection closed", code));
      }
    });
  }

  next(timeoutMs: number): Promise<Uint8Array> {
    const held = this.queue.shift();
    if (held !== undefined) return Promise.resolve(held);
    if (this.closedWith !== null) {
      return Promise.reject(
        new TransportError(this.closedWith.reason || "the connection closed", this.closedWith.code),
      );
    }
    return new Promise<Uint8Array>((resolve, reject) => {
      const timer = setTimeout(() => {
        this.waiter = null;
        reject(new TransportError("the computer did not answer in time", 1006));
      }, timeoutMs);
      this.waiter = {
        resolve: (m) => {
          clearTimeout(timer);
          resolve(m);
        },
        reject: (e) => {
          clearTimeout(timer);
          reject(e);
        },
      };
    });
  }

  detach(): void {
    this.forward = () => undefined;
    this.closeForward = () => undefined;
  }

  handoff(
    onMessage: (m: Uint8Array) => void,
    onClose: (code: number, reason: string) => void,
  ): void {
    this.forward = onMessage;
    this.closeForward = onClose;
    const held = this.queue;
    this.queue = [];
    for (const m of held) onMessage(m);
    if (this.closedWith !== null) onClose(this.closedWith.code, this.closedWith.reason);
  }
}
