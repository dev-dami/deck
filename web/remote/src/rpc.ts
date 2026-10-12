import {
  CLOSE_REVOKED,
  CLOSE_UNAUTHORIZED,
  DECODERS,
  type MethodName,
  type MethodTable,
  PROTOCOL_VERSION,
  parseServerFrame,
  type Resume,
  type WelcomeFrame,
} from "./protocol";

export interface SocketLike {
  onopen: (() => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: { code: number; reason: string }) => void) | null;
  onerror: (() => void) | null;
  send(data: string): void;
  close(code?: number, reason?: string): void;
}

export type SocketFactory = (url: string) => SocketLike;
export type TimerHandle = unknown;

export interface Clock {
  setTimeout(fn: () => void, ms: number): TimerHandle;
  clearTimeout(h: TimerHandle): void;
}

export type ConnStatus = "idle" | "connecting" | "ready" | "reconnecting" | "stopped";
export type StopReason = "unauthorized" | "revoked";

export class RpcError extends Error {
  readonly code: string;
  constructor(code: string, msg: string) {
    super(msg);
    this.name = "RpcError";
    this.code = code;
  }
}

// Failures where the request may or may not have reached the server, so a
// retry is only safe for idempotent calls.
export function isTransient(e: unknown): boolean {
  return e instanceof RpcError && (e.code === "timeout" || e.code === "disconnected");
}

export interface WelcomeInfo {
  welcome: WelcomeFrame;
  // True when the client must refetch snapshots: first connect, epoch change or
  // the server declined to replay.
  resync: boolean;
}

export interface RpcOptions {
  url: string;
  getToken: () => string | null;
  socketFactory: SocketFactory;
  clock?: Clock;
  random?: () => number;
  newId?: () => string;
  requestTimeoutMs?: number;
  helloTimeoutMs?: number;
  backoffBaseMs?: number;
  backoffMaxMs?: number;
  onStatus?: (s: ConnStatus) => void;
  onWelcome?: (w: WelcomeInfo) => void;
  onResync?: () => void;
  onEvent?: (seq: number, e: string, p: unknown) => void;
  onStopped?: (reason: StopReason) => void;
}

