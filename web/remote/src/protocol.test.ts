import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import {
  DECODERS,
  isAgentLine,
  isApproval,
  isChangedFile,
  isDevice,
  isEvFrame,
  isHelloFrame,
  isMessage,
  isModelChoice,
  isPairRequest,
  isPairResponse,
  isPushStatus,
  isQueuedItem,
  isReqFrame,
  isResFrame,
  isServerInfo,
  isSession,
  isWelcomeFrame,
  parseEvent,
  parseServerFrame,
  toMessage,
} from "./protocol";

const dir = join(import.meta.dir, "..", "..", "..", "internal", "remote", "wire", "testdata");

function load(name: string): unknown {
  return JSON.parse(readFileSync(join(dir, `${name}.json`), "utf8"));
}

function isObj(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

// Request bodies are only ever built by the client, so their fixtures pin the
// field names the server decodes rather than a guard from protocol.ts.
function isMessageEditReq(v: unknown): boolean {
  return (
    isObj(v) &&
    typeof v["session_id"] === "string" &&
    typeof v["message_id"] === "string" &&
    typeof v["text"] === "string"
  );
}

function isPushSubscribeReq(v: unknown): boolean {
  if (!isObj(v)) return false;
  const sub = v["subscription"];
  return (
    isObj(sub) &&
    typeof sub["endpoint"] === "string" &&
    isObj(sub["keys"]) &&
    typeof sub["keys"]["p256dh"] === "string" &&
    typeof sub["keys"]["auth"] === "string" &&
    typeof v["approvals"] === "boolean" &&
    typeof v["done"] === "boolean"
  );
}

const guards: Record<string, (v: unknown) => boolean> = {
  agent_line: isAgentLine,
  approval_command: isApproval,
  approval_edit: isApproval,
  changed_file: isChangedFile,
  device: isDevice,
  frame_err: isResFrame,
  frame_event: isEvFrame,
  frame_hello: isHelloFrame,
  frame_req: isReqFrame,
  frame_res: isResFrame,
  frame_welcome: isWelcomeFrame,
  message: isMessage,
  message_edit_req: isMessageEditReq,
  message_editable: isMessage,
  model_choice: isModelChoice,
  pair_request: isPairRequest,
  pair_response: isPairResponse,
  push_status: isPushStatus,
  push_subscribe_req: isPushSubscribeReq,
  queued_item: isQueuedItem,
  session: isSession,
};

describe("golden fixtures", () => {
  const files = readdirSync(dir)
    .filter((f) => f.endsWith(".json"))
    .map((f) => f.replace(/\.json$/, ""))
    .sort();

  test("fixtures directory is not empty", () => {
    expect(files.length).toBeGreaterThan(10);
  });

  test("every fixture has a guard, so a new one cannot go untested", () => {
    expect(files).toEqual(Object.keys(guards).sort());
  });

  for (const name of Object.keys(guards)) {
    test(`${name} satisfies its guard`, () => {
      const guard = guards[name];
      expect(guard?.(load(name))).toBe(true);
    });
  }

  test("guards reject other fixtures' shapes", () => {
    expect(isSession(load("message"))).toBe(false);
    expect(isMessage(load("session"))).toBe(false);
    expect(isApproval(load("device"))).toBe(false);
    expect(isHelloFrame(load("frame_welcome"))).toBe(false);
  });

  test("frames parse through parseServerFrame", () => {
    for (const name of ["frame_welcome", "frame_res", "frame_err", "frame_event"]) {
      expect(parseServerFrame(JSON.stringify(load(name)))).not.toBeNull();
    }
    // Client-to-server frames are not valid server frames.
    expect(parseServerFrame(JSON.stringify(load("frame_hello")))).toBeNull();
    expect(parseServerFrame(JSON.stringify(load("frame_req")))).toBeNull();
  });

  test("the event fixture decodes to a typed busy event", () => {
    const f = parseServerFrame(JSON.stringify(load("frame_event")));
    expect(f?.t).toBe("ev");
    if (f?.t !== "ev") return;
    expect(f.seq).toBe(4012);
    expect(parseEvent(f.e, f.p)).toEqual({ e: "busy", p: { session_id: "s1", busy: true } });
  });

  test("the error fixture carries code and msg", () => {
    const f = load("frame_err");
    expect(isResFrame(f) && f.ok === false && f.err.code).toBe("yolo_disabled");
  });
});

describe("parseEvent", () => {
  test("message events accept a fixture message", () => {
    const m = load("message");
    expect(parseEvent("message.created", m)?.e).toBe("message.created");
    expect(parseEvent("message.updated", m)?.e).toBe("message.updated");
  });

  test("session and approval events", () => {
    expect(parseEvent("session.updated", load("session"))?.e).toBe("session.updated");
    expect(parseEvent("approval.requested", load("approval_edit"))?.e).toBe("approval.requested");
    expect(parseEvent("approval.resolved", { id: "p1" })).toEqual({
      e: "approval.resolved",
      p: { id: "p1" },
    });
  });

  test("queue.changed tolerates a null items list", () => {
    expect(parseEvent("queue.changed", { session_id: "s1", items: null })).toEqual({
      e: "queue.changed",
      p: { session_id: "s1", items: [] },
    });
    expect(parseEvent("queue.changed", { session_id: "s1", items: [load("queued_item")] })?.e).toBe(
      "queue.changed",
    );
  });

  test("mode, notice and revoke events", () => {
    expect(parseEvent("mode.changed", { mode: "ask" })?.e).toBe("mode.changed");
    expect(parseEvent("mode.changed", { mode: "turbo" })).toBeNull();
    expect(parseEvent("notice", { session_id: "s1", text: "hi" })?.e).toBe("notice");
    expect(parseEvent("device.revoked", {})?.e).toBe("device.revoked");
  });

  test("malformed payloads and unknown events are ignored", () => {
    expect(parseEvent("busy", { session_id: "s1" })).toBeNull();
    expect(parseEvent("message.created", { id: "m" })).toBeNull();
    expect(parseEvent("future.thing", {})).toBeNull();
  });
});

describe("edit and push contract", () => {
  test("messages carry an optional editable flag", () => {
    const m = load("message") as object;
    expect(isMessage({ ...m, editable: true })).toBe(true);
    expect(isMessage({ ...m, editable: "yes" })).toBe(false);
    expect(toMessage({ ...m, editable: true, parts: null })?.editable).toBe(true);
    expect(isMessage({ ...m, edited: true })).toBe(true);
    expect(isMessage({ ...m, edited: "yes" })).toBe(false);
    expect(toMessage({ ...m, edited: true, parts: null })?.edited).toBe(true);
    const ev = parseEvent("message.updated", { ...m, editable: true });
    expect(ev?.e === "message.updated" && ev.p.editable).toBe(true);
  });

  test("server info carries an optional push key", () => {
    const welcome = load("frame_welcome") as { server: object };
    expect(isServerInfo({ ...welcome.server, push_key: "BPk" })).toBe(true);
    expect(isServerInfo({ ...welcome.server, push_key: 7 })).toBe(false);
    expect(isServerInfo(welcome.server)).toBe(true);
  });

  test("message.edit and the push methods decode", () => {
    expect(DECODERS["message.edit"](undefined)).toEqual({ ok: true, value: true });
    expect(DECODERS["push.subscribe"]({})).toEqual({ ok: true, value: true });
    expect(DECODERS["push.unsubscribe"](null)).toEqual({ ok: true, value: true });
    expect(DECODERS["push.test"](undefined)).toEqual({ ok: true, value: true });
    const st = { available: true, enabled: false, approvals: true, done: false };
    expect(isPushStatus(st)).toBe(true);
    expect(DECODERS["push.status"](st)).toEqual({ ok: true, value: st });
    expect(DECODERS["push.status"]({ available: true }).ok).toBe(false);
  });
});

describe("tolerant decoding", () => {
  test("a message with null parts becomes an empty message", () => {
    const m = { ...(load("message") as object), parts: null };
    expect(toMessage(m)?.parts).toEqual([]);
  });

  test("list decoders accept bare arrays, wrapped arrays and nil", () => {
    const s = load("session");
    if (!isSession(s)) throw new Error("session fixture is invalid");
    expect(DECODERS["sessions.list"]([s])).toEqual({ ok: true, value: [s] });
    expect(DECODERS["sessions.list"]({ sessions: [s] })).toEqual({ ok: true, value: [s] });
    expect(DECODERS["sessions.list"](null)).toEqual({ ok: true, value: [] });
    expect(DECODERS["sessions.list"](undefined)).toEqual({ ok: true, value: [] });
    expect(DECODERS["sessions.list"]([{ id: 1 }]).ok).toBe(false);
    expect(DECODERS["sessions.list"]("nope").ok).toBe(false);
  });

  test("mode.get accepts a body or a bare string", () => {
    expect(DECODERS["mode.get"]({ mode: "plan" })).toEqual({ ok: true, value: "plan" });
    expect(DECODERS["mode.get"]("ask")).toEqual({ ok: true, value: "ask" });
    expect(DECODERS["mode.get"]({ mode: "x" }).ok).toBe(false);
  });

  test("acknowledgement methods ignore the body", () => {
    expect(DECODERS["chat.send"]({})).toEqual({ ok: true, value: true });
    expect(DECODERS["chat.send"](undefined)).toEqual({ ok: true, value: true });
  });

  test("approvals.answer needs the resolved flag", () => {
    expect(DECODERS["approvals.answer"]({ resolved: true })).toEqual({
      ok: true,
      value: { resolved: true },
    });
    expect(DECODERS["approvals.answer"]({}).ok).toBe(false);
  });
});
