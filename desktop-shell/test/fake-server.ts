// A fake Deck for tests: the HTTP pair route and the protocol v2 WebSocket of
// docs/remote-contract.md, over an in-memory project. It follows
// pkg/remote/remotetest/fake.go (ids checked against their session, pages by
// index cursor) and takes its theme from the shared wire fixtures. Every
// request is decoded with the shared guards, so a malformed call from the
// app fails the test.
import { randomBytes } from "node:crypto";
import { readFileSync } from "node:fs";
import http from "node:http";
import { join } from "node:path";
import { type WebSocket, WebSocketServer } from "ws";
import {
  type AgentLine,
  type Approval,
  type ChangedFile,
  decodeTheme,
  isRecord,
  type Message,
  METHODS,
  type MethodName,
  type QueuedItem,
  type Session,
  type Theme,
} from "../../pkg/remote/web/src/protocol";

const TESTDATA = join(__dirname, "..", "..", "pkg", "remote", "wire", "testdata");

function fixtureTheme(): Theme {
  const t = decodeTheme(JSON.parse(readFileSync(join(TESTDATA, "entity_theme.json"), "utf8")));
  if (t === null) throw new Error("entity_theme.json does not decode");
  return t;
}

interface Thread {
  session: Session;
  messages: Message[];
  roster: AgentLine[];
  queue: QueuedItem[];
  changes: ChangedFile[];
}

interface Client {
  ws: WebSocket;
  focus: string;
}

export class Fail extends Error {
  constructor(
    readonly code: string,
    msg: string,
  ) {
    super(msg);
  }
}

const KINDS = ["claude", "codex", "opencode", "antigravity"];
const STREAM_STEPS = 6;
// "spin up two codex agents ...": the agent asked spawns them (agent.spawn).
const SPAWN = /\bspin up (a|an|one|two|three|[1-4]) (claude|codex|opencode|antigravity)\b/i;
const COUNT: Record<string, number> = { a: 1, an: 1, one: 1, two: 2, three: 3 };

export interface FakeOptions {
  allowYolo?: boolean;
  // Milliseconds between streamed updates.
  tickMs?: number;
  // Adds the thread "Big room" with this many messages.
  bigRoom?: number;
}

export class FakeDeck {
  readonly serverId = "srv_fake";
  code = "FAKE1-CODE2";
  readonly sent: Array<{ session_id: string; text: string }> = [];
  readonly answered: Array<{ id: string; decision: string }> = [];
  readonly interrupted: string[] = [];
  readonly edited: Array<{ message_id: string; text: string }> = [];
  readonly continued: string[] = [];
  // The queue items steered into a running turn.
  readonly steered: string[] = [];
  // Each models.list asked: the pickers list every kind once, then keep it.
  readonly modelLists: string[] = [];
  // Named agents (docs/design/agents-and-mentions.md): "@charles" for one
  // with no member of that name starts a member called charles, with its
  // role. The rpc fake keeps the list (ListProfiles).
  profiles: Array<{ name: string; kind: string; role?: string }> = [];
  // The agents an agent spawned, as Deck's agents tree lists them (the rpc
  // fake's Agents adds them under their spawner).
  readonly spawned: Array<{ session_id: string; parent: string; name: string; kind: string }> = [];
  private readonly http: http.Server;
  private readonly wss = new WebSocketServer({ noServer: true, maxPayload: 13 << 20 });
  private readonly clients = new Set<Client>();
  private readonly tokens = new Set<string>();
  private readonly threads = new Map<string, Thread>();
  private approvals: Approval[] = [];
  private readonly idem = new Map<string, string>();
  private readonly timers = new Set<NodeJS.Timeout>();
  readonly theme = fixtureTheme();
  private epoch = `ep_${randomBytes(4).toString("hex")}`;
  private seq = 0;
  private next = 100;
  // Twelve hours ago, so the fixture threads are recent, not settled.
  private clock = Date.now() - 12 * 3_600_000;
  port = 0;

  private constructor(private readonly opts: FakeOptions) {
    this.http = http.createServer((req, res) => this.route(req, res));
    this.http.on("upgrade", (req, sock, head) => {
      const origin = `http://${req.headers.host ?? ""}`;
      if (req.url !== "/api/v1/ws" || req.headers.origin !== origin) {
        sock.end("HTTP/1.1 403 Forbidden\r\n\r\n");
        return;
      }
      this.wss.handleUpgrade(req, sock, head, (ws) => this.accept(ws));
    });
    this.seed();
  }

