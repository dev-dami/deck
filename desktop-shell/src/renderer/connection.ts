import type { KV } from "@web/auth";
import { Controller } from "@web/controller";
import { documentTarget, type ThemeTarget } from "@web/theme";
import { type DeckBridge, type EngineStop, type LocalDeck, TOKEN_PLACEHOLDER } from "../shared/bridge";
import { BridgeSocket } from "./bridgeSocket";
import type { DeckClient } from "./client";
import { RpcDeckClient } from "./rpcClient";

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

function themeTarget(): ThemeTarget | null {
  return typeof document === "undefined" ? null : documentTarget(document);
}

// A paired Deck over protocol v2, with the phone client's controller: the
// RPC client, reconnects, focus, the reducer and every method call. Only
// the socket (through main) and the token store differ.
export function connect(bridge: DeckBridge, serverId: string): DeckClient {
  const c = new Controller({
    storage: placeholderStorage(serverId),
    fetchFn: () => Promise.reject(new Error("main pairs this app")),
    socketFactory: (id) => new BridgeSocket(bridge, id),
    wsUrl: serverId,
    deviceName: "",
    vibrate: () => {},
    theme: themeTarget(),
  });
  c.start(null);
  return c;
}

// A Deck on this machine that main runs with deck serve --rpc. onEngine
// hears when its engine stops by itself (and null once it runs again).
export function openLocal(bridge: DeckBridge, deck: LocalDeck, onEngine: (stop: EngineStop | null) => void = () => {}): DeckClient {
  const c = new RpcDeckClient(bridge, deck, themeTarget(), onEngine);
  c.start();
  return c;
}
