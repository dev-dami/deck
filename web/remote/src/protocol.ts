// Mirrors internal/remote/wire/wire.go. Keep in lockstep; the golden fixtures in
// wire/testdata are parsed by protocol.test.ts so drift fails the tests.

export const PROTOCOL_VERSION = 1;

export const MAX_IMAGE_BYTES = 8 << 20;
export const MAX_FRAME_BYTES = 12 << 20;

export const CLOSE_UNAUTHORIZED = 4401;
export const CLOSE_REVOKED = 4403;
export const CLOSE_SLOW_CONSUMER = 4408;

export const Code = {
  Unauthorized: "unauthorized",
  Forbidden: "forbidden",
  NotFound: "not_found",
  Invalid: "invalid",
  YoloDisabled: "yolo_disabled",
  Busy: "busy",
  TooLarge: "too_large",
  Internal: "internal",
} as const;

export type Mode = "plan" | "ask" | "yolo";
export type Decision = "allow" | "allow_session" | "deny";
export type ApprovalKind = "command" | "edit" | "other";
export type Role = "user" | "assistant" | "system";

export interface Resume {
  epoch: string;
  seq: number;
}

export interface ServerInfo {
  version: string;
  project: string;
  cwd: string;
  kinds: string[];
  allow_yolo: boolean;
  features?: string[];
  server_id?: string;
  protocol: number;
  headless: boolean;
  tunnel?: string;
  // VAPID public key (base64url); empty or absent when the server cannot push.
  push_key?: string;
}

export interface DeviceRef {
  id: string;
  name: string;
}

export interface WireError {
  code: string;
  msg: string;
}

export interface PairRequest {
  code: string;
  device_name: string;
}

export interface PairResponse {
  token: string;
  device_id: string;
  server_id?: string;
}

export interface Session {
  id: string;
  title: string;
  updated_at: number;
  busy: boolean;
  pending: number;
  last_text?: string;
}

export interface Part {
  type: string;
  text?: string;
  id?: string;
  name?: string;
  summary?: string;
  tool_call_id?: string;
  is_error?: boolean;
  output?: string;
  truncated?: boolean;
  diff?: string;
  mime?: string;
}

export interface Message {
  id: string;
  session_id: string;
  role: Role;
  agent?: string;
  created_at: number;
  updated_at: number;
  finished: boolean;
  // True for a user message that message.edit may change.
  editable?: boolean;
  // True once the user has rewritten the message.
  edited?: boolean;
  parts: Part[];
}

export interface Approval {
  id: string;
  session_id: string;
  tool_call_id?: string;
  agent?: string;
  kind: ApprovalKind;
  description: string;
  command?: string;
  cwd?: string;
  path?: string;
  diff?: string;
  raw?: string;
  created_at: number;
}

export interface QueuedItem {
  id: string;
  agent: string;
  text: string;
  at: number;
}

export interface AgentLine {
  name: string;
  kind: string;
  model?: string;
  effort?: string;
  busy: boolean;
}

export interface ModelChoice {
  id: string;
  label: string;
  efforts?: string[];
  selected?: boolean;
}

export interface ChangedFile {
  path: string;
  added: number;
  removed: number;
  diff?: string;
  truncated?: boolean;
}

export interface Device {
  id: string;
  name: string;
  created_at: number;
  last_seen: number;
  current?: boolean;
}

export interface ImageUpload {
  name: string;
  mime: string;
  data_b64: string;
}

// The browser's PushSubscription.toJSON() shape.
export interface PushSubscriptionWire {
  endpoint: string;
  keys: { p256dh: string; auth: string };
}

export interface PushStatus {
  available: boolean;
  enabled: boolean;
  approvals: boolean;
  done: boolean;
}

export interface PushSubscribeReq {
  subscription: PushSubscriptionWire;
  approvals: boolean;
  done: boolean;
}

// ---- frames ---------------------------------------------------------------

export interface HelloFrame {
  t: "hello";
  v: number;
  token: string;
  resume?: Resume;
}

export interface WelcomeFrame {
  t: "welcome";
  epoch?: string;
  replay?: boolean;
  server?: ServerInfo;
  device?: DeviceRef;
}

export interface ReqFrame {
  t: "req";
  id: string;
  m: string;
  p?: unknown;
}

export interface ResOkFrame {
  t: "res";
  id: string;
  ok: true;
  r?: unknown;
}

