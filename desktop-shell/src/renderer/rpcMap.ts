import {
  type AgentLine,
  type Approval,
  type ChangedFile,
  decodeAgentLine,
  decodeApproval,
  decodeChangedFile,
  decodeMessage,
  decodeModelChoice,
  decodeQueuedItem,
  decodeSession,
  decodeTheme,
  type Decoder,
  isRecord,
  type Message,
  type ModelChoice,
  parseEvent,
  type QueuedItem,
  type ServerEvent,
  type Session,
  type Theme,
} from "@web/protocol";
import type {
  AgentSummary,
  BrowserActionResponse,
  BrowserErrorCode,
  BrowserStateResponse,
  BrowserTab,
  HighlightItem,
  HighlightKind,
  HighlightSource,
  HighlightsResponse,
  HighlightState,
  OpResult,
} from "../shared/bridge";
import type { Profile } from "./profiles";
import { ATTENTION, type Attention, type Lease, type RoomEvent, type RoomUpdate } from "./roomMeta";

// deck.v1 messages (proto/deck/v1/deck.proto, as plain objects from main:
// snake_case fields, int64 as numbers, every field present) mapped to the
// wire entities the reducer keeps. The differences: times are Unix
// milliseconds, empty strings mean absent, and nothing is truncated. Each
// result goes through the shared guard, so the state holds only what a v2
// server could also have sent.

type Obj = Record<string, unknown>;

const s = (v: unknown): string => (typeof v === "string" ? v : "");
const n = (v: unknown): number => (typeof v === "number" && Number.isFinite(v) ? v : 0);

export function isoOf(ms: unknown): string {
  return new Date(n(ms)).toISOString();
}

// Copies the named string fields that are not empty.
function strings(from: Obj, keys: readonly string[]): Obj {
  const out: Obj = {};
  for (const k of keys) if (s(from[k]) !== "") out[k] = from[k];
  return out;
}

function mapSession(p: Obj): unknown {
  return {
    id: s(p["id"]),
    title: s(p["title"]),
    updated_at: isoOf(p["updated_at"]),
    busy: p["busy"] === true,
    pending: n(p["pending"]),
    mode: s(p["mode"]),
    ...strings(p, ["last_text"]),
  };
}

function mapPart(p: Obj): unknown {
  const out: Obj = {
    type: s(p["type"]),
    ...strings(p, ["text", "id", "name", "summary", "tool_call_id", "output", "diff"]),
  };
  if (p["is_error"] === true) out["is_error"] = true;
  const img = p["image"];
  if (isRecord(img)) out["image"] = { name: s(img["name"]) || "image", mime: s(img["mime"]), ...strings(img, ["label"]) };
  return out;
}

function mapMessage(p: Obj): unknown {
  const out: Obj = {
    id: s(p["id"]),
    session_id: s(p["session_id"]),
    role: s(p["role"]),
    created_at: isoOf(p["created_at"]),
    updated_at: isoOf(p["updated_at"]),
    finished: p["finished"] === true,
    parts: Array.isArray(p["parts"]) ? p["parts"].filter(isRecord).map(mapPart) : [],
    ...strings(p, ["agent", "turn_end"]),
  };
  if (p["editable"] === true) out["editable"] = true;
  if (p["edited"] === true) out["edited"] = true;
  return out;
}

function mapQuestion(q: Obj): unknown {
  const out: Obj = { id: s(q["id"]), text: s(q["text"]), ...strings(q, ["header"]) };
  const opts = Array.isArray(q["options"]) ? q["options"].filter(isRecord) : [];
  if (opts.length > 0) out["options"] = opts.map((o) => ({ label: s(o["label"]), ...strings(o, ["description"]) }));
  if (q["multi_select"] === true) out["multi_select"] = true;
  if (q["other"] === true) out["other"] = true;
  return out;
}