  static async start(opts: FakeOptions = {}): Promise<FakeDeck> {
    const f = new FakeDeck(opts);
    await new Promise<void>((r) => f.http.listen(0, "127.0.0.1", r));
    const addr = f.http.address();
    f.port = typeof addr === "object" && addr !== null ? addr.port : 0;
    return f;
  }

  // The link `deck remote pair` prints over loopback.
  get pairLink(): string {
    return `http://127.0.0.1:${this.port}/#pair=${this.code}`;
  }

  async close(): Promise<void> {
    for (const t of this.timers) clearTimeout(t);
    for (const c of this.clients) c.ws.terminate();
    await new Promise<void>((r) => this.http.close(() => r()));
  }

  // Drops every socket, as a restart would, with a new epoch.
  restart(): void {
    this.epoch = `ep_${randomBytes(4).toString("hex")}`;
    for (const c of this.clients) c.ws.terminate();
  }

  // Forgets every token and drops every socket: the next hello is refused
  // with 4401 token_invalid.
  revokeAll(): void {
    this.tokens.clear();
    this.restart();
  }

  // A notice for one session, such as Deck's overlap notes.
  notice(sessionId: string, text: string): void {
    this.emit("notice", { session_id: sessionId, text }, sessionId);
  }

  // A finished reply from an agent, as if it had just written it.
  reply(sessionId: string, agent: string, text: string): Message {
    return this.message(this.get(sessionId), "assistant", agent, text, true);
  }

  // A finished command an agent ran, with what it printed.
  toolRun(sessionId: string, agent: string, command: string, output: string): Message {
    const t = this.get(sessionId);
    const at = this.now();
    const call = this.id("tc");
    const m: Message = {
      id: this.id("msg"),
      session_id: sessionId,
      role: "assistant",
      agent,
      created_at: at,
      updated_at: at,
      finished: true,
      parts: [
        { type: "tool_call", id: call, name: "bash", summary: command },
        { type: "tool_result", tool_call_id: call, name: "bash", output },
      ],
    };
    t.messages = [...t.messages, m];
    this.emit("message.created", m, sessionId);
    return m;
  }

  // Adds the thread "Busy crew", stored as Deck stores a turn: a step's
  // text and calls in one message, each result in a message of its own
  // with no agent, two agents interleaved, a failed call, and one agent
  // still at work. With handoffs by mention, for the Work Graph's history.
  addBusyThread(): string {
    const t = this.addThread("ses_busy", "Busy crew");
    t.roster = [
      { id: "ada", name: "ada", kind: "claude", model: "opus", busy: true },
      { id: "bob", name: "bob", kind: "codex", model: "default", busy: false },
    ];
    const step = (agent: string, text: string, calls: Array<[string, string]>, finished = true): string[] => {
      const ids = calls.map(() => this.id("tc"));
      const m = this.message(t, "assistant", agent, text, finished);
      m.parts = [...m.parts, ...calls.map(([name, summary], i) => ({ type: "tool_call", id: ids[i]!, name, summary }))];
      return ids;
    };
    const results = (ids: string[], outs: Array<[string, boolean?]>) => {
      ids.forEach((id, i) => {
        const r = this.message(t, "assistant", undefined, "", true);
        const [output, failed] = outs[i] ?? ["", false];
        r.parts = [{ type: "tool_result", tool_call_id: id, name: "tool", output, ...(failed === true ? { is_error: true } : {}) }];
      });
    };
    this.message(t, "user", undefined, "@ada find why pairing locks everyone out, then @bob review the fix.", true);
    const a1 = step("ada", "I'll start with the limiter and its tests.", [
      ["Read", "pkg/remote/auth/limit.go"],
      ["Read", "pkg/remote/auth/limit_test.go"],
      ["Grep", "fail("],
      ["Read", "pkg/remote/auth/pair.go"],
      ["Bash", "go test ./pkg/remote/auth"],
    ]);
    const b1 = step("bob", "Reading the pairing flow while ada digs.", [["Read", "pkg/remote/auth/pair.go"]]);
    results(a1, [["package auth…"], ["package auth…"], ["limit.go:31: global.fail()"], ["package auth…"], ["--- FAIL: TestLockout (0.02s)\nFAIL", true]]);
    results(b1, [["package auth…"]]);
    const a2 = step("ada", "Now the fix.", [
      ["Edit", "pkg/remote/auth/limit.go"],
      ["Edit", "pkg/remote/auth/limit_test.go"],
      ["Bash", "go test ./pkg/remote/auth"],
      ["Bash", "go vet ./..."],
    ]);
    results(a2, [["ok"], ["ok"], ["ok  \tgithub.com/dev-dami/deck/pkg/remote/auth\t0.41s"], [""]]);
    this.message(t, "assistant", "ada", "The limiter counted every address in one bucket. It keys by `addr` now, with a test. @bob can you review?", true);
    const b2 = step("bob", "Reviewing ada's change.", [
      ["Read", "pkg/remote/auth/limit.go"],
      ["Bash", "git diff pkg/remote/auth"],
      ["Bash", "go test -race ./pkg/remote/auth"],
    ]);
    results(b2, [["…"], ["@@ -31 +31 @@"], ["ok"]]);
    this.message(t, "assistant", "bob", "Looks right. One nit: prune idle buckets. @ada", true);
    const a3 = step("ada", "Adding the prune.", [["Edit", "pkg/remote/auth/prune.go"], ["Bash", "go test ./pkg/remote/auth"]], false);
    results(a3.slice(0, 1), [["ok"]]);
    t.session = { ...t.session, busy: true };
    return t.session.id;
  }

