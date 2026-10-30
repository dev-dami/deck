import { describe, expect, test } from "bun:test";
import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";
import { canonicalJson, prettyJson } from "./canonical";
import {
  decodeServerFrame,
  EVENT_NAMES,
  FIXTURES,
  FRAME_TYPES,
  METHOD_NAMES,
  parseEvent,
  parseServerFrame,
} from "./protocol";

// The fixtures are the contract shared with the Go backend.
const TESTDATA = join(import.meta.dir, "..", "..", "wire", "testdata");

function readFixture(name: string): string {
  return readFileSync(join(TESTDATA, `${name}.json`), "utf8").replaceAll("\r\n", "\n");
}

describe("fixtures", () => {
  const registered = new Set<string>();
  for (const [name] of FIXTURES) {
    test(`${name} is registered once`, () => {
      expect(registered.has(name)).toBe(false);
      registered.add(name);
    });
  }

  for (const [name, decode] of FIXTURES) {
    test(`${name} decodes and round-trips byte-equal`, () => {
      const raw = readFixture(name);
      const parsed: unknown = JSON.parse(raw);
      // Canonical pretty form: sorted keys, 2-space indent, trailing newline.
      expect(raw).toBe(prettyJson(parsed));
      const decoded = decode(parsed);
      expect(decoded).not.toBeNull();
      // A field the decoder does not declare is dropped and fails here, as
      // does a value the decoder rewrote.
      expect(canonicalJson(decoded)).toBe(canonicalJson(parsed));
    });
  }

  test("every testdata file is registered", () => {
    const names = new Set(FIXTURES.map(([n]) => n));
    const files = readdirSync(TESTDATA)
      .filter((f) => f.endsWith(".json"))
      .map((f) => f.slice(0, -".json".length))
      // v3, relay, Noise and future vector fixtures belong to protocol3, the
      // Noise suite and later workstreams; the Go v2 test skips the same prefixes.
      .filter(
        (f) =>
          !f.startsWith("v3_") &&
          !f.startsWith("relay_") &&
          !f.startsWith("noise_") &&
          !f.startsWith("assert_") &&
          !f.startsWith("pair_") &&
          !f.startsWith("hostauth_") &&
          !f.startsWith("push_"),
      );
    expect(files.length).toBeGreaterThan(0);
    const unregistered = files.filter((f) => !names.has(f));
    expect(unregistered).toEqual([]);
  });

  test("every registered fixture has a file", () => {
    const files = new Set(readdirSync(TESTDATA));
    const missing = FIXTURES.map(([n]) => n).filter((n) => !files.has(`${n}.json`));
    expect(missing).toEqual([]);
  });
});

describe("fixtures cover the protocol", () => {
  const names = new Set(FIXTURES.map(([n]) => n));

  test("every frame type", () => {
    for (const t of FRAME_TYPES) expect(names.has(`frame_${t}`)).toBe(true);
  });

  test("every method has a params and a result fixture", () => {
    for (const m of METHOD_NAMES) {
      expect(names.has(`req_${m}`)).toBe(true);
      expect(names.has(`res_${m}`)).toBe(true);
    }
    expect(METHOD_NAMES).toHaveLength(32);
  });

  test("every event", () => {
    for (const e of EVENT_NAMES) expect(names.has(`event_${e}`)).toBe(true);
    expect(EVENT_NAMES).toHaveLength(16);
  });
});

describe("frames and events from fixtures", () => {
  test("server frames parse from text", () => {
    for (const name of [
      "frame_welcome",
      "frame_res",
      "frame_res_err",
      "frame_ev",
      "frame_resync",
    ]) {
      expect(parseServerFrame(readFixture(name))).not.toBeNull();
    }
    expect(parseServerFrame("not json")).toBeNull();
    expect(parseServerFrame('{"t":"mystery"}')).toBeNull();
    // A hello is never received by the phone.
    expect(parseServerFrame(readFixture("frame_hello"))).toBeNull();
  });

  test("an ev frame carries its event payload", () => {
    const f = decodeServerFrame(JSON.parse(readFixture("frame_ev")));
    expect(f?.t).toBe("ev");
    if (f?.t !== "ev") return;
    expect(f.seq).toBe(4012);
    const ev = parseEvent(f.e, f.p);
    expect(ev).toEqual({ e: "busy", p: { session_id: "ses_8f2a", busy: true } });
  });

  test("every event fixture parses through parseEvent", () => {
    for (const e of EVENT_NAMES) {
      const p: unknown = JSON.parse(readFixture(`event_${e}`));
      const ev = parseEvent(e, p);
      expect(ev?.e).toBe(e);
      expect(canonicalJson(ev?.p)).toBe(canonicalJson(p));
    }
  });

  test("unknown events and bad payloads are dropped, not thrown", () => {
    expect(parseEvent("mystery", {})).toBeNull();
    expect(parseEvent("busy", { session_id: 1 })).toBeNull();
    expect(parseEvent("mode.changed", { session_id: "s", mode: "auto" })).toBeNull();
    expect(parseEvent("session.created", null)).toBeNull();
  });

  test("a res with ok:false needs an err", () => {
    expect(decodeServerFrame({ t: "res", id: "1", ok: false })).toBeNull();
    expect(decodeServerFrame({ t: "res", id: "1", ok: true })).toEqual({
      t: "res",
      id: "1",
      ok: true,
    });
  });

  test("a welcome without theme or server_id is rejected", () => {
    const w: unknown = JSON.parse(readFixture("frame_welcome"));
    expect(decodeServerFrame(w)).not.toBeNull();
    if (!(typeof w === "object" && w !== null)) throw new Error("fixture");
    const server = (w as { server: Record<string, unknown> }).server;
    const { theme: _t, ...noTheme } = server;
    expect(decodeServerFrame({ ...w, server: noTheme })).toBeNull();
    const { server_id: _s, ...noId } = server;
    expect(decodeServerFrame({ ...w, server: noId })).toBeNull();
  });

  test("messages.list limit is bounded", () => {
    const req = FIXTURES.find(([n]) => n === "req_messages.list")?.[1];
    expect(req?.({ session_id: "s", limit: 100 })).not.toBeNull();
    expect(req?.({ session_id: "s", limit: 101 })).toBeNull();
    expect(req?.({ session_id: "s", limit: -1 })).toBeNull();
  });

  test("nil slices and maps from Go read as empty", () => {
    const msg = FIXTURES.find(([n]) => n === "entity_message")?.[1];
    const m: Record<string, unknown> = JSON.parse(readFixture("entity_message"));
    expect(msg?.({ ...m, parts: null })).toMatchObject({ parts: [] });
    const theme = FIXTURES.find(([n]) => n === "entity_theme")?.[1];
    expect(theme?.({ palette: null, kinds: null })).toEqual({ palette: {}, kinds: {} });
  });
});