export interface ResErrFrame {
  t: "res";
  id: string;
  ok: false;
  err: WireError;
}

export type ResFrame = ResOkFrame | ResErrFrame;

export interface EvFrame {
  t: "ev";
  seq: number;
  e: string;
  p?: unknown;
}

export interface ResyncFrame {
  t: "resync";
}

export type ServerFrame = WelcomeFrame | ResFrame | EvFrame | ResyncFrame;

// ---- primitive guards -----------------------------------------------------

export function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

const isStr = (v: unknown): v is string => typeof v === "string";
const isNum = (v: unknown): v is number => typeof v === "number" && Number.isFinite(v);
const isBool = (v: unknown): v is boolean => typeof v === "boolean";

// Go omits zero values (omitempty), so an optional field is absent or well-typed.
function optional(r: Record<string, unknown>, key: string, ok: (v: unknown) => boolean): boolean {
  const v = r[key];
  return v === undefined || ok(v);
}

const isStrArray = (v: unknown): v is string[] => Array.isArray(v) && v.every(isStr);

// Go marshals a nil slice as null.
function nullableArray(v: unknown): unknown[] | null {
  if (v === null || v === undefined) return [];
  return Array.isArray(v) ? v : null;
}

// ---- entity guards --------------------------------------------------------

export function isWireError(v: unknown): v is WireError {
  return isRecord(v) && isStr(v["code"]) && isStr(v["msg"]);
}

export function isPairRequest(v: unknown): v is PairRequest {
  return isRecord(v) && isStr(v["code"]) && isStr(v["device_name"]);
}

export function isPairResponse(v: unknown): v is PairResponse {
  return (
    isRecord(v) &&
    isStr(v["token"]) &&
    v["token"] !== "" &&
    isStr(v["device_id"]) &&
    optional(v, "server_id", isStr)
  );
}

export function isServerInfo(v: unknown): v is ServerInfo {
  return (
    isRecord(v) &&
    isStr(v["version"]) &&
    isStr(v["project"]) &&
    isStr(v["cwd"]) &&
    isStrArray(v["kinds"]) &&
    isBool(v["allow_yolo"]) &&
    isNum(v["protocol"]) &&
    isBool(v["headless"]) &&
    optional(v, "features", isStrArray) &&
    optional(v, "server_id", isStr) &&
    optional(v, "tunnel", isStr) &&
    optional(v, "push_key", isStr)
  );
}

export function isDeviceRef(v: unknown): v is DeviceRef {
  return isRecord(v) && isStr(v["id"]) && isStr(v["name"]);
}

export function isSession(v: unknown): v is Session {
  return (
    isRecord(v) &&
    isStr(v["id"]) &&
    isStr(v["title"]) &&
    isNum(v["updated_at"]) &&
    isBool(v["busy"]) &&
    isNum(v["pending"]) &&
    optional(v, "last_text", isStr)
  );
}

export function isPart(v: unknown): v is Part {
  return (
    isRecord(v) &&
    isStr(v["type"]) &&
    optional(v, "text", isStr) &&
    optional(v, "id", isStr) &&
    optional(v, "name", isStr) &&
    optional(v, "summary", isStr) &&
    optional(v, "tool_call_id", isStr) &&
    optional(v, "is_error", isBool) &&
    optional(v, "output", isStr) &&
    optional(v, "truncated", isBool) &&
    optional(v, "diff", isStr) &&
    optional(v, "mime", isStr)
  );
}

export function isRole(v: unknown): v is Role {
  return v === "user" || v === "assistant" || v === "system";
}

export function isMessage(v: unknown): v is Message {
  return (
    isRecord(v) &&
    isStr(v["id"]) &&
    isStr(v["session_id"]) &&
    isRole(v["role"]) &&
    optional(v, "agent", isStr) &&
    isNum(v["created_at"]) &&
    isNum(v["updated_at"]) &&
    isBool(v["finished"]) &&
    optional(v, "editable", isBool) &&
    optional(v, "edited", isBool) &&
    Array.isArray(v["parts"]) &&
    v["parts"].every(isPart)
  );
}

// Go marshals a message with no parts as "parts":null; treat that as empty.
export function toMessage(v: unknown): Message | null {
  if (isMessage(v)) return v;
  if (isRecord(v) && v["parts"] === null) {
    const withParts = { ...v, parts: [] };
    return isMessage(withParts) ? withParts : null;
  }
  return null;
}

