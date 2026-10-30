import { describe, expect, test } from "bun:test";
import type { KV } from "./auth";
import { Controller } from "./controller";
import { FakeClock, type FakeSocket, flush, SocketLog, welcomeFrame } from "./fakes";
import { UPLOAD_CHUNK_BYTES } from "./images";
import { isRecord } from "./protocol";
import type { ThemeTarget } from "./theme";

const session = {
  id: "s1",
  title: "Fix bug",
  updated_at: "2026-10-12T09:00:10Z",
  busy: false,
  pending: 0,
  mode: "ask",
};
const approvalEdit = {
  id: "p2",
  session_id: "s1",
  agent: "codex-1",
  kind: "edit",
  description: "Edit a file",
  path: "a.go",
  diff: "@@ -1 +1 @@\n-a\n+b\n",
  created_at: "2026-10-12T09:00:04Z",
};
const FP = "SHA256:AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA";
const offer = {
  code: "7H3K-9QWX",
  fingerprint: FP,
  host: null,
  port: null,
  name: null,
  expires: null,
};

function memory(): KV & { data: Map<string, string> } {
  const data = new Map<string, string>();
  return {
    data,
    get length() {
      return data.size;
    },
    key: (i) => [...data.keys()][i] ?? null,
    getItem: (k) => data.get(k) ?? null,
    setItem: (k, v) => {
      data.set(k, v);
    },
    removeItem: (k) => {
      data.delete(k);
    },
  };
}

const blocked: KV = {
  get length(): number {
    throw new Error("blocked");
  },
  key: () => {
    throw new Error("blocked");
  },
  getItem: () => {
    throw new Error("blocked");
  },
  setItem: () => {
    throw new Error("blocked");
  },
  removeItem: () => {
    throw new Error("blocked");
  },
};

function themeRecorder() {
  const vars = new Map<string, string>();
  const target: ThemeTarget = {
    setProperty: (n, v) => {
      vars.set(n, v);
    },
    removeProperty: (n) => {
      vars.delete(n);
    },
    setScheme: (s) => {
      scheme.push(s);
    },
    setThemeColor: () => {},
  };
  const scheme: Array<string | null> = [];
  return { vars, target, scheme };
}

function setup(over: { storage?: KV | null; pairStatus?: number } = {}) {
  const storage = over.storage === undefined ? memory() : over.storage;
  const log = new SocketLog();
  const clock = new FakeClock();
  const vibrations: Array<number | number[]> = [];
  const pairCalls: unknown[] = [];
  const warnings: string[] = [];
  const theme = themeRecorder();
  const controller = new Controller({
    storage,
    fetchFn: async (url, init) => {
      pairCalls.push([url, JSON.parse(String(init.body))]);
      if (over.pairStatus !== undefined) {
        return Response.json({ error: "invalid" }, { status: over.pairStatus });
      }
      return Response.json({
        token: "dev1.secret",
        device_id: "dev1",
        server_id: "srv_1",
        expires_at: "2027-01-10T09:30:00Z",
      });
    },
    socketFactory: log.factory,
    wsUrl: "ws://host/api/v1/ws",
    deviceName: "Pixel",
    vibrate: (p) => vibrations.push(p),
    theme: theme.target,
    clock,
    warn: (m) => warnings.push(m),
  });
  return { storage, log, clock, controller, vibrations, pairCalls, warnings, theme };
}

// Pairs through the offer flow and opens the socket.
async function paired(t: ReturnType<typeof setup>): Promise<FakeSocket> {
  t.controller.start(offer);
  await t.controller.confirmPair();
  const sock = t.log.latest;
  sock.open();
  return sock;
}

function answer(sock: FakeSocket, results: Record<string, unknown>): void {
  for (const f of sock.sent) {
    if (!isRecord(f) || f["t"] !== "req" || typeof f["m"] !== "string") continue;
    if (f["m"] in results) sock.receive({ t: "res", id: f["id"], ok: true, r: results[f["m"]] });
  }
}

