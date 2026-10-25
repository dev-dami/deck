import {
  type AppInfo,
  type DeckHost,
  type HostMethod,
  LOG_LEVELS,
  type LogLevel,
  type PairInput,
  RPC_METHODS,
  type RpcMethod,
  type ThreadChoice,
} from "../shared/bridge";
import type { RpcDecks } from "./decks";
import type { LinkHost } from "./link";
import { codeError, type Connect, pairWith } from "./pair";
import { connectPinned, normalizeFingerprint, targetError } from "./pin";
import type { PhoneService } from "./phone";
import type { RecentFolders } from "./recent";
import type { ServerStore } from "./servers";

export interface HostDeps {
  store: ServerStore;
  links: LinkHost;
  decks: RpcDecks;
  phone: PhoneService;
  recent: RecentFolders;
  // Null when deck is not on PATH.
  deckPath: string | null;
  pickFolder: () => Promise<string | null>;
  deviceName: string;
  connect?: Connect;
  appInfo: AppInfo;
  log: (level: LogLevel, message: string) => void;
  openLogs: () => Promise<void>;
  installUpdate: () => void;
  threadMenu: (thread: { id: string; title: string }) => Promise<ThreadChoice>;
}

// The longest renderer log line kept: a stack trace, not a flood.
const MAX_LOG = 4000;

// The main process's half of the bridge. Every argument comes from the
// renderer, so callHost checks its type before a method sees it.
export function createHost(d: HostDeps): DeckHost {
  const connect = d.connect ?? connectPinned;
  return {
    listServers: async () => d.store.list(),
    pair: async (input: PairInput) => {
      const fingerprint = input.fingerprint === null ? null : normalizeFingerprint(input.fingerprint);
      if (input.fingerprint !== null && fingerprint === null) {
        return { ok: false, message: "The fingerprint is not a SHA-256 fingerprint.", retryAfterMs: null };
      }
      const t = { host: input.host, port: input.port, tls: input.tls, fingerprint };
      const bad = targetError(t) ?? codeError(input.code);
      if (bad !== null) return { ok: false, message: bad, retryAfterMs: null };
      const r = await pairWith(t, input.code, d.deviceName, connect);
      if (!r.ok) return r;
      const server = d.store.save(
        {
          id: r.paired.serverId,
          host: t.host,
          port: t.port,
          tls: t.tls,
          fingerprint: t.fingerprint,
          deviceId: r.paired.deviceId,
          expiresAt: r.paired.expiresAt,
        },
        r.paired.token,
      );
      return { ok: true, server };
    },
    forgetServer: async (id: string) => d.store.remove(id),
    findDeck: async () => d.deckPath,
    pickFolder: () => d.pickFolder(),
    socketOpen: async (serverId: string) => d.links.open(serverId),
    socketSend: async (id: number, data: string) => d.links.send(id, data),
    socketClose: async (id: number, code: number, reason: string) => d.links.close(id, code, reason),
    openFolder: async (dir: string) => {
      const out = await d.decks.open(dir);
      if (out.ok) d.recent.add(dir);
      return out;
    },
    closeFolder: async (id: string) => d.decks.close(id),
    restartFolder: (id: string) => d.decks.restart(id),
    recentFolders: async () => d.recent.list(),
    rpcCall: (id: string, method: RpcMethod, request: Record<string, unknown>) =>
      d.decks.call(id, method, request),
    rpcSubscribe: async (id: string, epoch: string, seq: number) => d.decks.subscribe(id, epoch, seq),
    termSubscribe: async (id: string, term: string) => d.decks.termSubscribe(id, term),
    termUnsubscribe: async (id: string, term: string) => d.decks.termUnsubscribe(id, term),
    phoneStatus: () => d.phone.status(),
    // Only a folder this app has open is shared, whatever the renderer asks.
    phoneLink: (folder: string | null, relayDir: string | null) =>
      d.phone.link(folder !== null && d.decks.isOpenDir(folder) ? folder : null, relayDir),
    phoneNewCode: () => d.phone.newCode(),
    phoneRevoke: (deviceId: string) => d.phone.revoke(deviceId),
    phoneStop: () => d.phone.stop(),
    appInfo: async () => d.appInfo,
    openLogs: () => d.openLogs(),
    logFromRenderer: async (level: LogLevel, message: string) => d.log(level, `renderer: ${message.slice(0, MAX_LOG)}`),
    installUpdate: async () => d.installUpdate(),
    threadMenu: (thread: { id: string; title: string }) => d.threadMenu({ id: thread.id, title: thread.title }),
  };
}

type Check = (v: unknown) => boolean;
const isStr: Check = (v) => typeof v === "string";
const isInt: Check = (v) => Number.isInteger(v);
const isNat: Check = (v) => Number.isSafeInteger(v) && (v as number) >= 0;
const isObject: Check = (v) =>
  typeof v === "object" && v !== null && !Array.isArray(v) && Object.getPrototypeOf(v) === Object.prototype;
const isRpcMethod: Check = (v) => (RPC_METHODS as readonly unknown[]).includes(v);
const isStrOrNull: Check = (v) => v === null || typeof v === "string";
const isPairInput: Check = (v) => {
  if (!isObject(v)) return false;
  const p = v as Record<string, unknown>;
  return (
    isStr(p["host"]) &&
    isInt(p["port"]) &&
    typeof p["tls"] === "boolean" &&
    (p["fingerprint"] === null || isStr(p["fingerprint"])) &&
    isStr(p["code"])
  );
};

const isLogLevel: Check = (v) => (LOG_LEVELS as readonly unknown[]).includes(v);
const isThread: Check = (v) => {
  if (!isObject(v)) return false;
  const t = v as Record<string, unknown>;
  return Object.keys(t).length === 2 && isStr(t["id"]) && isStr(t["title"]);
};

const ARGS: Record<HostMethod, Check[]> = {
  listServers: [],
  pair: [isPairInput],
  forgetServer: [isStr],
  findDeck: [],
  pickFolder: [],
  socketOpen: [isStr],
  socketSend: [isInt, isStr],
  socketClose: [isInt, isInt, isStr],
  openFolder: [isStr],
  closeFolder: [isStr],
  restartFolder: [isStr],
  recentFolders: [],
  // The request's fields are checked by the proto encoder.
  rpcCall: [isStr, isRpcMethod, isObject],
  rpcSubscribe: [isStr, isStr, isNat],
  termSubscribe: [isStr, isStr],
  termUnsubscribe: [isStr, isStr],
  phoneStatus: [],
  phoneLink: [isStrOrNull, isStrOrNull],
  phoneNewCode: [],
  phoneRevoke: [isStr],
  phoneStop: [],
  appInfo: [],
  openLogs: [],
  logFromRenderer: [isLogLevel, isStr],
  installUpdate: [],
  threadMenu: [isThread],
};

// Calls method m with args from the renderer, or rejects when they are not
// the types the method takes.
export function callHost(host: DeckHost, m: HostMethod, args: unknown[]): Promise<unknown> {
  const checks = ARGS[m];
  if (args.length !== checks.length || !checks.every((c, i) => c(args[i]))) {
    return Promise.reject(new Error(`bad arguments for ${m}`));
  }
  const fn = host[m] as (...a: unknown[]) => Promise<unknown>;
  return fn(...args);
}