  // The thread's changed files, as if someone edited the tree.
  setChanges(sessionId: string, changes: ChangedFile[]): void {
    this.get(sessionId).changes = changes;
  }

  requestApproval(sessionId: string, agent: string, command: string): Approval {
    const a: Approval = {
      id: this.id("apr"),
      session_id: sessionId,
      agent,
      kind: "command",
      description: "Run a command",
      command,
      created_at: this.now(),
    };
    this.approvals.push(a);
    this.broadcast("approval.requested", a);
    this.touch(sessionId, {});
    return a;
  }

  // ---- HTTP ---------------------------------------------------------------

  private route(req: http.IncomingMessage, res: http.ServerResponse): void {
    const json = (status: number, body: unknown) => {
      res.writeHead(status, { "content-type": "application/json" });
      res.end(JSON.stringify(body));
    };
    if (req.method === "GET" && req.url === "/api/v1/health") {
      json(200, { ok: true, protocol: 2 });
      return;
    }
    if (req.method !== "POST" || req.url !== "/api/v1/pair") {
      json(404, { error: "not_found" });
      return;
    }
    if (req.headers.origin !== `http://${req.headers.host ?? ""}`) {
      json(403, { error: "forbidden" });
      return;
    }
    if (req.headers["content-type"] !== "application/json") {
      json(415, { error: "invalid" });
      return;
    }
    let body = "";
    req.on("data", (c: Buffer) => {
      body += c.toString("utf8");
    });
    req.on("end", () => {
      let p: unknown;
      try {
        p = JSON.parse(body);
      } catch {
        json(400, { error: "invalid" });
        return;
      }
      if (!isRecord(p) || p["code"] !== this.code || typeof p["device_name"] !== "string") {
        json(400, { error: "unauthorized" });
        return;
      }
      this.code = `USED-${randomBytes(3).toString("hex")}`;
      const token = randomBytes(16).toString("hex");
      this.tokens.add(token);
      json(200, {
        token,
        device_id: this.id("dev"),
        server_id: this.serverId,
        expires_at: "2027-01-17T09:00:00Z",
      });
    });
  }

  // ---- socket -------------------------------------------------------------