function mapApproval(p: Obj): unknown {
  const out: Obj = {
    id: s(p["id"]),
    session_id: s(p["session_id"]),
    agent: s(p["agent"]),
    kind: s(p["kind"]),
    description: s(p["description"]),
    created_at: isoOf(p["created_at"]),
    ...strings(p, ["tool_call_id", "command", "path", "diff", "raw", "origin"]),
  };
  const qs = Array.isArray(p["questions"]) ? p["questions"].filter(isRecord) : [];
  if (qs.length > 0) out["questions"] = qs.map(mapQuestion);
  return out;
}

function mapQueued(p: Obj): unknown {
  return { id: s(p["id"]), agent: s(p["agent"]), text: s(p["text"]), at: isoOf(p["at"]) };
}

function mapAgent(p: Obj): unknown {
  return {
    id: s(p["id"]),
    name: s(p["name"]) || s(p["id"]),
    kind: s(p["kind"]),
    busy: p["busy"] === true,
    ...strings(p, ["model", "effort", "profile", "role", "color"]),
  };
}

function mapModel(p: Obj): unknown {
  const out: Obj = { id: s(p["id"]), label: s(p["label"]) || s(p["id"]) };
  if (Array.isArray(p["efforts"]) && p["efforts"].length > 0) out["efforts"] = p["efforts"];
  if (p["selected"] === true) out["selected"] = true;
  if (p["video"] === true) out["video"] = true;
  return out;
}

function mapChanged(p: Obj): unknown {
  return { path: s(p["path"]), added: n(p["added"]), removed: n(p["removed"]), ...strings(p, ["diff"]) };
}

function mapTheme(p: Obj): unknown {
  const kinds: Obj = {};
  if (isRecord(p["kinds"])) {
    for (const [k, v] of Object.entries(p["kinds"])) {
      if (isRecord(v)) kinds[k] = { tint: s(v["tint"]), mark: s(v["mark"]) };
    }
  }
  return { palette: isRecord(p["palette"]) ? p["palette"] : {}, kinds };
}

function list<T>(v: unknown, map: (p: Obj) => unknown, decode: Decoder<T>): T[] {
  if (!Array.isArray(v)) return [];
  const out: T[] = [];
  for (const item of v) {
    const x = isRecord(item) ? decode(map(item)) : null;
    if (x !== null) out.push(x);
  }
  return out;
}

const field = (v: unknown, k: string): unknown => (isRecord(v) ? v[k] : undefined);

export const toSession = (v: unknown): Session | null => (isRecord(v) ? decodeSession(mapSession(v)) : null);
export const toSessions = (r: unknown): Session[] => list(field(r, "sessions"), mapSession, decodeSession);
export const toMessages = (r: unknown): Message[] => list(field(r, "messages"), mapMessage, decodeMessage);
export const toApprovals = (r: unknown): Approval[] => list(field(r, "approvals"), mapApproval, decodeApproval);
export const toQueue = (r: unknown): QueuedItem[] => list(field(r, "items"), mapQueued, decodeQueuedItem);
export const toRoster = (r: unknown): AgentLine[] => list(field(r, "agents"), mapAgent, decodeAgentLine);
export const toModels = (r: unknown): ModelChoice[] => list(field(r, "models"), mapModel, decodeModelChoice);
export const toChanges = (r: unknown): ChangedFile[] => list(field(r, "files"), mapChanged, decodeChangedFile);
export const toTheme = (v: unknown): Theme | null => (isRecord(v) ? decodeTheme(mapTheme(v)) : null);

// ---- Highlights and the browser (live.ts): the deck.v1 messages as the
// bridge types them. A kind or state this app does not know is dropped.

const HIGHLIGHT_KINDS: readonly string[] = ["needs_you", "blocker", "finding"];
const HIGHLIGHT_STATES: readonly string[] = ["open", "resolved", "dismissed"];

function toSource(v: unknown): HighlightSource | null {
  if (!isRecord(v)) return null;
  const src = { message_id: s(v["message_id"]), task_id: s(v["task_id"]), turn_id: s(v["turn_id"]) };
  return src.message_id === "" && src.task_id === "" && src.turn_id === "" ? null : src;
}