export function isApprovalKind(v: unknown): v is ApprovalKind {
  return v === "command" || v === "edit" || v === "other";
}

export function isApproval(v: unknown): v is Approval {
  return (
    isRecord(v) &&
    isStr(v["id"]) &&
    isStr(v["session_id"]) &&
    isApprovalKind(v["kind"]) &&
    isStr(v["description"]) &&
    isNum(v["created_at"]) &&
    optional(v, "tool_call_id", isStr) &&
    optional(v, "agent", isStr) &&
    optional(v, "command", isStr) &&
    optional(v, "cwd", isStr) &&
    optional(v, "path", isStr) &&
    optional(v, "diff", isStr) &&
    optional(v, "raw", isStr)
  );
}

export function isQueuedItem(v: unknown): v is QueuedItem {
  return isRecord(v) && isStr(v["id"]) && isStr(v["agent"]) && isStr(v["text"]) && isNum(v["at"]);
}

export function isAgentLine(v: unknown): v is AgentLine {
  return (
    isRecord(v) &&
    isStr(v["name"]) &&
    isStr(v["kind"]) &&
    isBool(v["busy"]) &&
    optional(v, "model", isStr) &&
    optional(v, "effort", isStr)
  );
}

export function isModelChoice(v: unknown): v is ModelChoice {
  return (
    isRecord(v) &&
    isStr(v["id"]) &&
    isStr(v["label"]) &&
    optional(v, "efforts", isStrArray) &&
    optional(v, "selected", isBool)
  );
}

export function isChangedFile(v: unknown): v is ChangedFile {
  return (
    isRecord(v) &&
    isStr(v["path"]) &&
    isNum(v["added"]) &&
    isNum(v["removed"]) &&
    optional(v, "diff", isStr) &&
    optional(v, "truncated", isBool)
  );
}

export function isDevice(v: unknown): v is Device {
  return (
    isRecord(v) &&
    isStr(v["id"]) &&
    isStr(v["name"]) &&
    isNum(v["created_at"]) &&
    isNum(v["last_seen"]) &&
    optional(v, "current", isBool)
  );
}

export function isPushStatus(v: unknown): v is PushStatus {
  return (
    isRecord(v) &&
    isBool(v["available"]) &&
    isBool(v["enabled"]) &&
    isBool(v["approvals"]) &&
    isBool(v["done"])
  );
}

export function isMode(v: unknown): v is Mode {
  return v === "plan" || v === "ask" || v === "yolo";
}

// ---- frames ---------------------------------------------------------------

export function isHelloFrame(v: unknown): v is HelloFrame {
  return (
    isRecord(v) &&
    v["t"] === "hello" &&
    isNum(v["v"]) &&
    isStr(v["token"]) &&
    optional(v, "resume", (r) => isRecord(r) && isStr(r["epoch"]) && isNum(r["seq"]))
  );
}

export function isReqFrame(v: unknown): v is ReqFrame {
  return isRecord(v) && v["t"] === "req" && isStr(v["id"]) && isStr(v["m"]);
}

export function isWelcomeFrame(v: unknown): v is WelcomeFrame {
  return (
    isRecord(v) &&
    v["t"] === "welcome" &&
    optional(v, "epoch", isStr) &&
    optional(v, "replay", isBool) &&
    optional(v, "server", isServerInfo) &&
    optional(v, "device", isDeviceRef)
  );
}

export function isResFrame(v: unknown): v is ResFrame {
  if (!isRecord(v) || v["t"] !== "res" || !isStr(v["id"])) return false;
  if (v["ok"] === true) return true;
  return v["ok"] === false && isWireError(v["err"]);
}

export function isEvFrame(v: unknown): v is EvFrame {
  return isRecord(v) && v["t"] === "ev" && isNum(v["seq"]) && isStr(v["e"]);
}

export function isResyncFrame(v: unknown): v is ResyncFrame {
  return isRecord(v) && v["t"] === "resync";
}

export function parseServerFrame(text: string): ServerFrame | null {
  let v: unknown;
  try {
    v = JSON.parse(text);
  } catch {
    return null;
  }
  if (isWelcomeFrame(v) || isResFrame(v) || isEvFrame(v) || isResyncFrame(v)) return v;
  return null;
}