  private accept(ws: WebSocket): void {
    const client: Client = { ws, focus: "" };
    let hello = false;
    ws.on("message", (data) => {
      let f: unknown;
      try {
        f = JSON.parse(data.toString());
      } catch {
        ws.close(4400, "bad frame");
        return;
      }
      if (!isRecord(f)) return;
      if (!hello) {
        if (f["t"] !== "hello" || f["v"] !== 2) {
          ws.close(4400, "bad hello");
          return;
        }
        if (typeof f["token"] !== "string" || !this.tokens.has(f["token"])) {
          ws.close(4401, "token_invalid");
          return;
        }
        hello = true;
        const resume = f["resume"];
        const replay = isRecord(resume) && resume["epoch"] === this.epoch;
        if (isRecord(resume) && typeof resume["focus"] === "string") client.focus = resume["focus"];
        this.clients.add(client);
        this.write(client, {
          t: "welcome",
          epoch: this.epoch,
          replay,
          server: {
            version: "fake",
            project: "deck",
            kinds: KINDS,
            allow_yolo: this.opts.allowYolo ?? false,
            features: ["images", "edit", "changes", "queue", "models", "devices"],
            server_id: this.serverId,
            protocol: 2,
            headless: true,
            tunnel: "",
            theme: this.theme,
          },
          device: { id: "dev_1", name: "Deck desktop", expires_at: "2027-01-17T09:00:00Z" },
        });
        return;
      }
      if (f["t"] !== "req" || typeof f["id"] !== "string" || typeof f["m"] !== "string") {
        ws.close(4400, "bad frame");
        return;
      }
      this.answer(client, f["id"], f["m"], f["p"] ?? {});
    });
    ws.on("close", () => this.clients.delete(client));
  }

  private write(c: Client, frame: unknown): void {
    if (c.ws.readyState === c.ws.OPEN) c.ws.send(JSON.stringify(frame));
  }

  private emit(e: string, p: unknown, scope: string | null): void {
    this.seq++;
    for (const c of this.clients) {
      if (scope === null || c.focus === scope) this.write(c, { t: "ev", seq: this.seq, e, p });
    }
    for (const l of this.listeners) l(e, p);
  }

  private broadcast(e: string, p: unknown): void {
    this.emit(e, p, null);
  }

  // ---- for the gRPC fake (fake-grpc.ts) ------------------------------------

  // Every event, of every session, as the rpc transport sends them.
  readonly listeners = new Set<(e: string, p: unknown) => void>();
  // The bytes of each message's image parts, in part order.
  readonly images = new Map<string, { data: Buffer; mime: string }[]>();

  // Every message of every thread, for the gRPC fake's search.
  allMessages(): Message[] {
    return [...this.threads.values()].flatMap((t) => t.messages);
  }

  // A message of the person with an image, as if they had just sent it.
  sendImage(sessionId: string, text: string, name: string, mime: string, data: Buffer): Message {
    const t = this.get(sessionId);
    const m = this.message(t, "user", undefined, text, true);
    const withImage: Message = { ...m, parts: [...m.parts, { type: "image", image: { name, mime } }] };
    t.messages = t.messages.map((x) => (x.id === m.id ? withImage : x));
    this.images.set(m.id, [{ data, mime }]);
    this.emit("message.updated", withImage, t.session.id);
    return withImage;
  }

  // Runs a v2 method, for a socket's client or none; throws Fail like a
  // refused request.
  invoke(m: MethodName, p: unknown, c: Client | null = null): unknown {
    const params = METHODS[m].req(p);
    if (params === null) throw new Fail("invalid", `bad params for ${m}`);
    return this.call(c, m, params as Record<string, unknown>);
  }

  private answer(c: Client, id: string, m: string, p: unknown): void {
    try {
      if (!(m in METHODS)) throw new Fail("invalid", `unknown method ${m}`);
      const r = this.invoke(m as MethodName, p, c);
      this.write(c, { t: "res", id, ok: true, r });
    } catch (e) {
      const err = e instanceof Fail ? { code: e.code, msg: e.message } : { code: "internal", msg: "fake failed" };
      this.write(c, { t: "res", id, ok: false, err });
    }
  }

  private get(sessionId: unknown): Thread {
    const t = typeof sessionId === "string" ? this.threads.get(sessionId) : undefined;
    if (t === undefined) throw new Fail("not_found", "no such session");
    return t;
  }

