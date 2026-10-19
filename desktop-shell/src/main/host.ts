import type { DeckHost, HostMethod, PairInput, PairOutcome } from "../shared/bridge";
import type { LinkHost } from "./link";
import type { LocalServers } from "./local";
import { codeError, pairWith, type Connect } from "./pair";
import { connectPinned, normalizeFingerprint, type Target, targetError } from "./pin";
import type { ServerStore } from "./servers";

export interface HostDeps {
  store: ServerStore;
  links: LinkHost;
  // Null when deck is not on PATH.
  local: LocalServers | null;
  deckPath: string | null;
  pickFolder: () => Promise<string | null>;
  deviceName: string;
  connect?: Connect;
}

// The main process's half of the bridge. Every argument comes from the
// renderer, so callHost checks its type before a method sees it.
export function createHost(d: HostDeps): DeckHost {
  const connect = d.connect ?? connectPinned;

  async function pairTarget(t: Target, code: string): Promise<PairOutcome> {
    const bad = targetError(t) ?? codeError(code);
    if (bad !== null) return { ok: false, message: bad, retryAfterMs: null };
    const r = await pairWith(t, code, d.deviceName, connect);
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
  }

  return {
    listServers: async () => d.store.list(),
    pair: (input: PairInput) => {
      const fingerprint = input.fingerprint === null ? null : normalizeFingerprint(input.fingerprint);
      if (input.fingerprint !== null && fingerprint === null) {
        return Promise.resolve({
          ok: false,
          message: "The fingerprint is not a SHA-256 fingerprint.",
          retryAfterMs: null,
        });
      }
      return pairTarget({ host: input.host, port: input.port, tls: input.tls, fingerprint }, input.code);
    },
    forgetServer: async (id: string) => d.store.remove(id),
    findDeck: async () => d.deckPath,
    startLocal: async (dir: string) => {
      if (d.local === null) {
        return { ok: false, message: "The deck command is not on PATH.", retryAfterMs: null };
      }
      const started = await d.local.start(dir);
      if ("error" in started) return { ok: false, message: started.error, retryAfterMs: null };
      const outcome = await pairTarget(started.offer.target, started.offer.code);
      if (!outcome.ok) started.done();
      return outcome;
    },
    pickFolder: () => d.pickFolder(),
    socketOpen: async (serverId: string) => d.links.open(serverId),
    socketSend: async (id: number, data: string) => d.links.send(id, data),
    socketClose: async (id: number, code: number, reason: string) =>
      d.links.close(id, code, reason),
  };
}

type Check = (v: unknown) => boolean;
const isStr: Check = (v) => typeof v === "string";
const isInt: Check = (v) => Number.isInteger(v);
const isPairInput: Check = (v) => {
  if (typeof v !== "object" || v === null) return false;
  const p = v as Record<string, unknown>;
  return (
    isStr(p["host"]) &&
    isInt(p["port"]) &&
    typeof p["tls"] === "boolean" &&
    (p["fingerprint"] === null || isStr(p["fingerprint"])) &&
    isStr(p["code"])
  );
};

const ARGS: Record<HostMethod, Check[]> = {
  listServers: [],
  pair: [isPairInput],
  forgetServer: [isStr],
  findDeck: [],
  startLocal: [isStr],
  pickFolder: [],
  socketOpen: [isStr],
  socketSend: [isInt, isStr],
  socketClose: [isInt, isInt, isStr],
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
