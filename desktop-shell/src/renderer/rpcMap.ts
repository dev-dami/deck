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
  if (isRecord(img)) out["image"] = { name: s(img["name"]) || "image", mime: s(img["mime"]) };
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
    ...strings(p, ["agent"]),
  };
  if (p["editable"] === true) out["editable"] = true;
  if (p["edited"] === true) out["edited"] = true;
  return out;
}

function mapApproval(p: Obj): unknown {
  return {
    id: s(p["id"]),
    session_id: s(p["session_id"]),
    agent: s(p["agent"]),
    kind: s(p["kind"]),
    description: s(p["description"]),
    created_at: isoOf(p["created_at"]),
    ...strings(p, ["tool_call_id", "command", "path", "diff", "raw"]),
  };
}

function mapQueued(p: Obj): unknown {
  return { id: s(p["id"]), agent: s(p["agent"]), text: s(p["text"]), at: isoOf(p["at"]) };
}

function mapAgent(p: Obj): unknown {
  return { id: s(p["id"]), name: s(p["name"]) || s(p["id"]), kind: s(p["kind"]), busy: p["busy"] === true, ...strings(p, ["model", "effort"]) };
}

function mapModel(p: Obj): unknown {
  const out: Obj = { id: s(p["id"]), label: s(p["label"]) || s(p["id"]) };
  if (Array.isArray(p["efforts"]) && p["efforts"].length > 0) out["efforts"] = p["efforts"];
  if (p["selected"] === true) out["selected"] = true;
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

export interface Ready {
  epoch: string;
  seq: number;
  resync: boolean;
}

export type RpcEvent = { k: "ready"; ready: Ready } | { k: "event"; seq: number; event: ServerEvent };

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
  const spec = EVENTS[kind];
  if (spec === undefined) return null;
  const event = parseEvent(spec.e, spec.map(payload));
  return event === null ? null : { k: "event", seq: n(v["seq"]), event };
}
