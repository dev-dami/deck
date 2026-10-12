import { isRecord } from "./protocol";
import type { PushEnv, PushPermission } from "./push";
import type { BrowserSub, PushBrowser } from "./push-client";
import type { Clock, SocketLike, TimerHandle } from "./rpc";

// Test doubles shared by rpc and controller tests. Not imported by app code.

export class FakeSocket implements SocketLike {
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  sent: unknown[] = [];
  closed = false;
  readonly url: string;

  constructor(url: string) {
    this.url = url;
  }

  send(data: string): void {
    this.sent.push(JSON.parse(data));
  }

  close(): void {
    this.closed = true;
  }

  // ---- test controls
  open(): void {
    this.onopen?.();
  }

  receive(frame: unknown): void {
    this.onmessage?.({ data: typeof frame === "string" ? frame : JSON.stringify(frame) });
  }

  drop(code = 1006): void {
    this.onclose?.({ code, reason: "" });
  }

  last(): Record<string, unknown> {
    const v = this.sent[this.sent.length - 1];
    if (!isRecord(v)) throw new Error("nothing sent");
    return v;
  }
}

interface Timer {
  at: number;
  fn: () => void;
  id: number;
}

export class FakeClock implements Clock {
  now = 0;
  private timers: Timer[] = [];
  private seq = 0;

  setTimeout(fn: () => void, ms: number): TimerHandle {
    const id = ++this.seq;
    this.timers.push({ at: this.now + ms, fn, id });
    return id;
  }

  clearTimeout(h: TimerHandle): void {
    this.timers = this.timers.filter((t) => t.id !== h);
  }

  pending(): number {
    return this.timers.length;
  }

  advance(ms: number): void {
    const end = this.now + ms;
    for (;;) {
      const due = this.timers.filter((t) => t.at <= end).sort((a, b) => a.at - b.at || a.id - b.id);
      const next = due[0];
      if (next === undefined) break;
      this.timers = this.timers.filter((t) => t.id !== next.id);
      this.now = next.at;
      next.fn();
    }
    this.now = end;
  }
}

export class SocketLog {
  sockets: FakeSocket[] = [];
  factory = (url: string): FakeSocket => {
    const s = new FakeSocket(url);
    this.sockets.push(s);
    return s;
  };

  get latest(): FakeSocket {
    const s = this.sockets[this.sockets.length - 1];
    if (s === undefined) throw new Error("no socket was created");
    return s;
  }
}

export class FakeSub implements BrowserSub {
  unsubscribed = false;
  readonly json: unknown;

  constructor(
    readonly endpoint: string,
    readonly applicationServerKey: ArrayBuffer | null,
  ) {
    this.json = { endpoint, keys: { p256dh: `p-${endpoint}`, auth: `a-${endpoint}` } };
  }

  async unsubscribe(): Promise<boolean> {
    this.unsubscribed = true;
    return true;
  }
}

export class FakePushBrowser implements PushBrowser {
  environment: PushEnv = {
    secure: true,
    serviceWorker: true,
    pushManager: true,
    notification: true,
    ios: false,
    standalone: false,
  };
  perm: PushPermission = "default";
  // What the permission prompt will answer.
  answer: PushPermission = "granted";
  sub: FakeSub | null = null;
  prompts = 0;
  made = 0;

  env(): PushEnv {
    return this.environment;
  }

  permission(): PushPermission {
    return this.perm;
  }

  async requestPermission(): Promise<PushPermission> {
    this.prompts++;
    this.perm = this.answer;
    return this.perm;
  }

  async current(): Promise<BrowserSub | null> {
    return this.sub?.unsubscribed === false ? this.sub : null;
  }

  async subscribe(key: Uint8Array<ArrayBuffer>): Promise<BrowserSub> {
    this.made++;
    this.sub = new FakeSub(`https://push.example/${this.made}`, key.buffer);
    return this.sub;
  }
}