export function toHighlightItem(v: unknown): HighlightItem | null {
  if (!isRecord(v) || s(v["id"]) === "") return null;
  const kind = s(v["kind"]);
  const state = s(v["state"]);
  if (!HIGHLIGHT_KINDS.includes(kind) || !HIGHLIGHT_STATES.includes(state)) return null;
  return {
    id: s(v["id"]),
    session_id: s(v["session_id"]),
    agent: s(v["agent"]),
    key: s(v["key"]),
    kind: kind as HighlightKind,
    title: s(v["title"]),
    detail: s(v["detail"]),
    source: toSource(v["source"]),
    state: state as HighlightState,
    created_at: n(v["created_at"]),
    updated_at: n(v["updated_at"]),
    unread: v["unread"] === true,
  };
}

export function toSummary(v: unknown): AgentSummary | null {
  if (!isRecord(v) || s(v["agent"]) === "") return null;
  return { session_id: s(v["session_id"]), agent: s(v["agent"]), text: s(v["text"]), source: toSource(v["source"]), updated_at: n(v["updated_at"]) };
}

export function toHighlights(r: unknown): HighlightsResponse {
  const items = field(r, "items");
  const summaries = field(r, "summaries");
  return {
    items: Array.isArray(items) ? items.map(toHighlightItem).filter((x): x is HighlightItem => x !== null) : [],
    summaries: Array.isArray(summaries) ? summaries.map(toSummary).filter((x): x is AgentSummary => x !== null) : [],
  };
}

function toTab(v: unknown): BrowserTab | null {
  if (!isRecord(v) || s(v["id"]) === "") return null;
  return {
    id: s(v["id"]),
    session_id: s(v["session_id"]),
    owner: s(v["owner"]),
    url: s(v["url"]),
    title: s(v["title"]),
    loading: v["loading"] === true,
    can_back: v["can_back"] === true,
    can_forward: v["can_forward"] === true,
    user_in_control: v["user_in_control"] === true,
    snapshot_version: s(v["snapshot_version"]),
    activity: s(v["activity"]),
    asleep: v["asleep"] === true,
  };
}

export function toBrowserTab(v: unknown): BrowserTab | null {
  return toTab(v);
}

// The room the state is about comes from session_id, which is present even
// when the room has no tab left.
export function toBrowserState(r: unknown): (BrowserStateResponse & { session_id: string }) | null {
  if (!isRecord(r)) return null;
  const tabs = r["tabs"];
  return {
    session_id: s(r["session_id"]),
    available: r["available"] === true,
    tabs: Array.isArray(tabs) ? tabs.map(toTab).filter((x): x is BrowserTab => x !== null) : [],
  };
}

export function toBrowserActionResponse(r: unknown): BrowserActionResponse | null {
  if (!isRecord(r)) return null;
  const res = r["result"];
  const images = isRecord(res) && Array.isArray(res["images"]) ? res["images"] : [];
  const result: OpResult | null = isRecord(res)
    ? {
        text: s(res["text"]),
        is_error: res["is_error"] === true,
        images: images.flatMap((im) =>
          isRecord(im) && (im["mime_type"] === "image/png" || im["mime_type"] === "image/jpeg") && im["data"] instanceof Uint8Array
            ? [{ mime_type: im["mime_type"], data: im["data"] }]
            : [],
        ),
      }
    : null;
  return { tab: toTab(r["tab"]), result, error_code: s(r["error_code"]) as BrowserErrorCode | "" };
}

export interface Ready {
  epoch: string;
  seq: number;
  resync: boolean;
}

// An event, with what it says of a room beyond the shared state when it
// says anything (roomMeta.ts); a lease change is about the room alone.
export type RpcEvent =
  | { k: "ready"; ready: Ready }
  | { k: "event"; seq: number; event: ServerEvent; room?: RoomEvent }
  | { k: "room"; seq: number; room: RoomEvent }
  | { k: "live"; seq: number; live: LiveEvent };

