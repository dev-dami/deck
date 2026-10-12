import {
  CLOSE_BAD_HELLO,
  CLOSE_RATE_LIMITED,
  CLOSE_REVOKED,
  CLOSE_TOKEN_INVALID,
  Code,
  decodeResult,
  MAX_IN_FLIGHT,
  type MethodName,
  type MethodTable,
  PROTOCOL,
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

// Why the client stopped for good (§6, R1): the token is gone, or this page
// and the server speak different protocol versions.
export type StopReason = "token_invalid" | "revoked" | "protocol" | "no_token";

export class RpcError extends Error {
  readonly code: string;
  constructor(code: string, msg: string) {
    super(msg);
    this.name = "RpcError";
    this.code = code;
  }
}

// Failures where the request may or may not have reached the server, so a
// retry is only safe with an idempotency key.
export function isTransient(e: unknown): boolean {
  return e instanceof RpcError && (e.code === "timeout" || e.code === "disconnected");
}

export interface WelcomeInfo {
  welcome: WelcomeFrame;
  // True when the client must refetch everything: first connect, a new epoch
  // or the server declined to replay.
  resync: boolean;
}

export interface RpcOptions {
  url: string;
  getToken: () => string | null;
  // The session the phone has open, re-sent as hello.resume.focus (R11).
  getFocus?: () => string | null;
  socketFactory: SocketFactory;
  clock?: Clock;
  random?: () => number;
  newId?: () => string;
  requestTimeoutMs?: number;
  helloTimeoutMs?: number;
  backoffBaseMs?: number;
  backoffMaxMs?: number;
  maxInFlight?: number;
  onStatus?: (s: ConnStatus) => void;
  onWelcome?: (w: WelcomeInfo) => void;
  onResync?: () => void;
  onEvent?: (seq: number, e: string, p: unknown) => void;
  onStopped?: (reason: StopReason) => void;
  warn?: (msg: string) => void;
}

const defaultClock: Clock = {
  setTimeout: (fn, ms) => setTimeout(fn, ms),
  clearTimeout: (h) => clearTimeout(h as ReturnType<typeof setTimeout>),
};

export const REQUEST_TIMEOUT_MS = 15_000;
export const BACKOFF_MAX_MS = 60_000;

interface Pending {
  resolve: (r: unknown) => void;
  reject: (e: Error) => void;
  timer: TimerHandle;
}

interface Queued {
  id: string;
  frame: string;
  pending: Omit<Pending, "timer">;
  timeoutMs: number;
}

export function backoffDelay(attempt: number, base: number, max: number, rand: number): number {
  const ceiling = Math.min(max, base * 2 ** Math.min(attempt, 20));
  // Half fixed, half jittered: keeps a floor so a flapping server is not hammered.
  return Math.round(ceiling / 2 + (ceiling / 2) * rand);
}

// A 4429 close carries the seconds to wait in its reason (§3.1).
export function retryAfterMs(reason: string): number | null {
  const n = Number.parseInt(reason.trim(), 10);
  return Number.isFinite(n) && n >= 0 ? n * 1000 : null;
}

// The error code a close reason starts with, if any ("token_invalid", "rate_limited: ...").
export function closeReasonCode(reason: string): string {
  const m = /^[a-z_]+/.exec(reason.trim());
  return m?.[0] ?? "";
}

export class RpcClient {
  private readonly o: RpcOptions;
  private readonly clock: Clock;
  private readonly requestTimeoutMs: number;
  private readonly helloTimeoutMs: number;
  private readonly base: number;
  private readonly max: number;
  private readonly maxInFlight: number;

  private sock: SocketLike | null = null;
  private state: ConnStatus = "idle";
  private attempt = 0;
  private reconnectTimer: TimerHandle | null = null;
  private helloTimer: TimerHandle | null = null;
  private pending = new Map<string, Pending>();
  private queue: Queued[] = [];
  private nextId = 1;
  private epoch: string | null = null;
  private lastSeq = 0;
  private sentResume = false;
  private waiters: Array<() => void> = [];
  private generation = 0;

  constructor(o: RpcOptions) {
    this.o = o;
    this.clock = o.clock ?? defaultClock;
    this.requestTimeoutMs = o.requestTimeoutMs ?? REQUEST_TIMEOUT_MS;
    this.helloTimeoutMs = o.helloTimeoutMs ?? 10_000;
    this.base = o.backoffBaseMs ?? 500;
    this.max = Math.min(o.backoffMaxMs ?? BACKOFF_MAX_MS, BACKOFF_MAX_MS);
    this.maxInFlight = o.maxInFlight ?? MAX_IN_FLIGHT;
  }

  get status(): ConnStatus {
    return this.state;
  }

  get resumeCursor(): Resume | null {
    if (this.epoch === null) return null;
    const focus = this.o.getFocus?.() ?? null;
    const r: Resume = { epoch: this.epoch, seq: this.lastSeq };
    if (focus !== null && focus !== "") r.focus = focus;
    return r;
  }

  get inFlight(): number {
    return this.pending.size;
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
      // push.status is the cheapest round trip the protocol has.
      this.call("push.status", {}, { timeoutMs: 4000 }).catch(() => this.reconnectNow());
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
    const id = String(this.nextId++);
    const timeoutMs = opts.timeoutMs ?? this.requestTimeoutMs;
    const frame = JSON.stringify({ t: "req", id, m, p: params });
    return new Promise<unknown>((resolve, reject) => {
      const q: Queued = { id, frame, pending: { resolve, reject }, timeoutMs };
      // At most MAX_IN_FLIGHT requests per connection (§3.6); the rest wait
      // their turn instead of being refused by the server.
      if (this.pending.size < this.maxInFlight) this.dispatch(q);
      else this.queue.push(q);
    }).then((r) => {
      const decoded = decodeResult(m, r);
      if (decoded === null) {
        this.o.warn?.(`dropped malformed ${m} result`);
        throw new RpcError("bad_response", `unexpected ${m} response`);
      }
      return decoded;
    });
  }

  private dispatch(q: Queued): void {
    const timer = this.clock.setTimeout(() => {
      this.pending.delete(q.id);
      q.pending.reject(new RpcError("timeout", "Deck did not answer in time"));
      this.drain();
    }, q.timeoutMs);
    this.pending.set(q.id, { ...q.pending, timer });
    try {
      this.sock?.send(q.frame);
    } catch (err) {
      this.clock.clearTimeout(timer);
      this.pending.delete(q.id);
      q.pending.reject(
        new RpcError("disconnected", err instanceof Error ? err.message : "send failed"),
      );
      this.drain();
    }
  }

  private drain(): void {
    while (this.state === "ready" && this.pending.size < this.maxInFlight) {
      const next = this.queue.shift();
      if (next === undefined) return;
      this.dispatch(next);
    }
  }

  // chat.send carries an idempotency key minted once per message and reused
  // across up to `retries` retries, so a retry after a timeout or a dropped
  // connection cannot post the same prompt twice.
  async sendChat(
    req: Omit<MethodTable["chat.send"]["req"], "idempotency_key">,
    opts: { retries?: number } = {},
  ): Promise<MethodTable["chat.send"]["res"]> {
    const key = (this.o.newId ?? randomId)();
    const retries = opts.retries ?? 3;
    let lastErr: unknown = null;
    for (let i = 0; i <= retries; i++) {
      try {
        return await this.call(
          "chat.send",
          { ...req, idempotency_key: key },
          { timeoutMs: 30_000 },
        );
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
      this.o.onStopped?.("no_token");
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
      const hello: Record<string, unknown> = { t: "hello", v: PROTOCOL, token };
      const resume = this.resumeCursor;
      this.sentResume = resume !== null;
      if (resume !== null) hello["resume"] = resume;
      sock.send(JSON.stringify(hello));
      this.helloTimer = this.clock.setTimeout(() => {
        if (gen === this.generation) this.handleClose(gen, 4000, "");
      }, this.helloTimeoutMs);
    };
    sock.onmessage = (ev) => {
      if (gen === this.generation) this.handleMessage(ev.data);
    };
    sock.onclose = (ev) => this.handleClose(gen, ev.code, ev.reason);
    sock.onerror = () => {
      // The close event that follows carries the code; nothing to do here.
    };
  }

  private handleMessage(data: unknown): void {
    if (typeof data !== "string") return;
    const f = parseServerFrame(data);
    if (f === null) {
      this.o.warn?.("dropped a frame that failed validation");
      return;
    }
    switch (f.t) {
      case "welcome": {
        this.clearHelloTimer();
        const resumed = this.sentResume && f.epoch === this.epoch;
        const resync = !(resumed && f.replay);
        if (resync) this.lastSeq = 0;
        this.epoch = f.epoch;
        this.attempt = 0;
        this.setStatus("ready");
        this.o.onWelcome?.({ welcome: f, resync });
        this.drain();
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
        this.drain();
        return;
      }
    }
  }

  private handleClose(gen: number, code: number, reason: string): void {
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
    switch (code) {
      case CLOSE_REVOKED:
        this.finish("revoked");
        return;
      case CLOSE_TOKEN_INVALID: {
        // R1: the token is forgotten only when the reason says token_invalid.
        // Any other reason on 4401, including an empty one (a proxy may strip
        // close reasons), is treated as a transient refusal: back off and keep
        // the token, the same path as a 4429 without a number.
        if (closeReasonCode(reason) === Code.TokenInvalid) {
          this.finish("token_invalid");
          return;
        }
        this.scheduleReconnect();
        return;
      }
      case CLOSE_BAD_HELLO:
        this.finish("protocol");
        return;
      case CLOSE_RATE_LIMITED:
        this.scheduleReconnect(retryAfterMs(reason));
        return;
      default:
        this.scheduleReconnect();
    }
  }

  private finish(reason: StopReason): void {
    this.teardown("stopped");
    this.o.onStopped?.(reason);
  }

  // Wait (jittered, capped at BACKOFF_MAX_MS) before the next attempt; a
  // server-given wait (4429) is a floor, never a ceiling, on the backoff.
  private scheduleReconnect(floorMs: number | null = null): void {
    this.setStatus("reconnecting");
    const rand = (this.o.random ?? Math.random)();
    let delay = backoffDelay(this.attempt, this.base, this.max, rand);
    if (floorMs !== null) {
      const floor = Math.min(floorMs, BACKOFF_MAX_MS);
      const jitter = Math.round(floor * 0.25 * rand);
      delay = Math.min(BACKOFF_MAX_MS, Math.max(delay, floor + jitter));
    }
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
    // Waiters woken by a stop must observe the stopped state, not hang.
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
    const queued = this.queue;
    this.queue = [];
    for (const p of all) {
      this.clock.clearTimeout(p.timer);
      p.reject(new RpcError("disconnected", "connection lost"));
    }
    for (const q of queued) q.pending.reject(new RpcError("disconnected", "connection lost"));
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
