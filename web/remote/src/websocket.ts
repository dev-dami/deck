import type { SocketLike } from "./rpc";

// Adapts the DOM WebSocket to the narrow SocketLike the RPC client uses, so the
// client stays testable against a fake and never touches DOM event types.
export class BrowserSocket implements SocketLike {
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  onerror: (() => void) | null = null;
  private readonly ws: WebSocket;

  constructor(url: string) {
    this.ws = new WebSocket(url);
    this.ws.addEventListener("open", () => this.onopen?.());
    this.ws.addEventListener("message", (ev) => this.onmessage?.({ data: ev.data }));
    this.ws.addEventListener("close", (ev) => this.onclose?.({ code: ev.code, reason: ev.reason }));
    this.ws.addEventListener("error", () => this.onerror?.());
  }

  send(data: string): void {
    this.ws.send(data);
  }

  close(code?: number, reason?: string): void {
    this.ws.close(code, reason);
  }
}