// What Highlights and the browser say as it happens (live.ts).
export type LiveEvent =
  | { k: "highlight"; item: HighlightItem }
  | { k: "summary"; summary: AgentSummary }
  | { k: "browser"; state: BrowserStateResponse & { session_id: string } };

// The v2 name and payload of each deck.v1.Event kind.
const EVENTS: Record<string, { e: string; map: (p: Obj) => unknown }> = {
  session_created: { e: "session.created", map: mapSession },
  session_updated: { e: "session.updated", map: mapSession },
  session_deleted: { e: "session.deleted", map: mapSession },
  message_created: { e: "message.created", map: mapMessage },
  message_updated: { e: "message.updated", map: mapMessage },
  message_deleted: { e: "message.deleted", map: (p) => ({ id: s(p["id"]), session_id: s(p["session_id"]) }) },
  approval_requested: { e: "approval.requested", map: mapApproval },
  approval_resolved: {
    e: "approval.resolved",
    map: (p) => ({ id: s(p["id"]), session_id: s(p["session_id"]), granted: p["granted"] === true }),
  },
  busy: { e: "busy", map: (p) => ({ session_id: s(p["session_id"]), busy: p["busy"] === true }) },
  queue_changed: {
    e: "queue.changed",
    map: (p) => ({ session_id: s(p["session_id"]), items: Array.isArray(p["items"]) ? p["items"].filter(isRecord).map(mapQueued) : [] }),
  },
  roster_changed: { e: "roster.changed", map: (p) => ({ session_id: s(p["session_id"]) }) },
  mode_changed: { e: "mode.changed", map: (p) => ({ session_id: s(p["session_id"]), mode: s(p["mode"]) }) },
  notice: { e: "notice", map: (p) => ({ session_id: s(p["session_id"]), text: s(p["text"]) }) },
  theme_changed: { e: "theme.changed", map: mapTheme },
};

// One deck.v1.Event: the Ready that opens a stream, or an event as the
// reducer knows it. Null for a kind this app does not know or a payload
// that fails the guard.
export function toEvent(v: unknown): RpcEvent | null {
  if (!isRecord(v)) return null;
  const kind = s(v["kind"]);
  const payload = v[kind];
  if (!isRecord(payload)) return null;
  if (kind === "ready") {
    return { k: "ready", ready: { epoch: s(payload["epoch"]), seq: n(payload["seq"]), resync: payload["resync"] === true } };
  }
  const seq = n(v["seq"]);
  if (kind === "lease_changed") {
    const room = toLeaseEvent(payload);
    return room === null ? null : { k: "room", seq, room };
  }
  if (kind === "highlight_changed") {
    const item = toHighlightItem(payload);
    return item === null ? null : { k: "live", seq, live: { k: "highlight", item } };
  }
  if (kind === "summary_changed") {
    const summary = toSummary(payload);
    return summary === null ? null : { k: "live", seq, live: { k: "summary", summary } };
  }
  if (kind === "browser_changed") {
    const state = toBrowserState(payload);
    return state === null || state.session_id === "" ? null : { k: "live", seq, live: { k: "browser", state } };
  }
  const spec = EVENTS[kind];
  if (spec === undefined) return null;
  const event = parseEvent(spec.e, spec.map(payload));
  if (event === null) return null;
  const room = roomOf(kind, payload);
  return room === null ? { k: "event", seq, event } : { k: "event", seq, event, room };
}

// ---- rooms (roomMeta.ts): desktop-side guards for what deck.v1 says
// beyond the shared entities.

const attentionOf = (v: unknown): Attention => ((ATTENTION as readonly unknown[]).includes(v) ? (v as Attention) : "");

const stringList = (v: unknown): string[] | null =>
  Array.isArray(v) && v.every((x) => typeof x === "string") ? (v as string[]) : null;