describe("pairing", () => {
  test("an offer waits for confirmation; nothing is sent before it (R9)", async () => {
    const t = setup();
    t.controller.start(offer);
    expect(t.controller.state.auth).toBe("offer");
    expect(t.controller.state.offer?.fingerprint).toBe(FP);
    expect(t.pairCalls).toHaveLength(0);
    await t.controller.confirmPair();
    expect(t.pairCalls).toEqual([["/api/v1/pair", { code: "7H3K-9QWX", device_name: "Pixel" }]]);
    expect(t.storage?.getItem("deck.remote.token.srv_1")).toBe("dev1.secret");
    expect(t.storage?.getItem("deck.remote.server")).toBe("srv_1");
    expect(t.controller.state.auth).toBe("paired");
    expect(t.controller.state.offer).toBeNull();
  });

  test("the welcome sets server, device and theme and loads snapshots", async () => {
    const t = setup();
    const sock = await paired(t);
    expect(sock.sent[0]).toEqual({ t: "hello", v: 2, token: "dev1.secret" });
    sock.receive(welcomeFrame({ allow_yolo: true }));
    expect(t.controller.state.link).toBe("online");
    expect(t.controller.state.server?.allow_yolo).toBe(true);
    expect(t.controller.state.device?.id).toBe("dev1");
    expect(t.theme.vars.get("--p-bg-base")).toBe("#111416");
    expect(t.theme.vars.get("--kind-claude")).toBe("#d97757");
    answer(sock, { "sessions.list": [session], "approvals.list": [approvalEdit] });
    await flush();
    const s = t.controller.state;
    expect(s.sessions.map((x) => x.id)).toEqual(["s1"]);
    expect(s.sessions[0]?.pending).toBe(1);
    expect(s.approvals).toHaveLength(1);
  });

  test("a pasted invite or code becomes an offer; junk is reported", () => {
    const t = setup();
    t.controller.start(null);
    t.controller.submitInput("7H3K-9QWX");
    expect(t.controller.state.auth).toBe("offer");
    expect(t.controller.state.offer?.fingerprint).toBeNull();
    t.controller.cancelOffer();
    expect(t.controller.state.auth).toBe("unpaired");
    t.controller.submitInput("deck://pair?v=9");
    expect(t.controller.state.auth).toBe("unpaired");
    expect(t.controller.state.authMessage).toContain("not a Deck");
  });

  test("a failed pairing returns to the pairing screen with a message", async () => {
    const t = setup({ pairStatus: 400 });
    t.controller.start(offer);
    await t.controller.confirmPair();
    expect(t.controller.state.auth).toBe("unpaired");
    expect(t.controller.state.authMessage).toContain("expired");
    expect(t.log.sockets).toHaveLength(0);
    expect(t.storage?.getItem("deck.remote.server")).toBeNull();
  });

  test("a rate-limited pairing keeps the offer for a retry", async () => {
    const t = setup({ pairStatus: 429 });
    t.controller.start(offer);
    await t.controller.confirmPair();
    expect(t.controller.state.auth).toBe("offer");
    expect(t.controller.state.offer?.code).toBe("7H3K-9QWX");
  });

  test("with a stored token it connects without pairing", () => {
    const storage = memory();
    storage.setItem("deck.remote.token.srv_1", "dev1.secret");
    storage.setItem("deck.remote.server", "srv_1");
    const t = setup({ storage });
    t.controller.start(null);
    expect(t.pairCalls).toHaveLength(0);
    expect(t.log.sockets).toHaveLength(1);
    t.log.latest.open();
    expect(t.log.latest.sent[0]).toMatchObject({ token: "dev1.secret" });
  });

  test("with nothing stored it shows pairing", () => {
    const t = setup();
    t.controller.start(null);
    expect(t.controller.state.auth).toBe("unpaired");
    expect(t.log.sockets).toHaveLength(0);
  });

  test("blocked storage keeps the session alive for this page load", async () => {
    const t = setup({ storage: blocked });
    const sock = await paired(t);
    expect(t.controller.state.toasts[0]?.kind).toBe("error");
    expect(sock.sent[0]).toMatchObject({ t: "hello", token: "dev1.secret" });
    sock.receive(welcomeFrame({}));
    sock.drop();
    t.clock.advance(1000);
    t.log.latest.open();
    expect(t.log.sockets).toHaveLength(2);
    expect(t.log.latest.sent[0]).toMatchObject({ token: "dev1.secret" });
  });
});

