// A message pipe to one computer: a relay lane or a direct socket. The Noise
// session above it neither knows nor cares which. Messages are whole Noise
// messages; the pipe preserves their boundaries and order.

export interface Transport {
  send(msg: Uint8Array): void;
  // Handlers may be set after messages have already arrived: the pipe holds
  // them until a handler exists.
  onMessage(cb: (msg: Uint8Array) => void): void;
  onClose(cb: (code: number, reason: string) => void): void;
  close(code?: number, reason?: string): void;
}

// The part of WebSocket the transports use, so tests can supply a fake.
export interface SocketLike {
  binaryType: string;
  readyState: number;
  onopen: ((ev: unknown) => void) | null;
  onmessage: ((ev: { data: unknown }) => void) | null;
  onclose: ((ev: { code: number; reason: string }) => void) | null;
  onerror: ((ev: unknown) => void) | null;
  send(data: string | ArrayBufferLike | ArrayBufferView): void;
  close(code?: number, reason?: string): void;
}

export type SocketFactory = (url: string) => SocketLike;

export const defaultSocketFactory: SocketFactory = (url) => new WebSocket(url) as SocketLike;

// Close codes (contract §5, remote-contract §3.1).
export const CLOSE_BAD_HELLO = 4400;
export const CLOSE_TOKEN_INVALID = 4401;
export const CLOSE_REVOKED = 4403;
export const CLOSE_SLOW_CONSUMER = 4408;
export const CLOSE_RATE_LIMITED = 4429;
// 1006 is what a browser reports when the socket dies without a close frame.
export const CLOSE_ABNORMAL = 1006;

export class TransportError extends Error {
  constructor(
    message: string,
    readonly code: number,
  ) {
    super(message);
    this.name = "TransportError";
  }
}

const OPEN = 1;

class SocketTransport implements Transport {
  private messageCb: ((msg: Uint8Array) => void) | null = null;
  private closeCb: ((code: number, reason: string) => void) | null = null;
  private pending: Uint8Array[] = [];
  private closed: { code: number; reason: string } | null = null;

  constructor(private readonly ws: SocketLike) {
    ws.onmessage = (ev) => {
      const data = ev.data;
      let bytes: Uint8Array | null = null;
      if (data instanceof ArrayBuffer) bytes = new Uint8Array(data);
      else if (ArrayBuffer.isView(data))
        bytes = new Uint8Array(data.buffer, data.byteOffset, data.byteLength);
      if (bytes === null) return; // a text frame after the first is not ours
      if (this.messageCb !== null) this.messageCb(bytes);
      else this.pending.push(bytes);
    };
    ws.onclose = (ev) => this.finish(ev.code, ev.reason);
    ws.onerror = () => {
      // The close event follows with the code; nothing to add here.
    };
  }

  private finish(code: number, reason: string): void {
    if (this.closed !== null) return;
    this.closed = { code, reason };
    this.closeCb?.(code, reason);
  }

  send(msg: Uint8Array): void {
    if (this.closed !== null || this.ws.readyState !== OPEN) {
      throw new TransportError("the connection is closed", CLOSE_ABNORMAL);
    }
    this.ws.send(msg);
  }

  onMessage(cb: (msg: Uint8Array) => void): void {
    this.messageCb = cb;
    const held = this.pending;
    this.pending = [];
    for (const m of held) cb(m);
  }

  onClose(cb: (code: number, reason: string) => void): void {
    this.closeCb = cb;
    if (this.closed !== null) cb(this.closed.code, this.closed.reason);
  }

  close(code = 1000, reason = ""): void {
    try {
      this.ws.close(code, reason);
    } catch {
      // Already closing.
    }
    this.finish(code, reason);
  }
}

export interface OpenOptions {
  factory?: SocketFactory;
  // The first text frame, sent as soon as the socket opens (a relay token or a
  // pair id; never in the URL).
  firstFrame?: string;
  timeoutMs?: number;
}

// Opens a WebSocket and resolves once it is open and the first frame is sent.
export function openSocket(url: string, opts: OpenOptions = {}): Promise<Transport> {
  const factory = opts.factory ?? defaultSocketFactory;
  const timeoutMs = opts.timeoutMs ?? 10_000;
  return new Promise<Transport>((resolve, reject) => {
    let ws: SocketLike;
    try {
      ws = factory(url);
    } catch (err) {
      reject(err instanceof Error ? err : new Error("could not open the connection"));
      return;
    }
    ws.binaryType = "arraybuffer";
    let settled = false;
    const timer = setTimeout(() => {
      if (settled) return;
      settled = true;
      try {
        ws.close();
      } catch {
        // Nothing to close yet.
      }
      reject(new TransportError("the connection timed out", CLOSE_ABNORMAL));
    }, timeoutMs);
    ws.onopen = () => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      try {
        if (opts.firstFrame !== undefined) ws.send(opts.firstFrame);
      } catch (err) {
        reject(err instanceof Error ? err : new Error("could not send"));
        return;
      }
      resolve(new SocketTransport(ws));
    };
    ws.onclose = (ev) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      reject(new TransportError(ev.reason || "the connection was refused", ev.code));
    };
    ws.onerror = () => {
      // onclose follows.
    };
  });
}