const defaultClock: Clock = {
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

interface Pending {
  resolve: (r: unknown) => void;
  reject: (e: Error) => void;
  timer: TimerHandle;
}

export function backoffDelay(attempt: number, base: number, max: number, rand: number): number {
  const ceiling = Math.min(max, base * 2 ** Math.min(attempt, 20));
  // Half fixed, half jittered: keeps a floor so a flapping server is not hammered.
  return Math.round(ceiling / 2 + (ceiling / 2) * rand);
}

export class RpcClient {
  private readonly o: RpcOptions;
  private readonly clock: Clock;
  private readonly requestTimeoutMs: number;
  private readonly helloTimeoutMs: number;
  private readonly base: number;
  private readonly max: number;

  private sock: SocketLike | null = null;
  private state: ConnStatus = "idle";
  private attempt = 0;
  private reconnectTimer: TimerHandle | null = null;
  private helloTimer: TimerHandle | null = null;
  private pending = new Map<string, Pending>();
  private nextId = 1;
  private epoch: string | null = null;
  private lastSeq = 0;
  private sentResume = false;
  private waiters: Array<() => void> = [];
  private generation = 0;

  constructor(o: RpcOptions) {
    this.o = o;
    this.clock = o.clock ?? defaultClock;
    this.requestTimeoutMs = o.requestTimeoutMs ?? 15_000;
    this.helloTimeoutMs = o.helloTimeoutMs ?? 10_000;
    this.base = o.backoffBaseMs ?? 500;
    this.max = o.backoffMaxMs ?? 30_000;
  }

  get status(): ConnStatus {
    return this.state;
  }

  get resumeCursor(): Resume | null {
    return this.epoch === null ? null : { epoch: this.epoch, seq: this.lastSeq };
  }

  start(): void {
    if (this.state !== "idle" && this.state !== "stopped") return;
    this.attempt = 0;
    this.open();
  }

  stop(): void {
    this.teardown("stopped");
  }

  // Page came back to the foreground: a mobile browser may have silently killed
  // the socket, so reconnect now instead of waiting out the backoff, and probe a
  // socket that claims to be open.
  notifyVisible(): void {
    if (this.state === "stopped" || this.state === "idle") return;
    if (this.state === "reconnecting") {
      this.reconnectNow();
      return;
    }
    if (this.state === "ready") {
      this.call("mode.get", {}, { timeoutMs: 4000 }).catch(() => this.reconnectNow());
    }
  }

  // Forget the resume cursor so the next hello is a fresh one.
  forgetResume(): void {
    this.epoch = null;
    this.lastSeq = 0;
  }

  reconnectNow(): void {
    if (this.state === "stopped" || this.state === "idle") return;
    this.dropSocket(false);
    this.attempt = 0;
    this.open();
  }

  call<M extends MethodName>(
    m: M,
    params: MethodTable[M]["req"],
    opts: { timeoutMs?: number } = {},
  ): Promise<MethodTable[M]["res"]> {
    if (this.state !== "ready" || this.sock === null) {
      return Promise.reject(new RpcError("offline", "not connected"));
    }
    const id = `c${this.nextId++}`;
    const timeoutMs = opts.timeoutMs ?? this.requestTimeoutMs;
    return new Promise<unknown>((resolve, reject) => {
      const timer = this.clock.setTimeout(() => {
        this.pending.delete(id);
        reject(new RpcError("timeout", `${m} timed out`));
      }, timeoutMs);
      this.pending.set(id, { resolve, reject, timer });
      try {
        this.sock?.send(JSON.stringify({ t: "req", id, m, p: params }));
      } catch (err) {
        this.clock.clearTimeout(timer);
        this.pending.delete(id);
        reject(new RpcError("disconnected", err instanceof Error ? err.message : "send failed"));
      }
    }).then((r) => {
      const decoded = DECODERS[m](r);
      if (!decoded.ok) throw new RpcError("bad_response", `unexpected ${m} response`);
      return decoded.value;
    });
  }

  // chat.send carries an idempotency key so a retry after a timeout or a dropped
  // connection cannot post the same prompt twice.
  async sendChat(
    req: Omit<MethodTable["chat.send"]["req"], "idempotency_key">,
    opts: { retries?: number } = {},
  ): Promise<void> {
    const key = (this.o.newId ?? randomId)();
    const retries = opts.retries ?? 3;
    let lastErr: unknown = null;
    for (let i = 0; i <= retries; i++) {
      try {
        await this.call("chat.send", { ...req, idempotency_key: key }, { timeoutMs: 30_000 });
        return;
      } catch (e) {
        lastErr = e;
        const offline = e instanceof RpcError && e.code === "offline";
        if (!isTransient(e) && !offline) throw e;
        if (i === retries) break;
        await this.whenReady(60_000);
      }
    }
    throw lastErr;
  }

  private whenReady(timeoutMs: number): Promise<void> {
    if (this.state === "ready") return Promise.resolve();
    if (this.state === "stopped" || this.state === "idle") {
      return Promise.reject(new RpcError("offline", "connection stopped"));
    }
    return new Promise<void>((resolve, reject) => {
      const timer = this.clock.setTimeout(() => {
        this.waiters = this.waiters.filter((w) => w !== done);
        reject(new RpcError("offline", "still offline"));
      }, timeoutMs);
      const done = () => {
        this.clock.clearTimeout(timer);
        resolve();
      };
      this.waiters.push(done);
    });
  }

  private setStatus(s: ConnStatus): void {
    if (this.state === s) return;
    this.state = s;
    this.o.onStatus?.(s);
    if (s === "ready") {
      const ws = this.waiters;
      this.waiters = [];
      for (const w of ws) w();
    }
  }

  private open(): void {
    const token = this.o.getToken();
    if (token === null || token === "") {
      this.setStatus("stopped");
      this.o.onStopped?.("unauthorized");
      return;
    }
    this.clearReconnectTimer();
    this.setStatus(this.attempt === 0 && this.epoch === null ? "connecting" : "reconnecting");
    const gen = ++this.generation;
    let sock: SocketLike;
    try {
      sock = this.o.socketFactory(this.o.url);
    } catch {
      this.scheduleReconnect();
      return;
    }
    this.sock = sock;
    sock.onopen = () => {
      if (gen !== this.generation) return;
      const hello: Record<string, unknown> = { t: "hello", v: PROTOCOL_VERSION, token };
      const resume = this.resumeCursor;
      this.sentResume = resume !== null;
      if (resume !== null) hello["resume"] = resume;
      sock.send(JSON.stringify(hello));
      this.helloTimer = this.clock.setTimeout(() => {
        if (gen === this.generation) this.handleClose(gen, 4000);
      }, this.helloTimeoutMs);
    };
    sock.onmessage = (ev) => {
      if (gen === this.generation) this.handleMessage(ev.data);
    };
    sock.onclose = (ev) => this.handleClose(gen, ev.code);
    sock.onerror = () => {
      // The close event that follows carries the code; nothing to do here.
    };
  }

  private handleMessage(data: unknown): void {
    if (typeof data !== "string") return;
    const f = parseServerFrame(data);
    if (f === null) return;
    switch (f.t) {
      case "welcome": {
        this.clearHelloTimer();
        const resumed = this.sentResume && f.epoch !== undefined && f.epoch === this.epoch;
        const resync = !(resumed && f.replay === true);
        if (resync) this.lastSeq = 0;
        if (f.epoch !== undefined) this.epoch = f.epoch;
        this.attempt = 0;
        this.setStatus("ready");
        this.o.onWelcome?.({ welcome: f, resync });
        return;
      }
      case "resync":
        this.lastSeq = 0;
        this.o.onResync?.();
        return;
      case "ev":
        // Replays can overlap what we already applied.
        if (f.seq <= this.lastSeq) return;
        this.lastSeq = f.seq;
        this.o.onEvent?.(f.seq, f.e, f.p);
        return;
      case "res": {
        const p = this.pending.get(f.id);
        if (p === undefined) return;
        this.pending.delete(f.id);
        this.clock.clearTimeout(p.timer);
        if (f.ok) p.resolve(f.r);
        else p.reject(new RpcError(f.err.code, f.err.msg));
        return;
      }
    }
  }

  private handleClose(gen: number, code: number): void {
    if (gen !== this.generation) return;
    this.clearHelloTimer();
    this.failPending();
    const sock = this.sock;
    this.sock = null;
    if (sock !== null) {
      sock.onopen = null;
      sock.onmessage = null;
      sock.onclose = null;
      sock.onerror = null;
      try {
        sock.close();
      } catch {
        // Already closed; nothing left to release.
      }
    }
    if (code === CLOSE_UNAUTHORIZED || code === CLOSE_REVOKED) {
      this.teardown("stopped");
      this.o.onStopped?.(code === CLOSE_REVOKED ? "revoked" : "unauthorized");
      return;
    }
    this.scheduleReconnect();
  }

  private scheduleReconnect(): void {
    this.setStatus("reconnecting");
    const delay = backoffDelay(this.attempt, this.base, this.max, (this.o.random ?? Math.random)());
    this.attempt++;
    this.clearReconnectTimer();
    this.reconnectTimer = this.clock.setTimeout(() => {
      this.reconnectTimer = null;
      this.open();
    }, delay);
  }

  private teardown(final: ConnStatus): void {
    this.generation++;
    this.clearReconnectTimer();
    this.clearHelloTimer();
    this.dropSocket(true);
    this.failPending();
    const ws = this.waiters;
    this.waiters = [];
    this.setStatus(final);
    // Waiters woke by a stop must observe the stopped state, not hang.
    for (const w of ws) w();
  }

  private dropSocket(final: boolean): void {
    this.generation++;
    this.clearHelloTimer();
    const sock = this.sock;
    this.sock = null;
    this.failPending();
    if (sock === null) return;
    sock.onopen = null;
    sock.onmessage = null;
    sock.onclose = null;
    sock.onerror = null;
    try {
      sock.close(final ? 1000 : 4000);
    } catch {
      // Socket was already closing; the replacement connection supersedes it.
    }
  }

  private failPending(): void {
    const all = [...this.pending.values()];
    this.pending.clear();
    for (const p of all) {
      this.clock.clearTimeout(p.timer);
      p.reject(new RpcError("disconnected", "connection lost"));
    }
  }

  private clearReconnectTimer(): void {
    if (this.reconnectTimer !== null) {
      this.clock.clearTimeout(this.reconnectTimer);
      this.reconnectTimer = null;
    }
  }

  private clearHelloTimer(): void {
    if (this.helloTimer !== null) {
      this.clock.clearTimeout(this.helloTimer);
      this.helloTimer = null;
    }
  }
}

export function randomId(): string {
  const c = globalThis.crypto;
  if (typeof c?.randomUUID === "function") return c.randomUUID();
  const bytes = new Uint8Array(16);
  c.getRandomValues(bytes);
  return Array.from(bytes, (b) => b.toString(16).padStart(2, "0")).join("");
}
