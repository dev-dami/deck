// The one surface between the renderer and the main process. The renderer
// never holds a token. For a Deck on another machine (protocol v2), it asks
// main to pair and talks through a socket that main opens, pins and
// authenticates. For a Deck on this machine (rpc), main starts
// `deck serve --rpc`, holds the per-launch token and makes the calls.

// A Deck server this app paired with. It carries no secret.
export interface SavedServer {
  // The server_id Deck gave at pairing: one per project data dir.
  id: string;
  host: string;
  port: number;
  tls: boolean;
  // "SHA256:<base64>" of the certificate, pinned on every connection; null
  // over plain loopback HTTP.
  fingerprint: string | null;
  deviceId: string;
  expiresAt: string;
  // False when this system has no safe place for the token: the pairing
  // then lasts until the app quits.
  persisted: boolean;
}

export interface PairInput {
  host: string;
  port: number;
  tls: boolean;
  fingerprint: string | null;
  code: string;
}

export type PairOutcome =
  | { ok: true; server: SavedServer }
  | { ok: false; message: string; retryAfterMs: number | null };

export type SocketEvent =
  | { type: "open" }
  | { type: "message"; data: string }
  | { type: "close"; code: number; reason: string };

// The token the renderer puts in its hello. Main replaces it with the real
// token before the frame leaves the machine.
export const TOKEN_PLACEHOLDER = "held-by-main";

// A Deck on this machine that main started with `deck serve --rpc` in a
// folder. It carries no secret: main keeps the token.
export interface LocalDeck {
  id: string;
  dir: string;
  project: string;
  version: string;
}

export type OpenOutcome = { ok: true; deck: LocalDeck } | { ok: false; message: string };

// The unary methods of deck.v1.Deck (proto/deck/v1/deck.proto) the window
// may call. Events is a stream and goes through rpcSubscribe.
export const RPC_METHODS = [
  "Info",
  "ListSessions",
  "CreateSession",
  "RenameSession",
  "DeleteSession",
  "ListMessages",
  "Send",
  "EditMessage",
  "Cancel",
  "Interrupt",
  "Continue",
  "Queue",
  "Steer",
  "CancelQueued",
  "Roster",
  "Models",
  "SetModel",
  "GetMode",
  "SetMode",
  "Approvals",
  "Answer",
  "Changes",
] as const;

export type RpcMethod = (typeof RPC_METHODS)[number];

// A call's answer: the response message as a plain object, or the gRPC
// status code and its message.
export type RpcResult = { ok: true; value: unknown } | { ok: false; code: number; message: string };

// What the Events stream of a local Deck sends to the window: one event (a
// deck.v1.Event as a plain object), the end of the stream, or the end of
// the Deck process.
export type RpcStreamEvent =
  | { type: "event"; event: unknown }
  | { type: "end"; code: number; message: string }
  | { type: "exit"; message: string };

// What main does for the renderer. The Electron main process serves it over
// IPC; the end-to-end tests serve the same object to a browser page.
export interface DeckHost {
  listServers(): Promise<SavedServer[]>;
  pair(input: PairInput): Promise<PairOutcome>;
  forgetServer(id: string): Promise<void>;
  // The deck binary on PATH, or null.
  findDeck(): Promise<string | null>;
  pickFolder(): Promise<string | null>;
  socketOpen(serverId: string): Promise<number>;
  socketSend(id: number, data: string): Promise<void>;
  socketClose(id: number, code: number, reason: string): Promise<void>;
  // Starts `deck serve --rpc` in dir, or reuses the Deck already open there.
  openFolder(dir: string): Promise<OpenOutcome>;
  // Closes the Deck's stdin, which stops it.
  closeFolder(id: string): Promise<void>;
  recentFolders(): Promise<string[]>;
  rpcCall(id: string, method: RpcMethod, request: Record<string, unknown>): Promise<RpcResult>;
  // Opens the Events stream from (epoch, seq) and closes the one open before.
  rpcSubscribe(id: string, epoch: string, seq: number): Promise<void>;
}

// The renderer's view: the host plus the socket and rpc event streams.
export interface DeckBridge extends DeckHost {
  onSocket(fn: (id: number, ev: SocketEvent) => void): () => void;
  onRpc(fn: (id: string, ev: RpcStreamEvent) => void): () => void;
}

export const HOST_METHODS = [
  "listServers",
  "pair",
  "forgetServer",
  "findDeck",
  "pickFolder",
  "socketOpen",
  "socketSend",
  "socketClose",
  "openFolder",
  "closeFolder",
  "recentFolders",
  "rpcCall",
  "rpcSubscribe",
] as const satisfies ReadonlyArray<keyof DeckHost>;

export type HostMethod = (typeof HOST_METHODS)[number];

export const SOCKET_EVENT_CHANNEL = "deck:socket";
export const RPC_EVENT_CHANNEL = "deck:rpc";

export function hostChannel(m: HostMethod): string {
  return `deck:${m}`;
}
