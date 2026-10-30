// The one surface between the renderer and the main process. The renderer
// never holds a token. For a Deck on another machine (protocol v2), it asks
// main to pair and talks through a socket that main opens, pins and
// authenticates. For a Deck on this machine (rpc), main starts
// `deck serve --rpc`, holds the per-launch token and makes the calls.

import type { ArtifactConflict, ArtifactDiagnostic, ArtifactFile, ArtifactKind } from "./artifacts";

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

// Why the engine did not start: the last lines it wrote to stderr, and
// whether it found no agent CLI on PATH.
export interface StartError {
  lines: string[];
  noAgents: boolean;
}

export type OpenOutcome = { ok: true; deck: LocalDeck } | { ok: false; message: string; start?: StartError };

// An agent CLI the engine can drive, and where it is on the engine's PATH.
export interface AgentCli {
  kind: string;
  path: string | null;
}

// The agent CLIs on the engine's PATH, and whether the engine was found.
export interface AgentClis {
  engine: boolean;
  clis: AgentCli[];
}

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
  "UnarchiveSession",
  "UndoApply",
  "StartRace",
  "ListFiles",
  // Bytes arrive as a Uint8Array.
  "ReadImage",
  "SearchMessages",
  // Named agents (docs/design/agents-and-mentions.md, section 4).
  "ListProfiles",
  "SaveProfile",
  "DeleteProfile",
  // Plan limits: checks, waits for a reset, a window started early.
  "Limits",
  "CheckLimits",
  "StartWindow",
  "ResolveWait",
  "ScheduleSend",
  "CancelScheduled",
  // Worktrees and named capsules: the Work tab.
  "Work",
  "WorktreeAction",
  "CapsuleAction",
  "CapsuleFiles",
  // Optional desktop browser and persisted thread highlights.
  "BrowserState",
  "BrowserAction",
  "Highlights",
  "HighlightAction",
  // Artifacts: saved revisions of documents and small apps.
  "Artifacts",
  "ArtifactRevisions",
  "ArtifactRead",
  "ArtifactAction",
  "ReportArtifactBuild",
] as const;

export type RpcMethod = (typeof RPC_METHODS)[number];

// Main-only methods: never add these to RPC_METHODS/the renderer allowlist.
export const BROWSER_CONTROLLER_METHODS = ["RegisterBrowserController", "UnregisterBrowserController", "BrowserResult", "BrowserUpdate", "BrowserOrigin"] as const;
export type MainRpcMethod = RpcMethod | (typeof BROWSER_CONTROLLER_METHODS)[number];

// Protobuf/IPC image bytes arrive as Uint8Array. Only the JSON/MCP wire
// base64-encodes them; an image is never a base64 text tool result.
export interface OpImage {
  mime_type: "image/png" | "image/jpeg";
  data: Uint8Array;
}

export interface OpResult {
  text: string;
  images: OpImage[];
  is_error: boolean;
}

export const BROWSER_LIMITS = {
  url: 8192,
  text: 16_384,
  tabs: 40,
  snapshot: 64 * 1024,
  imageBytes: 8 * 1024 * 1024,
  imagePixels: 16_777_216,
  executionMs: 30_000,
} as const;

export type BrowserCommand =
  | { action: "open"; url: string }
  | { action: "navigate"; tab_id: string; url: string }
  | { action: "back" | "forward" | "reload" | "close" | "take_control" | "release_control"; tab_id: string }
  | { action: "snapshot"; tab_id: string; interactive?: boolean; max_lines?: number }
  // An element_ref in a screenshot clips to the element; it then needs the snapshot_version too.
  | { action: "screenshot"; tab_id: string; element_ref?: string; snapshot_version?: string }
  | { action: "click"; tab_id: string; element_ref: string; snapshot_version: string }
  | { action: "type"; tab_id: string; element_ref: string; snapshot_version: string; text: string }
  | { action: "select"; tab_id: string; element_ref: string; snapshot_version: string; text: string }
  | { action: "press"; tab_id: string; key: string }
  // scroll: dy pixels, or to top/bottom, or an element_ref (with its snapshot_version) into view.
  | { action: "scroll"; tab_id: string; dy?: number; to?: "top" | "bottom"; element_ref?: string; snapshot_version?: string }
  // wait: for text on the page, for the load to finish, or for ms; timeout_s bounds the first two.
  | { action: "wait"; tab_id: string; text?: string; load?: boolean; ms?: number; timeout_s?: number }
  | { action: "handoff"; tab_id: string; target_agent: string };

export interface BrowserActionRequest {
  session_id: string;
  // Renderer = user: any tab of this session is controllable. Navigation
  // commands imply Take control; release_control explicitly resumes agents.
  command: BrowserCommand;
}

export interface BrowserTab {
  id: string;
  session_id: string;
  owner: string;
  url: string;
  title: string;
  loading: boolean;
  can_back: boolean;
  can_forward: boolean;
  user_in_control: boolean;
  snapshot_version: string;
  // What the owning agent is doing now ("clicking e12"), for a moment after.
  activity: string;
  // The page was let go to save memory; it comes back at the same address.
  asleep: boolean;
}

