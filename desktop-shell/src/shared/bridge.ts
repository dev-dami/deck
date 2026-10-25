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

// Why a local Deck's engine stopped by itself: main's message and the last
// lines it wrote to stderr. The project stays open and can restart.
export interface EngineStop {
  message: string;
  stderr: string[];
}

// A phone paired with this computer through `deck mobile connect`. Keys and
// relay tokens never reach the window.
export interface PhoneDevice {
  id: string;
  name: string;
}

// What `deck mobile connect --json` reports, for the Phone page.
export interface PhoneState {
  name: string;
  relayUrl: string;
  online: boolean;
  projects: number;
  devices: PhoneDevice[];
  // The pairing link and its QR (rows of "1" dark and "0" light modules,
  // quiet zone included); set only just after a link or a new code.
  pairUrl: string | null;
  qr: string[] | null;
  expiresAt: string | null;
  message: string | null;
}

export type PhoneOutcome =
  | { ok: true; phone: PhoneState }
  // needsRelayDir: the local relay runtime was not found; the page asks for
  // the folder that holds it (pkg/remote/relay with its dependencies).
  | { ok: false; message: string; needsRelayDir: boolean };

// The unary methods of deck.v1.Deck (proto/deck/v1/deck.proto) the window
// may call. Events and TerminalOutput are streams and go through
// rpcSubscribe and termSubscribe.
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
  "GitStatus",
  "GitCheckout",
  "GitCommit",
  "GitPush",
  "GitPull",
  "GitCreatePR",
  "GitMergePR",
  "Usage",
  "TerminalOpen",
  "TerminalWrite",
  "TerminalResize",
  "TerminalClose",
  "Board",
  "TaskAction",
  "SetOrchestrate",
  "Agents",
  "Team",
  "TeamPost",
  "ShareThread",
  "ThreadShares",
  "ProposeContract",
  "AnswerContract",
  "ReadShared",
  "PostShared",
  "AnswerShared",
  // Optional: Info.capabilities says whether the Deck serves them.
  "IsolateSession",
  "ApplyIsolated",
  "UnisolateSession",
  "RevertFiles",
  "UndoRevert",
  "ArchiveSession",
  "StartRace",
  "ListFiles",
] as const;

export type RpcMethod = (typeof RPC_METHODS)[number];

// A call's answer: the response message as a plain object, or the gRPC
// status code and its message.
export type RpcResult = { ok: true; value: unknown } | { ok: false; code: number; message: string };

// What the Events stream of a local Deck sends to the window: one event (a
// deck.v1.Event as a plain object), the end of the stream, the end of the
// Deck process on its own (stopped: restartFolder starts it again), or a
// Deck that is closed for good (exit).
export type RpcStreamEvent =
  | { type: "event"; event: unknown }
  | { type: "end"; code: number; message: string }
  | ({ type: "stopped" } & EngineStop)
  | { type: "exit"; message: string }
  | TermEvent;

// What a terminal's output stream sends: what the shell printed (base64,
// as the bytes may split a UTF-8 sequence), its exit, or the end of the
// stream (the window subscribes again, and gets the scrollback).
export type TermEvent =
  | { type: "term"; term: string; data: string }
  | { type: "term-exit"; term: string; code: number }
  | { type: "term-end"; term: string; code: number; message: string };

// The commands of the application menu. Main sends one on MENU_EVENT_CHANNEL;
// the renderer runs the palette command that matches it.
export const MENU_COMMANDS = [
  "new-thread",
  "open-folder",
  "settings",
  "toggle-sidebar",
  "toggle-panel",
  "toggle-terminal",
  "palette",
  "about",
  "inbox",
  "next-attention",
  "room-1",
  "room-2",
  "room-3",
  "room-4",
  "room-5",
  "room-6",
  "room-7",
  "room-8",
  "room-9",
] as const;

export type MenuCommand = (typeof MENU_COMMANDS)[number];

// A deck:// link or a folder argument, sent on LINK_EVENT_CHANNEL. "pair"
// carries the invite line for the pairing form; "open" a folder that exists.
export type LinkEvent = { type: "pair"; url: string } | { type: "open"; path: string };

// What the updater tells the window: an update is downloaded and waits for
// a restart, or the answer to "Check for updates" in the Help menu.
export type UpdateEvent = { type: "ready"; version: string } | { type: "status"; message: string };

export const LOG_LEVELS = ["error", "warn", "info"] as const;
export type LogLevel = (typeof LOG_LEVELS)[number];

