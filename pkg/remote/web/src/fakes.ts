import { isRecord } from "./protocol";
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

  drop(code = 1006, reason = ""): void {
    this.onclose?.({ code, reason });
  }

  last(): Record<string, unknown> {
    const v = this.sent[this.sent.length - 1];
    if (!isRecord(v)) throw new Error("nothing sent");
    return v;
  }

  // Every req frame for method m, oldest first.
  requests(m: string): Record<string, unknown>[] {
    return this.sent.filter(
      (f): f is Record<string, unknown> => isRecord(f) && f["t"] === "req" && f["m"] === m,
    );
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

  // When the next timer fires, relative to now; null when none is set.
  nextDelay(): number | null {
    if (this.timers.length === 0) return null;
    return Math.min(...this.timers.map((t) => t.at)) - this.now;
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

// A welcome frame shaped like frame_welcome.json, with overrides.
export function welcomeFrame(over: {
  epoch?: string;
  replay?: boolean;
  allow_yolo?: boolean;
  server_id?: string;
  theme?: unknown;
}): Record<string, unknown> {
  return {
    t: "welcome",
    epoch: over.epoch ?? "e1",
    replay: over.replay ?? false,
    server: {
      version: "0.12.0",
      project: "deck",
      kinds: ["claude", "codex"],
      allow_yolo: over.allow_yolo ?? false,
      features: ["images", "edit", "changes"],
      server_id: over.server_id ?? "srv_1",
      protocol: 2,
      headless: false,
      tunnel: "",
      theme: over.theme ?? {
        palette: { bg_base: "#111416", fg_base: "#e6e8ea", primary: "#5ec9a8" },
        kinds: { claude: { tint: "#d97757", mark: "✻" } },
      },
    },
    device: { id: "dev1", name: "Pixel", expires_at: "2027-01-10T09:30:00Z" },
  };
}

export async function flush(rounds = 10): Promise<void> {
  for (let i = 0; i < rounds; i++) await Promise.resolve();
}