// ---- events ---------------------------------------------------------------

export type ServerEvent =
  | { e: "session.created"; p: Session }
  | { e: "session.updated"; p: Session }
  | { e: "session.deleted"; p: { id: string } }
  | { e: "message.created"; p: Message }
  | { e: "message.updated"; p: Message }
  | { e: "message.deleted"; p: { id: string; session_id?: string } }
  | { e: "approval.requested"; p: Approval }
  | { e: "approval.resolved"; p: { id: string } }
  | { e: "busy"; p: { session_id: string; busy: boolean } }
  | { e: "roster.changed"; p: { session_id: string } }
  | { e: "queue.changed"; p: { session_id: string; items: QueuedItem[] } }
  | { e: "mode.changed"; p: { mode: Mode } }
  | { e: "notice"; p: { session_id?: string; text: string } }
  | { e: "device.revoked"; p: Record<string, never> };

// session.deleted / message.deleted payloads are not pinned down by wire.go;
// accept both {id} and {session_id} for sessions.
function deletedId(p: unknown): string | null {
  if (!isRecord(p)) return null;
  if (isStr(p["id"])) return p["id"];
  if (isStr(p["session_id"])) return p["session_id"];
  return null;
}

export function parseEvent(e: string, p: unknown): ServerEvent | null {
  switch (e) {
    case "session.created":
    case "session.updated":
      return isSession(p) ? { e, p } : null;
    case "session.deleted": {
      const id = deletedId(p);
      return id === null ? null : { e, p: { id } };
    }
    case "message.created":
    case "message.updated": {
      const m = toMessage(p);
      return m === null ? null : { e, p: m };
    }
    case "message.deleted": {
      if (!isRecord(p) || !isStr(p["id"])) return null;
      return isStr(p["session_id"])
        ? { e, p: { id: p["id"], session_id: p["session_id"] } }
        : { e, p: { id: p["id"] } };
    }
    case "approval.requested":
      return isApproval(p) ? { e, p } : null;
    case "approval.resolved":
      return isRecord(p) && isStr(p["id"]) ? { e, p: { id: p["id"] } } : null;
    case "busy":
      return isRecord(p) && isStr(p["session_id"]) && isBool(p["busy"])
        ? { e, p: { session_id: p["session_id"], busy: p["busy"] } }
        : null;
    case "roster.changed":
      return isRecord(p) && isStr(p["session_id"])
        ? { e, p: { session_id: p["session_id"] } }
        : null;
    case "queue.changed": {
      if (!isRecord(p) || !isStr(p["session_id"])) return null;
      const items = nullableArray(p["items"]);
      if (items === null || !items.every(isQueuedItem)) return null;
      return { e, p: { session_id: p["session_id"], items } };
    }
    case "mode.changed":
      return isRecord(p) && isMode(p["mode"]) ? { e, p: { mode: p["mode"] } } : null;
    case "notice": {
      if (!isRecord(p) || !isStr(p["text"])) return null;
      return isStr(p["session_id"])
        ? { e, p: { session_id: p["session_id"], text: p["text"] } }
        : { e, p: { text: p["text"] } };
    }
    case "device.revoked":
      return { e, p: {} };
    default:
      return null;
  }
}

// ---- methods --------------------------------------------------------------

export interface ChatSendReq {
  session_id: string;
  text: string;
  images?: ImageUpload[];
  idempotency_key?: string;
}

export interface ModelsSetReq {
  session_id: string;
  kind: string;
  model: string;
  effort?: string;
}