  private call(c: Client | null, m: MethodName, p: Record<string, unknown>): unknown {
    switch (m) {
      case "sessions.list":
        return [...this.threads.values()].map((t) => this.withPending(t.session));
      case "sessions.create": {
        const t = this.addThread(this.id("ses"), typeof p["title"] === "string" ? p["title"] : "");
        this.broadcast("session.created", t.session);
        return t.session;
      }
      case "sessions.rename":
        this.touch(this.get(p["id"]).session.id, { title: String(p["title"]) });
        return {};
      case "sessions.delete": {
        const t = this.get(p["id"]);
        this.threads.delete(t.session.id);
        this.approvals = this.approvals.filter((a) => a.session_id !== t.session.id);
        this.broadcast("session.deleted", t.session);
        return {};
      }
      case "sessions.focus":
        if (p["id"] !== "") this.get(p["id"]);
        if (c !== null) c.focus = String(p["id"]);
        return {};
      case "messages.list":
        return this.page(this.get(p["session_id"]), p["before"], p["limit"]);
      case "chat.send":
        return this.send(this.get(p["session_id"]), String(p["text"]), p["idempotency_key"]);
      case "roster.get":
        return this.get(p["session_id"]).roster;
      case "queue.list":
        return this.get(p["session_id"]).queue;
      case "queue.steer":
      case "queue.cancel": {
        const t = this.get(p["session_id"]);
        const item = t.queue.find((q) => q.id === p["item_id"]);
        if (item === undefined) throw new Fail("not_found", "no such item");
        t.queue = t.queue.filter((q) => q !== item);
        this.emit("queue.changed", { session_id: t.session.id, items: t.queue }, t.session.id);
        if (m === "queue.steer") {
          this.steered.push(item.id);
          const agent = t.roster.find((l) => l.id === item.agent);
          if (agent !== undefined) this.stream(t, agent, item.text, 0);
        }
        return {};
      }
      case "changes.get":
        return this.get(p["session_id"]).changes;
      case "mode.get":
        return { mode: this.get(p["session_id"]).session.mode };
      case "mode.set": {
        const t = this.get(p["session_id"]);
        if (p["mode"] === "yolo" && !(this.opts.allowYolo ?? false)) {
          throw new Fail("yolo_disabled", "This device may not switch to yolo.");
        }
        this.touch(t.session.id, { mode: String(p["mode"]) });
        this.broadcast("mode.changed", { session_id: t.session.id, mode: p["mode"] });
        return { mode: p["mode"] };
      }
      case "models.list":
        // No session: the kind's models apart from any thread.
        if (p["session_id"] !== "" && p["session_id"] !== undefined) this.get(p["session_id"]);
        this.modelLists.push(String(p["kind"]));
        return [
          { id: "default", label: "Default", selected: true },
          { id: "fast", label: "Fast" },
          { id: "deep", label: "Deep", efforts: ["low", "high"] },
        ];
      case "models.set": {
        const t = this.get(p["session_id"]);
        t.roster = t.roster.map((l) => (l.kind === p["kind"] ? { ...l, model: String(p["model"]) } : l));
        this.broadcast("roster.changed", { session_id: t.session.id });
        return {};
      }
      case "approvals.list":
        return this.approvals.filter((a) => p["session_id"] === undefined || a.session_id === p["session_id"]);
      case "approvals.answer": {
        const t = this.get(p["session_id"]);
        const a = this.approvals.find((x) => x.id === p["id"] && x.session_id === t.session.id);
        if (a === undefined) throw new Fail("not_found", "no such approval");
        this.approvals = this.approvals.filter((x) => x !== a);
        this.answered.push({ id: a.id, decision: String(p["decision"]) });
        this.broadcast("approval.resolved", { id: a.id, session_id: a.session_id, granted: p["decision"] !== "deny" });
        this.touch(t.session.id, {});
        return { resolved: true };
      }
      case "agent.interrupt": {
        const t = this.get(p["session_id"]);
        if (!t.roster.some((l) => l.id === p["agent"])) throw new Fail("not_found", "no such agent");
        this.interrupted.push(String(p["agent"]));
        for (const m of t.messages) {
          if (m.agent !== p["agent"] || m.finished) continue;
          const stopped: Message = { ...m, finished: true, updated_at: this.now() };
          t.messages = t.messages.map((x) => (x.id === m.id ? stopped : x));
          this.emit("message.updated", stopped, t.session.id);
        }
        this.setBusy(t, String(p["agent"]), false);
        return {};
      }
      case "chat.cancel":
        this.get(p["session_id"]);
        return {};
      case "chat.continue":
        this.continued.push(this.get(p["session_id"]).session.id);
        return {};
      case "message.edit": {
        const t = this.get(p["session_id"]);
        const m = t.messages.find((x) => x.id === p["message_id"] && x.role === "user");
        if (m === undefined) throw new Fail("not_found", "no such message");
        this.edited.push({ message_id: m.id, text: String(p["text"]) });
        const next: Message = { ...m, edited: true, updated_at: this.now(), parts: [{ type: "text", text: String(p["text"]) }] };
        t.messages = t.messages.map((x) => (x.id === m.id ? next : x));
        this.emit("message.updated", next, t.session.id);
        return {};
      }
      case "devices.list":
        return [];
      case "devices.revoke":
        return {};
      case "push.status":
        return { available: false, enabled: false, approvals: false, done: false };
      case "push.subscribe":
      case "push.unsubscribe":
      case "push.test":
        return {};
    }
  }