describe("focus and reconnect (R11)", () => {
  test("opening a session sends focus and loads it; the resume carries the focus", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    const open = t.controller.openSession("s1");
    expect(sock.last()).toMatchObject({ m: "sessions.focus", p: { id: "s1" } });
    answer(sock, { "sessions.focus": {} });
    await flush();
    expect(sock.last()["m"]).toBeDefined();
    const page = sock.requests("messages.list")[0];
    expect(page?.["p"]).toEqual({ session_id: "s1", limit: 100 });
    answer(sock, {
      "messages.list": { messages: [], more: false },
      "queue.list": [],
      "roster.get": [],
    });
    await open;
    expect(t.controller.focus).toBe("s1");

    sock.drop();
    t.clock.advance(1000);
    const next = t.log.latest;
    next.open();
    expect(next.sent[0]).toMatchObject({ resume: { epoch: "e1", seq: 0, focus: "s1" } });
  });

  test("after a replayed reconnect the focused session's messages are refetched", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.openSession("s1");
    answer(sock, { "sessions.focus": {} });
    await flush();
    sock.drop();
    t.clock.advance(1000);
    const next = t.log.latest;
    next.open();
    next.receive(welcomeFrame({ epoch: "e1", replay: true }));
    await flush();
    expect(next.requests("messages.list")).toHaveLength(1);
    expect(next.requests("sessions.list")).toHaveLength(0);
    expect(next.requests("sessions.focus")).toHaveLength(0);
  });

  test("a new epoch refetches everything and says the focus again", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.openSession("s1");
    answer(sock, { "sessions.focus": {} });
    await flush();
    sock.drop();
    t.clock.advance(1000);
    const next = t.log.latest;
    next.open();
    next.receive(welcomeFrame({ epoch: "e2", replay: false }));
    await flush();
    expect(next.requests("sessions.list")).toHaveLength(1);
    expect(next.requests("approvals.list")).toHaveLength(1);
    expect(next.requests("messages.list")).toHaveLength(1);
    expect(next.requests("sessions.focus")[0]?.["p"]).toEqual({ id: "s1" });
  });

  test("a resync frame refetches everything", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    await flush();
    const before = sock.requests("sessions.list").length;
    sock.receive({ t: "resync" });
    await flush();
    expect(sock.requests("sessions.list")).toHaveLength(before + 1);
  });

  test("a session opened while offline is focused by the welcome", async () => {
    const t = setup();
    const sock = await paired(t);
    // Not ready yet: openSession only records the focus.
    await t.controller.openSession("s1");
    expect(sock.requests("sessions.focus")).toHaveLength(0);
    expect(t.controller.focus).toBe("s1");
    sock.receive(welcomeFrame({}));
    await flush();
    expect(sock.requests("sessions.focus")[0]).toMatchObject({ p: { id: "s1" } });
  });

  test("scoped events for a session that is not open are dropped", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.openSession("s1");
    answer(sock, { "sessions.focus": {} });
    await flush();
    const row = {
      id: "m9",
      session_id: "s2",
      role: "assistant",
      created_at: "2026-10-12T09:00:01Z",
      updated_at: "2026-10-12T09:00:01Z",
      finished: true,
      parts: [{ type: "text", text: "elsewhere" }],
    };
    sock.receive({ t: "ev", seq: 1, e: "message.created", p: row });
    expect(t.controller.state.messages["s2"]).toBeUndefined();
    expect(t.warnings.some((w) => w.includes("message.created"))).toBe(true);
    sock.receive({
      t: "ev",
      seq: 2,
      e: "message.created",
      p: { ...row, id: "m10", session_id: "s1" },
    });
    expect(t.controller.state.messages["s1"]?.map((m) => m.id)).toContain("m10");
  });

  test("closing a session unfocuses", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.openSession("s1");
    t.controller.closeSession();
    expect(sock.last()).toMatchObject({ m: "sessions.focus", p: { id: "" } });
    expect(t.controller.focus).toBeNull();
  });
});

