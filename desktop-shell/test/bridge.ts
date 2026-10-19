// The real main-process host (pairing, pinning, the socket relay, local
// Decks over rpc) without Electron: tests call it in-process, and the e2e
// tests serve it to a page.
import { join } from "node:path";
import { loadService, RpcDecks } from "../src/main/decks";
import { createHost } from "../src/main/host";
import { LinkHost } from "../src/main/link";
import { RecentFolders } from "../src/main/recent";
import { type Sealer, ServerStore } from "../src/main/servers";
import type { DeckBridge, DeckHost, RpcStreamEvent, SocketEvent } from "../src/shared/bridge";
import { PROTO } from "./fake-grpc";

// The deck binary the tests run: it prints DECK_FAKE_HANDSHAKE and lives
// until stdin closes.
export const FAKE_DECK = join(__dirname, "fake-deck.cjs");

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

export interface TestHost {
  host: DeckHost;
  store: ServerStore;
  links: LinkHost;
  decks: RpcDecks;
}

export function testHost(
  emit: (id: number, ev: SocketEvent) => void,
  emitRpc: (id: string, ev: RpcStreamEvent) => void,
  pickFolder: () => Promise<string | null> = async () => null,
): TestHost {
  const store = new ServerStore(null, noKeychain);
  const links = new LinkHost(store, emit);
  const decks = new RpcDecks(FAKE_DECK, loadService(PROTO), emitRpc);
  const host = createHost({
    store,
    links,
    decks,
    recent: new RecentFolders(null),
    deckPath: FAKE_DECK,
    deviceName: "Deck desktop (test)",
    pickFolder,
  });
  return { host, store, links, decks };
}

// A bridge as the preload builds it, wired straight to the host. seen
// records everything main hands the window, to check that no token is in it.
export function inProcessBridge(): DeckBridge & TestHost & { seen: unknown[] } {
  const sockets = new Set<(id: number, ev: SocketEvent) => void>();
  const rpcs = new Set<(id: string, ev: RpcStreamEvent) => void>();
  const seen: unknown[] = [];
  const t = testHost(
    (id, ev) => {
      seen.push(ev);
      for (const l of sockets) l(id, ev);
    },
    (id, ev) => {
      seen.push(ev);
      for (const l of rpcs) l(id, ev);
    },
  );
  const recorded = Object.fromEntries(
    Object.entries(t.host).map(([k, fn]) => [
      k,
      async (...args: unknown[]) => {
        const r = await (fn as (...a: unknown[]) => Promise<unknown>)(...args);
        seen.push(r);
        return r;
      },
    ]),
  ) as unknown as DeckHost;
  const listen = <T>(set: Set<T>, fn: T) => {
    set.add(fn);
    return () => {
      set.delete(fn);
    };
  };
  return {
    ...recorded,
    ...t,
    host: t.host,
    seen,
    onSocket: (fn) => listen(sockets, fn),
    onRpc: (fn) => listen(rpcs, fn),
  };
}