// A deck.v1 Session's room fields; null without an id.
export function toRoom(v: unknown): RoomUpdate | null {
  if (!isRecord(v) || s(v["id"]) === "") return null;
  const l = v["lead"];
  const lead = isRecord(l) && s(l["kind"]) !== "" ? { agent: s(l["agent"]), kind: s(l["kind"]), model: s(l["model"]), slug: s(l["slug"]), effort: s(l["effort"]), profile: s(l["profile"]), video: l["video"] === true } : null;
  return { k: "session", id: s(v["id"]), attention: attentionOf(v["attention"]), branch: s(v["branch"]), archived: v["archived"] === true, parent: s(v["parent_id"]), lead };
}

export function toRooms(r: unknown): RoomUpdate[] {
  const v = field(r, "sessions");
  if (!Array.isArray(v)) return [];
  return v.map(toRoom).filter((x): x is RoomUpdate => x !== null);
}

function toLeaseEvent(p: Obj): RoomEvent | null {
  const paths = stringList(p["paths"]);
  if (s(p["session_id"]) === "" || s(p["agent"]) === "" || paths === null || paths.length === 0) return null;
  const lease: Lease = { agent: s(p["agent"]), paths, exclusive: p["exclusive"] === true };
  return { k: "lease", session: s(p["session_id"]), lease, released: p["released"] === true };
}

function roomOf(kind: string, p: Obj): RoomEvent | null {
  switch (kind) {
    case "session_created":
    case "session_updated":
      return toRoom(p);
    case "session_deleted":
      return s(p["id"]) === "" ? null : { k: "deleted", id: s(p["id"]) };
    case "notice":
      return s(p["via"]) === "" ? null : { k: "note", session: s(p["session_id"]), note: { text: s(p["text"]), via: s(p["via"]) } };
  }
  return null;
}

export interface FileList {
  paths: string[];
  truncated: boolean;
}

export const toFiles = (r: unknown): string[] => stringList(field(r, "files")) ?? [];

export const toFileList = (r: unknown): FileList => ({ paths: stringList(field(r, "paths")) ?? [], truncated: field(r, "truncated") === true });

export const toCapabilities = (info: unknown): string[] => stringList(field(info, "capabilities")) ?? [];

// ReadImage's answer: the bytes come through IPC as a Uint8Array.
export interface ImageBytes {
  data: Uint8Array;
  mime: string;
}

export function toImage(r: unknown): ImageBytes | null {
  const data = field(r, "data");
  const mime = s(field(r, "mime"));
  return data instanceof Uint8Array && data.length > 0 && mime.startsWith("image/") ? { data, mime } : null;
}

// One message SearchMessages found; at is Unix ms.
export interface SearchHit {
  session_id: string;
  message_id: string;
  agent: string;
  snippet: string;
  at: number;
}

// ListProfiles' answer; a profile with no name or kind is dropped.
export function toProfiles(r: unknown): Profile[] {
  const v = field(r, "profiles");
  if (!Array.isArray(v)) return [];
  return v.filter(isRecord).flatMap((p) =>
    s(p["name"]) === "" || s(p["kind"]) === ""
      ? []
      : [
          {
            name: s(p["name"]),
            kind: s(p["kind"]),
            model: s(p["model"]),
            effort: s(p["effort"]),
            role: s(p["role"]),
            instructions: s(p["instructions"]),
            voice: s(p["voice"]),
            paths: stringList(p["paths"]) ?? [],
            color: s(p["color"]),
            scope: s(p["scope"]) === "project" ? "project" : "global",
          } satisfies Profile,
        ],
  );
}

export function toSearchHits(r: unknown): SearchHit[] {
  const v = field(r, "hits");
  if (!Array.isArray(v)) return [];
  return v.filter(isRecord).flatMap((h) =>
    s(h["session_id"]) === "" || s(h["message_id"]) === ""
      ? []
      : [{ session_id: s(h["session_id"]), message_id: s(h["message_id"]), agent: s(h["agent"]), snippet: s(h["snippet"]), at: n(h["at"]) }],
  );
}
