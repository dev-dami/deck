import type { SocketLike } from "./rpc";

// Adapts the DOM WebSocket to the narrow SocketLike the RPC client uses, so the
// client stays testable against a fake and never touches DOM event types.
export class BrowserSocket implements SocketLike {
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  private readonly ws: WebSocket;

  constructor(url: string) {
    this.ws = new WebSocket(url);
    this.ws.addEventListener("open", () => this.onopen?.());
    this.ws.addEventListener("message", (ev) => this.onmessage?.({ data: ev.data }));
    // An error is always followed by a close event, which carries the code.
    this.ws.addEventListener("close", (ev) => this.onclose?.({ code: ev.code, reason: ev.reason }));
  }

  send(data: string): void {
    this.ws.send(data);
  }

  close(code?: number, reason?: string): void {
    this.ws.close(code, reason);
  }
}
