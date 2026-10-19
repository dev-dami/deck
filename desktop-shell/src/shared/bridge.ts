// The one surface between the renderer and the main process. The renderer
// never holds a token: it asks main to pair, and it talks to Deck through a
// socket that main opens, pins and authenticates.

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

// What main does for the renderer. The Electron main process serves it over
// IPC; the end-to-end tests serve the same object to a browser page.
export interface DeckHost {
  listServers(): Promise<SavedServer[]>;
  pair(input: PairInput): Promise<PairOutcome>;
  forgetServer(id: string): Promise<void>;
  // The deck binary on PATH, or null.
  findDeck(): Promise<string | null>;
  // Starts `deck remote` in dir and pairs with it.
  startLocal(dir: string): Promise<PairOutcome>;
  pickFolder(): Promise<string | null>;
  socketOpen(serverId: string): Promise<number>;
  socketSend(id: number, data: string): Promise<void>;
  socketClose(id: number, code: number, reason: string): Promise<void>;
}

// The renderer's view: the host plus a stream of socket events.
export interface DeckBridge extends DeckHost {
  onSocket(fn: (id: number, ev: SocketEvent) => void): () => void;
}

export const HOST_METHODS = [
  "listServers",
  "pair",
  "forgetServer",
  "findDeck",
  "startLocal",
  "pickFolder",
  "socketOpen",
  "socketSend",
  "socketClose",
] as const satisfies ReadonlyArray<keyof DeckHost>;

export type HostMethod = (typeof HOST_METHODS)[number];

export const SOCKET_EVENT_CHANNEL = "deck:socket";

export function hostChannel(m: HostMethod): string {
  return `deck:${m}`;
}