export interface BrowserStateResponse {
  available: boolean;
  tabs: BrowserTab[];
  session_id: string;
}

export type BrowserErrorCode = "browser_unavailable" | "user_in_control" | "stale_snapshot" | "not_owner" | "origin_denied" | "cancelled" | "timeout" | "invalid_request" | "unsupported_image";

export interface BrowserActionResponse {
  tab: BrowserTab | null;
  result: OpResult | null;
  error_code: BrowserErrorCode | "";
}

export interface HighlightSource {
  message_id: string;
  task_id: string;
  turn_id: string;
}

export type HighlightKind = "needs_you" | "blocker" | "finding";
export type HighlightState = "open" | "resolved" | "dismissed";
export const HIGHLIGHT_LIMITS = { title: 160, detail: 2000, summary: 280, key: 128 } as const;

export interface HighlightItem {
  id: string;
  session_id: string;
  agent: string;
  key: string;
  kind: HighlightKind;
  title: string;
  detail: string;
  source: HighlightSource | null;
  state: HighlightState;
  created_at: number;
  updated_at: number;
  unread: boolean;
}

export interface AgentSummary {
  session_id: string;
  agent: string;
  text: string;
  source: HighlightSource | null;
  updated_at: number;
}

export interface HighlightsResponse {
  items: HighlightItem[];
  summaries: AgentSummary[];
}

export interface HighlightActionRequest {
  session_id: string;
  action: "add" | "update" | "resolve" | "dismiss" | "seen" | "pin";
  id?: string;
  key?: string;
  kind?: HighlightKind;
  title?: string;
  detail?: string;
  source?: HighlightSource;
  // Only the seen action accepts a batch.
  ids?: string[];
}

// A native tab overlays the window, like Preview. Null bounds hides the
// browser. The caller cannot select a tab belonging to a different room.
export interface BrowserViewInput {
  deck_id: string;
  session_id: string;
  tab_id: string;
  bounds: PreviewBounds | null;
}

export interface BrowserViewHost {
  browserView(input: BrowserViewInput): Promise<void>;
}

// A call's answer: the response message as a plain object, or the gRPC
// status code and its message.
// artifact_conflict is set when the engine refused a stale artifact edit.
export type RpcResult =
  | { ok: true; value: unknown }
  | { ok: false; code: number; message: string; artifact_conflict?: ArtifactConflict };

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
export type ThreadChoice = "rename" | "archive" | "unarchive" | "delete" | null;

// Archive in a thread row's menu: offered, shown but off while an agent
// works, left out for a Deck that cannot archive, or Unarchive for an
// archived room.
export const ARCHIVE_OFFERS = ["on", "busy", "none", "archived"] as const;
export type ArchiveOffer = (typeof ARCHIVE_OFFERS)[number];

export interface ThreadMenuInput {
  id: string;
  title: string;
  archive: ArchiveOffer;
}

// A native notification. tag names the room; a click on the notification
// sends it back on NOTIFICATION_EVENT_CHANNEL. silent: no sound.
export interface NotifyInput {
  title: string;
  body: string;
  tag: string;
  silent: boolean;
}

export const NOTIFY_LIMITS = { title: 200, body: 500, tag: 500, badge: 9999 } as const;

// The preview pane: a page of a dev server on this computer, in a view that
// main owns and lays over the window where the renderer asks.
export const PREVIEW_LIMITS = { ports: 32, url: 2048 } as const;

export type PreviewNav = { action: "back" | "forward" | "reload" } | { action: "path"; path: string };

// Where the view goes, in the window's CSS pixels, and the page's zoom (a
// device width wider than the box zooms the page out to fit).
export interface PreviewBounds {
  x: number;
  y: number;
  w: number;
  h: number;
  zoom: number;
}

// What main sends on PREVIEW_EVENT_CHANNEL for the address bar.
export interface PreviewState {
  url: string;
  canBack: boolean;
  canForward: boolean;
  loading: boolean;
  title: string;
}

// The artifact pane: the files of one artifact revision in a view that main
// owns, like Preview. Main builds a React revision first (it never runs the
// code) and answers with the diagnostics when the build fails.
export interface ArtifactShowInput {
  // Opaque, chosen by the caller and echoed on every ArtifactShown of this
  // show, so events of an earlier page never count for a later one.
  request: string;
  kind: Exclude<ArtifactKind, "markdown">;
  entrypoint: string;
  files: ArtifactFile[];
}

// What main accepts to show: the contract's bounds, per revision.
export const ARTIFACT_SHOW_LIMITS = { files: 32, file: 256 << 10, total: 1 << 20 } as const;

export type ArtifactShowResult = { ok: true } | { ok: false; diagnostics: ArtifactDiagnostic[] };

export type ArtifactBounds = PreviewBounds;

// What main sends on ARTIFACT_EVENT_CHANNEL about the shown page.
export interface ArtifactShown {
  request: string;
  kind: "loaded" | "error" | "gone";
  message: string;
}