export interface AppInfo {
  version: string;
  electron: string;
  chromium: string;
  platform: string;
}

// The item picked in a thread row's native menu, or null.
export type ThreadChoice = "rename" | "delete" | null;

// A native notification. tag names the room; a click on the notification
// sends it back on NOTIFICATION_EVENT_CHANNEL. silent: no sound.
export interface NotifyInput {
  title: string;
  body: string;
  tag: string;
  silent: boolean;
}

export const NOTIFY_LIMITS = { title: 200, body: 500, tag: 500, badge: 9999 } as const;

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
  // Starts the engine of a Deck that stopped by itself again, in the same
  // folder and with the same id.
  restartFolder(id: string): Promise<OpenOutcome>;
  recentFolders(): Promise<string[]>;
  rpcCall(id: string, method: RpcMethod, request: Record<string, unknown>): Promise<RpcResult>;
  // Opens the Events stream from (epoch, seq) and closes the one open before.
  rpcSubscribe(id: string, epoch: string, seq: number): Promise<void>;
  // Streams a terminal's output (from its scrollback) as TermEvents.
  termSubscribe(id: string, term: string): Promise<void>;
  termUnsubscribe(id: string, term: string): Promise<void>;
  // The phone link of this computer (`deck mobile connect`). Status reads
  // it; link starts the relay and daemon (sharing the open folder, when one
  // is given) and returns a fresh QR; newCode replaces the QR; revoke
  // disconnects one phone; stop ends remote access.
  phoneStatus(): Promise<PhoneOutcome>;
  phoneLink(folder: string | null, relayDir: string | null): Promise<PhoneOutcome>;
  phoneNewCode(): Promise<PhoneOutcome>;
  phoneRevoke(deviceId: string): Promise<PhoneOutcome>;
  phoneStop(): Promise<PhoneOutcome>;
  // The app's version and its runtime, for Settings > About.
  appInfo(): Promise<AppInfo>;
  openLogs(): Promise<void>;
  // Writes an error of the window to the app's log file.
  logFromRenderer(level: LogLevel, message: string): Promise<void>;
  // Quits and starts the downloaded update.
  installUpdate(): Promise<void>;
  // Shows the native menu of a thread row and resolves with the choice.
  threadMenu(thread: { id: string; title: string }): Promise<ThreadChoice>;
  // Shows a native notification; main sends it, the window needs no
  // permission for it.
  notify(n: NotifyInput): Promise<void>;
  // The count on the dock or taskbar icon; 0 clears it.
  badge(count: number): Promise<void>;
  // Whether the window has the focus.
  focused(): Promise<boolean>;
}

// The renderer's view: the host, the event streams, and the path of a
// folder dropped on the window.
export interface DeckBridge extends DeckHost {
  onSocket(fn: (id: number, ev: SocketEvent) => void): () => void;
  onRpc(fn: (id: string, ev: RpcStreamEvent) => void): () => void;
  onMenu(fn: (command: MenuCommand) => void): () => void;
  onLink(fn: (link: LinkEvent) => void): () => void;
  onUpdate(fn: (ev: UpdateEvent) => void): () => void;
  // A click on a notification, with its tag.
  onNotificationClick(fn: (ev: { tag: string }) => void): () => void;
  // The path of a dropped folder. The window passes only folders, and main
  // checks that the path is one before it opens it.
  folderPath(file: File): string;
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
  "restartFolder",
  "recentFolders",
  "rpcCall",
  "rpcSubscribe",
  "termSubscribe",
  "termUnsubscribe",
  "phoneStatus",
  "phoneLink",
  "phoneNewCode",
  "phoneRevoke",
  "phoneStop",
  "appInfo",
  "openLogs",
  "logFromRenderer",
  "installUpdate",
  "threadMenu",
  "notify",
  "badge",
  "focused",
] as const satisfies ReadonlyArray<keyof DeckHost>;

export type HostMethod = (typeof HOST_METHODS)[number];

export const SOCKET_EVENT_CHANNEL = "deck:socket";
export const RPC_EVENT_CHANNEL = "deck:rpc";
export const MENU_EVENT_CHANNEL = "deck:menu";
export const LINK_EVENT_CHANNEL = "deck:link";
export const UPDATE_EVENT_CHANNEL = "deck:update";
export const NOTIFICATION_EVENT_CHANNEL = "deck:notification-click";

export function hostChannel(m: HostMethod): string {
  return `deck:${m}`;
}