  // ---- behaviour ----------------------------------------------------------

  private page(t: Thread, before: unknown, limit: unknown): unknown {
    const end = typeof before === "string" ? Number(before.slice(1)) : t.messages.length;
    const n = typeof limit === "number" ? limit : 50;
    const start = Math.max(0, end - n);
    const out: Record<string, unknown> = { messages: t.messages.slice(start, end), more: start > 0 };
    if (start > 0) out["cursor"] = `c${start}`;
    return out;
  }

  // Leading mentions pick the agents, as Deck's addressing does: "$kind+"
  // adds a member, "$kind" reaches the first one (or adds it), "$id" one
  // member, "$all" every member. No mention reaches the first member.
  private send(t: Thread, text: string, key: unknown): unknown {
    if (typeof key === "string") {
      const known = this.idem.get(key);
      if (known !== undefined) return { message_id: known };
    }
    this.sent.push({ session_id: t.session.id, text });
    const user = this.message(t, "user", undefined, text, true);
    if (typeof key === "string") this.idem.set(key, user.id);
    const targets: AgentLine[] = [];
    let rest = text.trim();
    for (;;) {
      // "$" or "@", as Deck reads both; "@src/a.ts" is a path, not a name.
      const m = /^[$@]([\w-]+)([+*]?)(?![\w/-]|\.\S)\s*/.exec(rest);
      if (m === null) break;
      rest = rest.slice(m[0].length);
      const [, name = "", suffix] = m;
      if (name === "all") targets.push(...t.roster);
      else if (suffix === "+" && KINDS.includes(name)) targets.push(this.addMember(t, name));
      else if (KINDS.includes(name)) targets.push(t.roster.find((l) => l.kind === name) ?? this.addMember(t, name));
      else {
        const l = t.roster.find((x) => x.id === name || x.name === name);
        const p = this.profiles.find((x) => x.name.toLowerCase() === name.toLowerCase());
        if (l !== undefined) targets.push(l);
        else if (p !== undefined) targets.push(this.addMember(t, p.kind, p));
      }
    }
    if (targets.length === 0) targets.push(t.roster[0] ?? this.addMember(t, "claude"));
    const spawn = SPAWN.exec(rest);
    if (spawn !== null && targets[0] !== undefined && !targets[0].busy) {
      const parent = targets[0];
      const n = COUNT[spawn[1]!.toLowerCase()] ?? Number(spawn[1]);
      const at = (this.opts.tickMs ?? 150) * (STREAM_STEPS + 1);
      const timer = setTimeout(() => {
        this.timers.delete(timer);
        if (this.threads.has(t.session.id)) this.spawn(t, parent, spawn[2]!.toLowerCase(), n, rest);
      }, at);
      this.timers.add(timer);
    }
    // A busy agent gets the message when its turn ends, as Deck's queue does:
    // one item per agent, its text without the mentions.
    for (const [i, agent] of [...new Set(targets)].entries()) {
      if (agent.busy) {
        t.queue = [...t.queue, { id: `${user.id}:${agent.id}`, agent: agent.id, text: rest, at: this.now() }];
        this.emit("queue.changed", { session_id: t.session.id, items: t.queue }, t.session.id);
      } else this.stream(t, agent, rest, i);
    }
    return { message_id: user.id };
  }