// The app icon, drawn by the renderer (renderer/appIcon) as PNGs at a few
// sizes of one square image. Main checks each one (the PNG signature, its
// IHDR width and height against size, the byte caps), then shows it on the
// Dock (macOS) or on the window and its taskbar button (Windows, Linux),
// and keeps the last one for the next start. On Windows it also points the
// installer's shortcuts (desktop, Start menu, a taskbar pin) at it, and on
// Linux the package's .desktop launcher, through a copy under the user's
// applications; the standard icon puts theirs back (main/launcherIcon.ts).
// A Mac's bundle (Finder, Launchpad) keeps its own.
export interface AppIconImage {
  // Width and height in pixels: one of APP_ICON_LIMITS.sizes.
  size: number;
  png: Uint8Array;
}

// What became of the installed app's launchers: "done" when they wear the
// icon, "none" when there are none Deck can change (a Mac, a development
// build, an AppImage no menu knows), "denied" when some are everyone's and
// need an administrator (a per-machine install), "failed" otherwise.
export type LauncherState = "done" | "none" | "denied" | "failed";

export interface AppIconResult {
  // Whether the running app shows it: false when the system could not
  // decode it, and then nothing else changed.
  shown: boolean;
  launchers: LauncherState;
}

export const APP_ICON_LIMITS = {
  sizes: [16, 24, 32, 48, 64, 128, 256, 512],
  // Bytes of one PNG, and of all of them.
  bytes: 1024 * 1024,
  total: 3 * 1024 * 1024,
} as const;

// The sizes the renderer draws for each platform. The Dock shows up to
// 256px (128pt at 2x) and the 512 keeps it sharp when magnified; Windows
// and Linux pick from the small sizes for title bars, taskbars and
// switchers.
export const APP_ICON_SIZES = {
  mac: [128, 256, 512],
  other: [16, 24, 32, 48, 64, 128, 256],
} as const;

// What main does for the renderer. The Electron main process serves it over
// IPC; the end-to-end tests serve the same object to a browser page.
// Optional while talking to older hosts; runtime browser support also
// requires BrowserState/BrowserAction capabilities. Main wires the method
// when the native browser manager is installed.
export interface DeckHost extends Partial<BrowserViewHost> {
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
  threadMenu(thread: ThreadMenuInput): Promise<ThreadChoice>;
  // Shows a native notification; main sends it, the window needs no
  // permission for it.
  notify(n: NotifyInput): Promise<void>;
  // The count on the dock or taskbar icon; 0 clears it.
  badge(count: number): Promise<void>;
  // Whether the window has the focus.
  focused(): Promise<boolean>;
  // The agent CLIs on the engine's PATH. Main looks once; refresh looks again.
  agentClis(refresh: boolean): Promise<AgentClis>;
  // The ports (at most 32) that accept a connection on this computer now.
  probePorts(ports: number[]): Promise<number[]>;
  // Loads a loopback http URL of an open port in the preview view; false
  // when main refuses it.
  previewOpen(input: { url: string }): Promise<boolean>;
  previewNav(nav: PreviewNav): Promise<void>;
  // Shows the view at these bounds.
  previewBounds(bounds: PreviewBounds): Promise<void>;
  previewHide(): Promise<void>;
  // Destroys the view and its page.
  previewClose(): Promise<void>;
  // Builds (React) and shows an artifact revision in the artifact view.
  artifactShow(input: ArtifactShowInput): Promise<ArtifactShowResult>;
  artifactBounds(bounds: ArtifactBounds): Promise<void>;
  artifactHide(): Promise<void>;
  artifactClose(): Promise<void>;
  // Shows this icon for the app, keeps it for the next start and gives it
  // to the installed app's launchers. standard: it is the icon Deck ships
  // with, so the launchers go back to their own.
  setAppIcon(images: AppIconImage[], standard: boolean): Promise<AppIconResult>;
  // The icon a project folder already has (its logo or favicon) as a data:
  // URL, or null. Only for a folder the app has open or opened lately.
  projectIcon(dir: string): Promise<string | null>;
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
  onPreview(fn: (s: PreviewState) => void): () => void;
  onArtifact(fn: (s: ArtifactShown) => void): () => void;
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
  "agentClis",
  "probePorts",
  "previewOpen",
  "previewNav",
  "previewBounds",
  "previewHide",
  "previewClose",
  "artifactShow",
  "artifactBounds",
  "artifactHide",
  "artifactClose",
  "browserView",
  "setAppIcon",
  "projectIcon",
] as const satisfies ReadonlyArray<keyof DeckHost>;

export type HostMethod = (typeof HOST_METHODS)[number];

export const SOCKET_EVENT_CHANNEL = "deck:socket";
export const RPC_EVENT_CHANNEL = "deck:rpc";
export const MENU_EVENT_CHANNEL = "deck:menu";
export const LINK_EVENT_CHANNEL = "deck:link";
export const UPDATE_EVENT_CHANNEL = "deck:update";
export const NOTIFICATION_EVENT_CHANNEL = "deck:notification-click";
export const PREVIEW_EVENT_CHANNEL = "deck:preview";
export const ARTIFACT_EVENT_CHANNEL = "deck:artifact";

export function hostChannel(m: HostMethod): string {
  return `deck:${m}`;
}
