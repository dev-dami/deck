import type { KV } from "@web/auth";
import { Controller } from "@web/controller";
import { documentTarget } from "@web/theme";
import { type DeckBridge, TOKEN_PLACEHOLDER } from "../shared/bridge";
import { BridgeSocket } from "./bridgeSocket";

// The shared controller keeps its token in a KV store. Here that store holds
// only a placeholder: main keeps the real token and writes it into the
// hello. Clearing it (token_invalid, revoked) sends the window back to the
// connect screen; main forgets the real token on the same close.
function placeholderStorage(serverId: string): KV {
  const m = new Map<string, string>([
    ["deck.remote.server", serverId],
    [`deck.remote.token.${serverId}`, TOKEN_PLACEHOLDER],
  ]);
  return {
    get length() {
      return m.size;
    },
    key: (i) => [...m.keys()][i] ?? null,
    getItem: (k) => m.get(k) ?? null,
    setItem: (k, v) => {
      m.set(k, v);
    },
    removeItem: (k) => {
      m.delete(k);
    },
  };
}

// A controller for one paired server, from the phone client's code: the RPC
// client, reconnects, focus, the reducer and every method call. Only the
// socket (through main) and the token store differ.
export function connect(bridge: DeckBridge, serverId: string): Controller {
  const c = new Controller({
    storage: placeholderStorage(serverId),
    fetchFn: () => Promise.reject(new Error("main pairs this app")),
    socketFactory: (id) => new BridgeSocket(bridge, id),
    wsUrl: serverId,
    deviceName: "",
    vibrate: () => {},
    theme: typeof document === "undefined" ? null : documentTarget(document),
  });
  c.start(null);
  return c;
}