  // An agent starts n agents of kind with a brief, as agent.spawn does: each
  // joins the room, the brief shows as the spawner's relay ("→ $name"), and
  // each works on it and answers its spawner.
  private spawn(t: Thread, parent: AgentLine, kind: string, n: number, brief: string): void {
    for (let i = 0; i < n; i++) {
      const child = this.addMember(t, kind);
      this.spawned.push({ session_id: t.session.id, parent: parent.id, name: child.id, kind });
      this.message(t, "assistant", parent.id, `→ $${child.id}\n\n${brief}`, true);
      this.stream(t, child, `@${parent.id} ${brief}`, i);
    }
  }

  // The spawned agents of a room as agents tree nodes.
  spawnedNodes(sessionId: string): Array<Record<string, unknown>> {
    const t = this.threads.get(sessionId);
    return this.spawned
      .filter((s) => s.session_id === sessionId)
      .map((s) => {
        const busy = t?.roster.find((l) => l.id === s.name)?.busy === true;
        return { name: s.name, kind: s.kind, model: "default", status: busy ? "working" : "idle", depth: 1, parent: s.parent };
      });
  }

  private addMember(t: Thread, kind: string, profile?: { name: string; role?: string }): AgentLine {
    const n = t.roster.filter((l) => l.kind === kind).length + 1;
    const line: AgentLine =
      profile === undefined
        ? { id: `${kind}-${n}`, name: `${kind}-${n}`, kind, model: "default", busy: false }
        : { id: profile.name, name: profile.name, kind, model: "default", busy: false, profile: profile.name, ...(profile.role ? { role: profile.role } : {}) };
    t.roster = [...t.roster, line];
    this.broadcast("roster.changed", { session_id: t.session.id });
    return line;
  }

  private setBusy(t: Thread, agent: string, busy: boolean): void {
    t.roster = t.roster.map((l) => (l.id === agent ? { ...l, busy } : l));
    const any = t.roster.some((l) => l.busy);
    if (any !== t.session.busy) {
      t.session = { ...t.session, busy: any };
      this.broadcast("busy", { session_id: t.session.id, busy: any });
    }
    this.broadcast("roster.changed", { session_id: t.session.id });
  }

  // Streams a reply word by word: message.created, then message.updated
  // until finished. Agents that start together interleave.
  private stream(t: Thread, agent: AgentLine, body: string, offset: number): void {
    const words = `**${agent.id}** on it. You asked: _${body || "nothing"}_. I read the code and the tests pass.`.split(" ");
    const msg = this.message(t, "assistant", agent.id, "", false);
    this.setBusy(t, agent.id, true);
    const tick = this.opts.tickMs ?? 150;
    for (let step = 1; step <= STREAM_STEPS; step++) {
      const timer = setTimeout(
        () => {
          this.timers.delete(timer);
          if (!this.threads.has(t.session.id)) return;
          const done = step === STREAM_STEPS;
          const shown = words.slice(0, Math.ceil((words.length * step) / STREAM_STEPS)).join(" ");
          const cur = t.messages.find((m) => m.id === msg.id);
          if (cur === undefined || cur.finished) return;
          const next: Message = { ...cur, updated_at: this.now(), finished: done, parts: [{ type: "text", text: shown }] };
          t.messages = t.messages.map((m) => (m.id === msg.id ? next : m));
          this.emit("message.updated", next, t.session.id);
          if (done) {
            this.setBusy(t, agent.id, false);
            this.touch(t.session.id, { last_text: shown });
          }
        },
        tick * step + offset * Math.round(tick / 3),
      );
      this.timers.add(timer);
    }
  }

  private message(t: Thread, role: Message["role"], agent: string | undefined, text: string, finished: boolean): Message {
    const at = this.now();
    const m: Message = {
      id: this.id("msg"),
      session_id: t.session.id,
      role,
      created_at: at,
      updated_at: at,
      finished,
      parts: text === "" ? [] : [{ type: "text", text }],
      ...(agent === undefined ? {} : { agent }),
    };
    t.messages = [...t.messages, m];
    this.emit("message.created", m, t.session.id);
    if (text !== "") this.touch(t.session.id, { last_text: text });
    return m;
  }