describe("events", () => {
  test("live events update state and approvals vibrate", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    answer(sock, { "sessions.list": [session] });
    await flush();
    sock.receive({ t: "ev", seq: 1, e: "approval.requested", p: approvalEdit });
    sock.receive({ t: "ev", seq: 2, e: "busy", p: { session_id: "s1", busy: true } });
    sock.receive({ t: "ev", seq: 3, e: "mode.changed", p: { session_id: "s1", mode: "plan" } });
    expect(t.controller.state.approvals.map((a) => a.id)).toEqual(["p2"]);
    expect(t.controller.state.sessions[0]).toMatchObject({ busy: true, mode: "plan" });
    expect(t.vibrations).toHaveLength(1);
  });

  test("an invalid event is dropped with a warning, never a crash", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    sock.receive({ t: "ev", seq: 1, e: "approval.requested", p: { id: "x" } });
    sock.receive({ t: "ev", seq: 2, e: "mystery", p: {} });
    expect(t.controller.state.approvals).toEqual([]);
    expect(t.warnings).toHaveLength(2);
    expect(t.controller.state.link).toBe("online");
  });

  test("theme.changed updates the CSS variables live", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    sock.receive({
      t: "ev",
      seq: 1,
      e: "theme.changed",
      p: { palette: { bg_base: "#fbfaf7" }, kinds: { codex: { tint: "#5b4bd6", mark: ">_" } } },
    });
    expect(t.theme.vars.get("--p-bg-base")).toBe("#fbfaf7");
    expect(t.theme.vars.get("--p-primary")).toBeUndefined();
    expect(t.theme.vars.get("--kind-claude")).toBeUndefined();
    expect(t.theme.vars.get("--kind-codex")).toBe("#5b4bd6");
  });

  test("Deck's own look leaves the colours to style.css", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    sock.receive({
      t: "ev",
      seq: 1,
      e: "theme.changed",
      p: {
        palette: { bg_base: "#19191b", primary: "#0a84ff", fg_base: "#e3e3e5" },
        kinds: { claude: { tint: "#ff9f5a", mark: "✻" } },
      },
    });
    // No palette, no tints and no scheme: the phone's own light or dark.
    expect([...t.theme.vars.keys()]).toEqual([]);
    expect(t.theme.scheme.at(-1)).toBeNull();
    // The marks still come from the theme.
    expect(t.controller.state.theme?.kinds["claude"]?.mark).toBe("✻");
  });

  test("device.paired toasts; roster.changed for the open session refetches it", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.openSession("s1");
    answer(sock, { "sessions.focus": {} });
    await flush();
    const before = sock.requests("roster.get").length;
    sock.receive({ t: "ev", seq: 1, e: "roster.changed", p: { session_id: "s1" } });
    sock.receive({ t: "ev", seq: 2, e: "roster.changed", p: { session_id: "other" } });
    expect(sock.requests("roster.get")).toHaveLength(before + 1);
    sock.receive({
      t: "ev",
      seq: 3,
      e: "device.paired",
      p: {
        id: "dev_93c0",
        name: "iPhone",
        created_at: "2026-10-12T10:02:17Z",
        expires_at: "2027-01-10T10:02:17Z",
        allow_yolo: false,
      },
    });
    expect(t.controller.state.toasts.at(-1)?.text).toContain("iPhone");
  });
});

