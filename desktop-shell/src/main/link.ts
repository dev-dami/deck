import type net from "node:net";
import WebSocket from "ws";
import type { SocketEvent } from "../shared/bridge";
import { authority, connectPinned, originOf, type Target } from "./pin";
import type { ServerStore } from "./servers";

// The close codes after which Deck will never take the token again (§3.5).
const CLOSE_TOKEN_INVALID = 4401;
const CLOSE_REVOKED = 4403;
const CLOSE_BAD_HELLO = 4400;
// The frame cap after the hello (§3.6), with room for the envelope.
const MAX_PAYLOAD = (12 << 20) + 64 * 1024;

type Emit = (id: number, ev: SocketEvent) => void;
type Connect = (t: Target) => Promise<net.Socket>;

interface Conn {
  serverId: string;
  token: string;
  ws: WebSocket | null;
  closed: boolean;
  helloSent: boolean;
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// The renderer's hello with the real token in place of its placeholder. Only
// v and a well-formed resume cursor are kept from what the renderer sent.
export function sealHello(raw: string, token: string): string | null {
  let f: unknown;
  try {
    f = JSON.parse(raw);
  } catch {
    return null;
  }
  if (!isRecord(f) || f["t"] !== "hello" || f["v"] !== 2) return null;
  const hello: Record<string, unknown> = { t: "hello", v: 2, token };
  const r = f["resume"];
  if (isRecord(r) && typeof r["epoch"] === "string" && Number.isInteger(r["seq"])) {
    const resume: Record<string, unknown> = { epoch: r["epoch"], seq: r["seq"] };
    if (typeof r["focus"] === "string") resume["focus"] = r["focus"];
    hello["resume"] = resume;
  }
  return JSON.stringify(hello);
}

// After the hello the renderer may send requests only.
export function isRequest(raw: string): boolean {
  try {
    const f: unknown = JSON.parse(raw);
    return isRecord(f) && f["t"] === "req" && typeof f["id"] === "string" && typeof f["m"] === "string";
  } catch {
    return false;
  }
}

// ws accepts 1000 and the 3000-4999 range from an application.
function closeCode(code: number): number {
  return code === 1000 || (code >= 3000 && code <= 4999) ? code : 1000;
}

// Opens Deck's WebSocket for the renderer and relays frames both ways. The
// token never leaves this class: the renderer's hello carries a placeholder
// and this class writes the real token in. A close that ends the pairing
// (token_invalid, revoked) also forgets the stored token.
export class LinkHost {
  private next = 1;
  private readonly conns = new Map<number, Conn>();

  constructor(
    private readonly store: ServerStore,
    private readonly emit: Emit,
    private readonly connect: Connect = connectPinned,
  ) {}

  open(serverId: string): number {
    const id = this.next++;
    const entry = this.store.get(serverId);
    if (entry === null) {
      // No token for that server: tell the renderer as Deck would.
      queueMicrotask(() =>
        this.emit(id, { type: "close", code: CLOSE_TOKEN_INVALID, reason: "token_invalid" }),
      );
      return id;
    }
    const conn: Conn = { serverId, token: entry.token, ws: null, closed: false, helloSent: false };
    this.conns.set(id, conn);
    void this.dial(id, conn, entry.server);
    return id;
  }

  send(id: number, data: string): void {
    const conn = this.conns.get(id);
    const ws = conn?.ws;
    if (conn === undefined || ws === null || ws === undefined || ws.readyState !== WebSocket.OPEN) {
      return;
    }
    let frame: string | null = data;
    if (!conn.helloSent) {
      frame = sealHello(data, conn.token);
      conn.helloSent = frame !== null;
    } else if (!isRequest(data)) {
      frame = null;
    }
    if (frame === null) {
      ws.close(CLOSE_BAD_HELLO, "bad frame from the window");
      return;
    }
    ws.send(frame);
  }

  close(id: number, code: number, reason: string): void {
    const conn = this.conns.get(id);
    if (conn === undefined) return;
    conn.closed = true;
    this.conns.delete(id);
    conn.ws?.close(closeCode(code), reason.slice(0, 120));
  }

  closeAll(): void {
    for (const id of [...this.conns.keys()]) this.close(id, 1000, "");
  }

  private async dial(id: number, conn: Conn, t: Target): Promise<void> {
    let sock: net.Socket;
    try {
      sock = await this.connect(t);
    } catch {
      this.finish(id, conn, 1006, "");
      return;
    }
    if (conn.closed) {
      sock.destroy();
      return;
    }
    const ws = new WebSocket(`${t.tls ? "wss" : "ws"}://${authority(t)}/api/v1/ws`, {
      origin: originOf(t),
      createConnection: () => sock,
      maxPayload: MAX_PAYLOAD,
      handshakeTimeout: 10_000,
      perMessageDeflate: false,
    });
    conn.ws = ws;
    ws.on("open", () => this.emit(id, { type: "open" }));
    ws.on("message", (data, binary) => {
      if (!binary) this.emit(id, { type: "message", data: data.toString() });
    });
    // An error is always followed by close, which carries the code.
    ws.on("error", () => {});
    ws.on("close", (code, reason) => this.finish(id, conn, code, reason.toString()));
  }

  private finish(id: number, conn: Conn, code: number, reason: string): void {
    if (code === CLOSE_REVOKED || (code === CLOSE_TOKEN_INVALID && reason.startsWith("token_invalid"))) {
      this.store.remove(conn.serverId);
    }
    if (conn.closed) return;
    conn.closed = true;
    this.conns.delete(id);
    this.emit(id, { type: "close", code, reason });
  }
}
