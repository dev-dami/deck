// The real main-process host (pairing, pinning, the socket relay, local
// Decks over rpc) without Electron: tests call it in-process, and the e2e
// tests serve it to a page.
import { join } from "node:path";
import { loadService, RpcDecks } from "../src/main/decks";
import { createHost, type HostDeps } from "../src/main/host";
import { LinkHost } from "../src/main/link";
import { PhoneLink } from "../src/main/phone";
import { Browser } from "../src/main/browser";
import { RecentFolders } from "../src/main/recent";
import { type Sealer, ServerStore } from "../src/main/servers";
import type { DeckBridge, DeckHost, LogLevel, RpcStreamEvent, SocketEvent } from "../src/shared/bridge";
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
  // What the host wrote to the log, as [level, message].
  logged: [LogLevel, string][];
}

export function testHost(
  emit: (id: number, ev: SocketEvent) => void,
  emitRpc: (id: string, ev: RpcStreamEvent) => void,
  pickFolder: () => Promise<string | null> = async () => null,
  deps: Partial<HostDeps> = {},
): TestHost {
  const store = new ServerStore(null, noKeychain);
  const links = new LinkHost(store, emit);
  // No window unless a test brings one: its pages are not made.
  const browser = (deps.browser as Browser | undefined) ?? new Browser({ factory: { create: () => null }, send: () => {} });
  const decks = new RpcDecks(FAKE_DECK, loadService(PROTO), emitRpc, null, undefined, browser);
  const logged: [LogLevel, string][] = [];
  const host = createHost({
    store,
    links,
    decks,
    phone: new PhoneLink(null),
    recent: new RecentFolders(null),
    deckPath: FAKE_DECK,
    deviceName: "Deck desktop (test)",
    pickFolder,
    appInfo: { version: "0.1.0", electron: "test", chromium: "test", platform: process.platform },
    log: (level, message) => logged.push([level, message]),
    openLogs: async () => {},
    installUpdate: () => {},
    threadMenu: async () => null,
    notify: () => {},
    badge: () => {},
    focused: () => true,
    agentClis: async () => [
      { kind: "claude", path: "/usr/local/bin/claude" },
      { kind: "codex", path: "/opt/homebrew/bin/codex" },
      { kind: "opencode", path: null },
      { kind: "antigravity", path: null },
      { kind: "pi", path: null },
    ],
    browser,
    setAppIcon: () => ({ shown: true, launchers: "none" }),
    ...deps,
  });
  return { host, store, links, decks, logged };
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
    // No menu, links or updates outside Electron.
    onMenu: () => () => {},
    onLink: () => () => {},
    onUpdate: () => () => {},
    onNotificationClick: () => () => {},
    onBrowser: () => () => {},
    folderPath: () => "",
  };
}