describe("requests", () => {
  test("answering an approval names the session and drops it", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    sock.receive({ t: "ev", seq: 1, e: "approval.requested", p: approvalEdit });
    const p = t.controller.answerApproval("s1", "p2", "allow_session");
    expect(sock.last()).toMatchObject({
      m: "approvals.answer",
      p: { session_id: "s1", id: "p2", decision: "allow_session" },
    });
    sock.receive({ t: "res", id: sock.last()["id"], ok: true, r: { resolved: true } });
    await p;
    expect(t.controller.state.approvals).toEqual([]);
  });

  test("queue steer and cancel name the session", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.steer("s1", "q1");
    expect(sock.last()).toMatchObject({ m: "queue.steer", p: { session_id: "s1", item_id: "q1" } });
    void t.controller.cancelQueued("s1", "q2");
    expect(sock.last()).toMatchObject({
      m: "queue.cancel",
      p: { session_id: "s1", item_id: "q2" },
    });
    void t.controller.interrupt("s1", "codex-1");
    expect(sock.last()).toMatchObject({
      m: "agent.interrupt",
      p: { session_id: "s1", agent: "codex-1" },
    });
  });

  test("a server error surfaces as a toast and leaves the mode alone", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    answer(sock, { "sessions.list": [session] });
    await flush();
    const p = t.controller.setMode("s1", "yolo");
    expect(sock.last()).toMatchObject({ m: "mode.set", p: { session_id: "s1", mode: "yolo" } });
    sock.receive({
      t: "res",
      id: sock.last()["id"],
      ok: false,
      err: { code: "yolo_disabled", msg: "This phone may not switch to yolo." },
    });
    expect(await p).toBe(false);
    expect(t.controller.state.toasts[0]?.text).toContain("yolo");
    expect(t.controller.state.sessions[0]?.mode).toBe("ask");
  });

  test("a mode change applies the server's answer", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    answer(sock, { "sessions.list": [session] });
    await flush();
    const p = t.controller.setMode("s1", "plan");
    sock.receive({ t: "res", id: sock.last()["id"], ok: true, r: { mode: "plan" } });
    expect(await p).toBe(true);
    expect(t.controller.state.sessions[0]?.mode).toBe("plan");
  });

  test("older pages use the cursor and stop at the end", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.openSession("s1");
    answer(sock, { "sessions.focus": {} });
    await flush();
    answer(sock, {
      "messages.list": { messages: [], more: true, cursor: "cur_a" },
      "queue.list": [],
      "roster.get": [],
    });
    await flush();
    const older = t.controller.loadOlder("s1");
    const req = sock.requests("messages.list")[1];
    expect(req?.["p"]).toEqual({ session_id: "s1", before: "cur_a", limit: 100 });
    sock.receive({ t: "res", id: req?.["id"], ok: true, r: { messages: [], more: false } });
    expect(await older).toBe(false);
    expect(await t.controller.loadOlder("s1")).toBe(false);
    expect(sock.requests("messages.list")).toHaveLength(2);
  });

  test("chat.send carries images and an idempotency key", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    const img = { name: "a.png", mime: "image/png", data_b64: "AAAA" };
    const p = t.controller.send("s1", "hi", [img]);
    const sent = sock.last();
    expect(sent["m"]).toBe("chat.send");
    expect(sent["p"]).toMatchObject({ session_id: "s1", text: "hi", images: [img] });
    expect(typeof (sent["p"] as { idempotency_key: unknown }).idempotency_key).toBe("string");
    sock.receive({ t: "res", id: sent["id"], ok: true, r: { message_id: "m1" } });
    expect(await p).toBe(true);
  });

  test("a video is uploaded in chunks, then chat.send names it with its label", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    const bytes = new Uint8Array(UPLOAD_CHUNK_BYTES + 10);
    bytes.set([0, 0, 0, 0x20, 0x66, 0x74, 0x79, 0x70]);
    const file = new Blob([bytes], { type: "video/mp4" });
    const p = t.controller.send("s1", "look", [
      { name: "clip.mp4", mime: "video/mp4", label: "the crash", file },
    ]);
    const answerUpload = async (n: number) => {
      for (let i = 0; i < 100 && sock.requests("media.upload").length < n; i++) {
        await new Promise((r) => setTimeout(r, 2));
      }
      const req = sock.requests("media.upload")[n - 1];
      sock.receive({ t: "res", id: req?.["id"], ok: true, r: { upload_id: "up1", received: 0 } });
      return req?.["p"] as Record<string, unknown>;
    };
    const first = await answerUpload(1);
    expect(first).toMatchObject({
      session_id: "s1",
      name: "clip.mp4",
      mime: "video/mp4",
      size: bytes.length,
      offset: 0,
    });
    expect(atob(String(first["data_b64"])).length).toBe(UPLOAD_CHUNK_BYTES);
    const second = await answerUpload(2);
    expect(second).toMatchObject({
      session_id: "s1",
      upload_id: "up1",
      offset: UPLOAD_CHUNK_BYTES,
    });
    expect(second["size"]).toBeUndefined();
    for (let i = 0; i < 100 && sock.requests("chat.send").length === 0; i++) await flush();
    const sent = sock.requests("chat.send")[0];
    expect(sent?.["p"]).toMatchObject({
      session_id: "s1",
      text: "look",
      images: [{ name: "clip.mp4", mime: "video/mp4", label: "the crash", upload_id: "up1" }],
    });
    sock.receive({ t: "res", id: sent?.["id"], ok: true, r: { message_id: "m1" } });
    expect(await p).toBe(true);
  });

  test("editing a message sends message.edit and the update arrives as an event", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    void t.controller.openSession("s1");
    answer(sock, { "sessions.focus": {} });
    await flush();
    const userMsg = {
      id: "m1",
      session_id: "s1",
      role: "user",
      created_at: "2026-10-12T09:00:01Z",
      updated_at: "2026-10-12T09:00:01Z",
      finished: true,
      editable: true,
      parts: [{ type: "text", text: "old" }],
    };
    sock.receive({ t: "ev", seq: 1, e: "message.created", p: userMsg });
    const p = t.controller.editMessage("s1", "m1", "new");
    expect(sock.last()).toMatchObject({
      m: "message.edit",
      p: { session_id: "s1", message_id: "m1", text: "new" },
    });
    sock.receive({ t: "res", id: sock.last()["id"], ok: true });
    expect(await p).toBe(true);
    sock.receive({
      t: "ev",
      seq: 2,
      e: "message.updated",
      p: {
        ...userMsg,
        updated_at: "2026-10-12T09:00:02Z",
        edited: true,
        parts: [{ type: "text", text: "new" }],
      },
    });
    expect(t.controller.state.messages["s1"]?.[0]?.parts[0]?.text).toBe("new");
  });

  test("editing while offline reports it instead of throwing", async () => {
    const t = setup();
    await paired(t);
    expect(await t.controller.editMessage("s1", "m1", "x")).toBe(false);
    expect(t.controller.state.toasts[0]?.text).toContain("offline");
  });

  test("devices load into state; revokeSelf names this device only", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    const p = t.controller.loadDevices();
    const dev = {
      id: "dev1",
      name: "Pixel",
      created_at: "2026-10-12T09:28:51Z",
      expires_at: "2027-01-10T09:30:00Z",
      allow_yolo: true,
      self: true,
    };
    sock.receive({ t: "res", id: sock.last()["id"], ok: true, r: [dev] });
    await p;
    expect(t.controller.state.devices).toEqual([dev]);
    void t.controller.revokeSelf();
    expect(sock.last()).toMatchObject({ m: "devices.revoke", p: { id: "dev1" } });
  });
});