  private touch(id: string, patch: Partial<Session>): void {
    const t = this.threads.get(id);
    if (t === undefined) return;
    t.session = { ...t.session, ...patch, updated_at: this.now() };
    this.broadcast("session.updated", this.withPending(t.session));
  }

  private withPending(s: Session): Session {
    return { ...s, pending: this.approvals.filter((a) => a.session_id === s.id).length };
  }

  private now(): string {
    this.clock += 1000;
    return new Date(this.clock).toISOString();
  }

  private id(prefix: string): string {
    return `${prefix}_${this.next++}`;
  }

  private addThread(id: string, title: string): Thread {
    const t: Thread = {
      session: { id, title, updated_at: this.now(), busy: false, pending: 0, mode: "ask" },
      messages: [],
      roster: [],
      queue: [],
      changes: [],
    };
    this.threads.set(id, t);
    return t;
  }

  private seed(): void {
    const long = this.addThread("ses_long", "Long history");
    long.roster = [{ id: "claude-1", name: "claude-1", kind: "claude", model: "default", busy: false }];
    for (let i = 1; i <= 130; i++) {
      this.message(long, i % 2 === 1 ? "user" : "assistant", i % 2 === 1 ? undefined : "claude-1", `Note ${i}`, true);
    }

    if (this.opts.bigRoom !== undefined) {
      const big = this.addThread("ses_big", "Big room");
      big.roster = [{ id: "claude-1", name: "claude-1", kind: "claude", model: "default", busy: false }];
      for (let i = 1; i <= this.opts.bigRoom; i++) {
        this.message(big, i % 2 === 1 ? "user" : "assistant", i % 2 === 1 ? undefined : "claude-1", `Big ${i}`, true);
      }
    }

    const docs = this.addThread("ses_docs", "Docs pass");
    docs.roster = [{ id: "claude-1", name: "claude-1", kind: "claude", model: "default", busy: false }];
    this.message(docs, "user", undefined, "Update the README for the new flags.", true);
    this.message(docs, "assistant", "claude-1", "I will run the tests before I edit the README.", true);
    this.approvals.push({
      id: "apr_docs",
      session_id: docs.session.id,
      agent: "claude-1",
      kind: "command",
      description: "Run a command",
      command: "go test ./...",
      created_at: this.now(),
    });

    const main = this.addThread("ses_main", "Fix the pairing lockout");
    main.roster = [
      { id: "claude-1", name: "claude-1", kind: "claude", model: "default", effort: "high", busy: false },
      { id: "codex-1", name: "codex-1", kind: "codex", model: "default", busy: false },
    ];
    this.message(main, "user", undefined, "$claude $codex Why does the limiter lock everyone out?", true);
    const reply = this.message(main, "assistant", "claude-1", "", true);
    reply.parts = [
      { type: "reasoning", text: "Read the limiter first." },
      { type: "tool_call", id: "tc_1", name: "bash", summary: "go test ./pkg/remote/..." },
      { type: "tool_result", tool_call_id: "tc_1", name: "bash", output: "ok  \tgithub.com/dev-dami/deck/pkg/remote/auth\t0.41s\n" },
      {
        type: "text",
        text: "The limiter counts failures from **every** address in one bucket.\n\n- Twenty bad pairs lock out all phones\n- The fix keys the count by `addr`\n\n```go\nl.fail(addr)\n```",
      },
    ];
    main.messages = main.messages.map((m) => (m.id === reply.id ? reply : m));
    this.message(main, "assistant", "codex-1", "I agree. I also checked that a valid token is never blocked.", true);
    main.queue = [{ id: "q_1", agent: "codex-1", text: "Also run go vet when you are done.", at: this.now() }];
    main.changes = [
      {
        path: "pkg/remote/auth/limit.go",
        added: 3,
        removed: 1,
        diff: "@@ -12,7 +12,9 @@ func (l *Limiter) Fail(addr string) {\n \tl.mu.Lock()\n-\tglobal.fail()\n+\tl.fail(addr)\n+\t// Per address, never global (R1).\n+\tl.prune()\n \tl.mu.Unlock()\n",
      },
      { path: "pkg/remote/auth/limit_test.go", added: 12, removed: 0, diff: "@@ -0,0 +1,3 @@\n+func TestLimiterIsPerAddress(t *testing.T) {\n+\t// twenty failures from one address\n+}\n" },
    ];
  }
}
