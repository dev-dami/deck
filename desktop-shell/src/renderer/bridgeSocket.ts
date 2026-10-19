import type { SocketLike } from "@web/rpc";
import type { DeckBridge, SocketEvent } from "../shared/bridge";

// A SocketLike over the bridge: the shared RpcClient drives it exactly as it
// drives a browser WebSocket, while main owns the real socket and the token.
export class BridgeSocket implements SocketLike {
  onopen: (() => void) | null = null;
  onmessage: ((ev: { data: unknown }) => void) | null = null;
  onclose: ((ev: { code: number; reason: string }) => void) | null = null;
  private id: number | null = null;
  private closed = false;
  private readonly off: () => void;

  constructor(
    private readonly bridge: DeckBridge,
    serverId: string,
  ) {
    // Main may emit an event for this socket before the open call returns
    // its id; those wait here until the id is known.
    let early: Array<[number, SocketEvent]> | null = [];
    this.off = bridge.onSocket((id, ev) => {
      if (early !== null) early.push([id, ev]);
      else if (id === this.id) this.deliver(ev);
    });
    bridge.socketOpen(serverId).then(
      (id) => {
        this.id = id;
        const waiting = early ?? [];
        early = null;
        if (this.closed) void bridge.socketClose(id, 1000, "");
        for (const [eid, ev] of waiting) if (eid === id) this.deliver(ev);
      },
      () => this.deliver({ type: "close", code: 1006, reason: "" }),
    );
  }

  send(data: string): void {
    if (this.id !== null && !this.closed) void this.bridge.socketSend(this.id, data);
  }

  close(code = 1000, reason = ""): void {
    if (this.closed) return;
    this.closed = true;
    this.off();
    if (this.id !== null) void this.bridge.socketClose(this.id, code, reason);
  }

  private deliver(ev: SocketEvent): void {
    if (this.closed) return;
    switch (ev.type) {
      case "open":
        this.onopen?.();
        return;
      case "message":
        this.onmessage?.({ data: ev.data });
        return;
      case "close":
        this.closed = true;
        this.off();
        this.onclose?.({ code: ev.code, reason: ev.reason });
        return;
    }
  }
}