export interface MethodTable {
  "sessions.list": { req: Record<string, never>; res: Session[] };
  "sessions.create": { req: { title?: string }; res: Session };
  "sessions.rename": { req: { id: string; title: string }; res: true };
  "sessions.delete": { req: { id: string }; res: true };
  "sessions.focus": { req: { id: string }; res: true };
  "messages.list": {
    req: { session_id: string; before?: number; limit?: number };
    res: Message[];
  };
  "chat.send": { req: ChatSendReq; res: true };
  "message.edit": { req: { session_id: string; message_id: string; text: string }; res: true };
  "push.status": { req: Record<string, never>; res: PushStatus };
  "push.subscribe": { req: PushSubscribeReq; res: true };
  "push.unsubscribe": { req: Record<string, never>; res: true };
  "push.test": { req: Record<string, never>; res: true };
  "chat.cancel": { req: { session_id: string }; res: true };
  "chat.continue": { req: { session_id: string }; res: true };
  "agent.interrupt": { req: { session_id: string; agent: string }; res: true };
  "queue.list": { req: { session_id: string }; res: QueuedItem[] };
  "queue.steer": { req: { item_id: string }; res: true };
  "queue.cancel": { req: { item_id: string }; res: true };
  "roster.get": { req: { session_id: string }; res: AgentLine[] };
  "models.list": { req: { session_id: string; kind: string }; res: ModelChoice[] };
  "models.set": { req: ModelsSetReq; res: true };
  "mode.get": { req: Record<string, never>; res: Mode };
  "mode.set": { req: { mode: Mode }; res: true };
  "approvals.list": { req: Record<string, never>; res: Approval[] };
  "approvals.answer": { req: { id: string; decision: Decision }; res: { resolved: boolean } };
  "changes.get": { req: { session_id: string }; res: ChangedFile[] };
  "devices.list": { req: Record<string, never>; res: Device[] };
  "devices.revoke": { req: { id: string }; res: true };
}

export type MethodName = keyof MethodTable;

// wire.go does not define result envelopes for list methods. Accept a bare
// array or a single-array-field object ({"sessions":[...]}) so either server
// choice works; a nil Go slice (null/absent) is an empty list.
function listOf<T>(
  r: unknown,
  guard: (v: unknown) => v is T,
  coerce?: (v: unknown) => T | null,
): T[] | null {
  let arr = nullableArray(r);
  if (arr === null && isRecord(r)) {
    const arrays = Object.values(r).filter(Array.isArray);
    if (arrays.length === 1) arr = arrays[0] ?? null;
  }
  if (arr === null) return null;
  const out: T[] = [];
  for (const item of arr) {
    const v = guard(item) ? item : (coerce?.(item) ?? null);
    if (v === null) return null;
    out.push(v);
  }
  return out;
}

function modeOf(r: unknown): Mode | null {
  if (isMode(r)) return r;
  return isRecord(r) && isMode(r["mode"]) ? r["mode"] : null;
}

function singleOf<T>(r: unknown, guard: (v: unknown) => v is T, key: string): T | null {
  if (guard(r)) return r;
  if (isRecord(r) && guard(r[key])) return r[key];
  return null;
}

export type Decoded<T> = { ok: true; value: T } | { ok: false };
type Decoders = { [M in MethodName]: (r: unknown) => Decoded<MethodTable[M]["res"]> };

const fail = { ok: false } as const;
const nothing: Decoded<true> = { ok: true, value: true };

function from<T>(v: T | null): Decoded<T> {
  return v === null ? fail : { ok: true, value: v };
}

const list =
  <T>(guard: (v: unknown) => v is T) =>
  (r: unknown): Decoded<T[]> =>
    from(listOf(r, guard));

// Acknowledgement-only methods ignore their result body.
const ack = (): Decoded<true> => nothing;

export const DECODERS: Decoders = {
  "sessions.list": list(isSession),
  "sessions.create": (r) => from(singleOf(r, isSession, "session")),
  "sessions.rename": ack,
  "sessions.delete": ack,
  "sessions.focus": ack,
  "messages.list": (r) => from(listOf(r, isMessage, toMessage)),
  "chat.send": ack,
  "message.edit": ack,
  "push.status": (r) => (isPushStatus(r) ? { ok: true, value: r } : fail),
  "push.subscribe": ack,
  "push.unsubscribe": ack,
  "push.test": ack,
  "chat.cancel": ack,
  "chat.continue": ack,
  "agent.interrupt": ack,
  "queue.list": list(isQueuedItem),
  "queue.steer": ack,
  "queue.cancel": ack,
  "roster.get": list(isAgentLine),
  "models.list": list(isModelChoice),
  "models.set": ack,
  "mode.get": (r) => from(modeOf(r)),
  "mode.set": ack,
  "approvals.list": list(isApproval),
  "approvals.answer": (r) =>
    isRecord(r) && isBool(r["resolved"]) ? { ok: true, value: { resolved: r["resolved"] } } : fail,
  "changes.get": list(isChangedFile),
  "devices.list": list(isDevice),
  "devices.revoke": ack,
};