describe("stopping (R1)", () => {
  test("4401 token_invalid and 4403 forget the token and return to pairing", async () => {
    for (const [code, reason, text] of [
      [4401, "token_invalid", "no longer valid"],
      [4403, "revoked", "removed"],
    ] as const) {
      const t = setup();
      const sock = await paired(t);
      sock.receive(welcomeFrame({}));
      sock.drop(code, reason);
      expect(t.controller.state.auth).toBe("unpaired");
      expect(t.controller.state.authMessage).toContain(text);
      expect(t.storage?.getItem("deck.remote.token.srv_1")).toBeNull();
      expect(t.storage?.getItem("deck.remote.server")).toBeNull();
    }
  });

  test("4429 keeps the token and the paired state", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    sock.drop(4429, "5");
    expect(t.controller.state.auth).toBe("paired");
    expect(t.controller.state.link).toBe("offline");
    expect(t.storage?.getItem("deck.remote.token.srv_1")).toBe("dev1.secret");
    t.clock.advance(60_000);
    expect(t.log.sockets).toHaveLength(2);
  });

  test("4400 shows the protocol mismatch screen and keeps the token", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.drop(4400, "unsupported protocol version");
    expect(t.controller.state.auth).toBe("protocol");
    expect(t.storage?.getItem("deck.remote.token.srv_1")).toBe("dev1.secret");
  });

  test("forget clears the token locally", async () => {
    const t = setup();
    const sock = await paired(t);
    sock.receive(welcomeFrame({}));
    t.controller.forget("bye");
    expect(t.controller.state.auth).toBe("unpaired");
    expect(t.storage?.getItem("deck.remote.token.srv_1")).toBeNull();
    expect(sock.closed).toBe(true);
  });
});
