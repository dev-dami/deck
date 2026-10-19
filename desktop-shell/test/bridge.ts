// The real main-process host (pairing, pinning, the socket relay) without
// Electron: tests call it in-process, and the e2e tests serve it to a page.
import { createHost } from "../src/main/host";
import { LinkHost } from "../src/main/link";
import { type Sealer, ServerStore } from "../src/main/servers";
import type { DeckBridge, DeckHost, SocketEvent } from "../src/shared/bridge";

// No keychain: tokens stay in memory, as on a Linux box without one.
const noKeychain: Sealer = {
  available: () => false,
  seal: () => {
    throw new Error("no keychain");
  },
  open: () => {
    throw new Error("no keychain");
  },
};

export function testHost(emit: (id: number, ev: SocketEvent) => void): {
  host: DeckHost;
  store: ServerStore;
  links: LinkHost;
} {
  const store = new ServerStore(null, noKeychain);
  const links = new LinkHost(store, emit);
  const host = createHost({
    store,
    links,
    local: null,
    deckPath: null,
    deviceName: "Deck desktop (test)",
    pickFolder: async () => null,
  });
  return { host, store, links };
}

// A bridge as the preload builds it, wired straight to the host.
export function inProcessBridge(): DeckBridge & { store: ServerStore; links: LinkHost } {
  const listeners = new Set<(id: number, ev: SocketEvent) => void>();
  const { host, store, links } = testHost((id, ev) => {
    for (const l of listeners) l(id, ev);
  });
  return {
    ...host,
    store,
    links,
    onSocket: (fn) => {
      listeners.add(fn);
      return () => {
        listeners.delete(fn);
      };
    },
  };
}
