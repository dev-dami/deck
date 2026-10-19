// A fake `deck serve --rpc`: the deck.v1.Deck service of
// proto/deck/v1/deck.proto on a Unix socket, behind the bearer token, over
// the same in-memory project as the v2 fake (fake-server.ts). It follows
// pkg/rpc: times in Unix milliseconds, no truncation flags, every event of
// every session on Events, opened by a Ready.
import { randomBytes } from "node:crypto";
import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import type { MethodName } from "../../pkg/remote/web/src/protocol";
import { Fail, FakeDeck, type FakeOptions } from "./fake-server";

export const PROTO = join(__dirname, "..", "..", "proto", "deck", "v1", "deck.proto");

type Obj = Record<string, unknown>;

const TIME_KEYS = new Set(["updated_at", "created_at", "at"]);

// A v2 entity as pkg/rpc's convert.go writes it: times to milliseconds, the
// *_truncated flags gone.
export function protoOf(v: unknown): unknown {
  if (Array.isArray(v)) return v.map(protoOf);
  if (typeof v !== "object" || v === null) return v;
  const out: Obj = {};
  for (const [k, x] of Object.entries(v)) {
    if (k.endsWith("_truncated")) continue;
    out[k] = TIME_KEYS.has(k) && typeof x === "string" ? Date.parse(x) : protoOf(x);
  }
  return out;
}

const CODES: Record<string, grpc.status> = {
  not_found: grpc.status.NOT_FOUND,
  invalid: grpc.status.INVALID_ARGUMENT,
  yolo_disabled: grpc.status.PERMISSION_DENIED,
};

const opt = (v: unknown): unknown => (v === "" || v === 0 ? undefined : v);

// Each unary method: the v2 method it runs, its params from the request,
// and the response from the v2 result.
const UNARY: Record<string, { m: MethodName; req: (r: Obj) => Obj; res: (r: unknown) => unknown }> = {
  ListSessions: { m: "sessions.list", req: () => ({}), res: (r) => ({ sessions: r }) },
  CreateSession: { m: "sessions.create", req: (r) => ({ title: r["title"] }), res: (r) => r },
  RenameSession: { m: "sessions.rename", req: (r) => ({ id: r["session_id"], title: r["title"] }), res: () => ({}) },
  DeleteSession: { m: "sessions.delete", req: (r) => ({ id: r["session_id"] }), res: () => ({}) },
  ListMessages: {
    m: "messages.list",
    req: (r) => ({ session_id: r["session_id"], before: opt(r["before"]), limit: opt(r["limit"]) }),
    res: (r) => r,
  },
  Send: { m: "chat.send", req: (r) => ({ session_id: r["session_id"], text: r["text"] }), res: (r) => r },
  Cancel: { m: "chat.cancel", req: (r) => ({ session_id: r["session_id"] }), res: () => ({}) },
  Continue: { m: "chat.continue", req: (r) => ({ session_id: r["session_id"] }), res: () => ({}) },
  Interrupt: { m: "agent.interrupt", req: (r) => ({ session_id: r["session_id"], agent: r["agent"] }), res: () => ({}) },
  Queue: { m: "queue.list", req: (r) => ({ session_id: r["session_id"] }), res: (r) => ({ items: r }) },
  Steer: { m: "queue.steer", req: (r) => ({ session_id: r["session_id"], item_id: r["item_id"] }), res: () => ({}) },
  CancelQueued: { m: "queue.cancel", req: (r) => ({ session_id: r["session_id"], item_id: r["item_id"] }), res: () => ({}) },
  Roster: { m: "roster.get", req: (r) => ({ session_id: r["session_id"] }), res: (r) => ({ agents: r }) },
  Models: { m: "models.list", req: (r) => ({ session_id: r["session_id"], kind: r["kind"] }), res: (r) => ({ models: r }) },
  SetModel: {
    m: "models.set",
    req: (r) => ({ session_id: r["session_id"], kind: r["kind"], model: r["model"], effort: opt(r["effort"]) }),
    res: () => ({}),
  },
  GetMode: { m: "mode.get", req: (r) => ({ session_id: r["session_id"] }), res: (r) => r },
  SetMode: { m: "mode.set", req: (r) => ({ session_id: r["session_id"], mode: r["mode"] }), res: () => ({}) },
  Approvals: { m: "approvals.list", req: (r) => ({ session_id: opt(r["session_id"]) }), res: (r) => ({ approvals: r }) },
  Answer: {
    m: "approvals.answer",
    req: (r) => ({ session_id: r["session_id"], id: r["id"], decision: r["decision"] }),
    res: (r) => r,
  },
  Changes: { m: "changes.get", req: (r) => ({ session_id: r["session_id"] }), res: (r) => ({ files: r }) },
};

function clean(o: Obj): Obj {
  return Object.fromEntries(Object.entries(o).filter(([, v]) => v !== undefined));
}

export class FakeRpcDeck {
  readonly token = randomBytes(32).toString("hex");
  readonly sock = join(mkdtempSync(join(tmpdir(), "deck-rpc-")), "deck.sock");
  readonly epoch = `e${randomBytes(4).toString("hex")}`;
  // Every call the fake got, by method, with its token check passed.
  readonly calls: string[] = [];
  private readonly server = new grpc.Server();
  private readonly streams = new Set<grpc.ServerWritableStream<Obj, Obj>>();
  private seq = 0;

  private constructor(readonly deck: FakeDeck) {}

  static async start(opts: FakeOptions = {}): Promise<FakeRpcDeck> {
    const f = new FakeRpcDeck(await FakeDeck.start({ allowYolo: true, ...opts }));
    const def = protoLoader.loadSync(PROTO, { keepCase: true, longs: Number, defaults: true, oneofs: true });
    const pkg = grpc.loadPackageDefinition(def) as unknown as {
      deck: { v1: { Deck: { service: grpc.ServiceDefinition } } };
    };
    f.server.addService(pkg.deck.v1.Deck.service, f.handlers());
    await new Promise<void>((resolve, reject) =>
      f.server.bindAsync(`unix:${f.sock}`, grpc.ServerCredentials.createInsecure(), (err) =>
        err === null ? resolve() : reject(err),
      ),
    );
    f.deck.listeners.add((e, p) => f.publish(e, p));
    return f;
  }

  // The line `deck serve --rpc` prints.
  get handshake(): string {
    return JSON.stringify({ v: 1, sock: this.sock, token: this.token, pid: process.pid, version: "fake" });
  }

  // Ends every Events stream, as a Deck under load would (RESOURCE_EXHAUSTED).
  dropStreams(): void {
    // grpc-js ends a server stream with a status when it emits "error".
    for (const s of this.streams) s.emit("error", { code: grpc.status.RESOURCE_EXHAUSTED, details: "fell behind" });
    this.streams.clear();
  }

  async close(): Promise<void> {
    this.server.forceShutdown();
    await this.deck.close();
  }

  private authorized(md: grpc.Metadata): boolean {
    return md.get("authorization").some((v) => v === `Bearer ${this.token}`);
  }

  private publish(e: string, p: unknown): void {
    this.seq++;
    const event = { seq: this.seq, [e.replace(".", "_")]: protoOf(p) };
    for (const s of this.streams) s.write(event);
  }

  private handlers(): grpc.UntypedServiceImplementation {
    const h: grpc.UntypedServiceImplementation = {
      Info: (call: grpc.ServerUnaryCall<Obj, Obj>, cb: grpc.sendUnaryData<Obj>) => {
        if (!this.authorized(call.metadata)) return cb({ code: grpc.status.UNAUTHENTICATED, details: "missing or wrong token" });
        this.calls.push("Info");
        cb(null, { version: "fake", project: "deck", kinds: ["claude", "codex", "opencode"], theme: protoOf(this.deck.theme) });
      },
      Events: (call: grpc.ServerWritableStream<Obj, Obj>) => {
        if (!this.authorized(call.metadata)) {
          call.destroy({ code: grpc.status.UNAUTHENTICATED, details: "missing or wrong token" } as grpc.ServiceError);
          return;
        }
        this.calls.push("Events");
        const asked = String(call.request["epoch"] ?? "");
        const seq = Number(call.request["seq"] ?? 0);
        // The fake keeps no ring: a client that missed events must refetch.
        const resync = asked !== "" && (asked !== this.epoch || seq !== this.seq);
        call.write({ seq: 0, ready: { epoch: this.epoch, seq: this.seq, resync } });
        this.streams.add(call);
        call.on("cancelled", () => this.streams.delete(call));
      },
    };
    for (const [name, spec] of Object.entries(UNARY)) {
      h[name] = (call: grpc.ServerUnaryCall<Obj, Obj>, cb: grpc.sendUnaryData<Obj>) => {
        if (!this.authorized(call.metadata)) return cb({ code: grpc.status.UNAUTHENTICATED, details: "missing or wrong token" });
        this.calls.push(name);
        try {
          cb(null, protoOf(spec.res(this.deck.invoke(spec.m, clean(spec.req(call.request))))) as Obj);
        } catch (e) {
          const code = e instanceof Fail ? (CODES[e.code] ?? grpc.status.INTERNAL) : grpc.status.INTERNAL;
          cb({ code, details: e instanceof Error ? e.message : "fake failed" });
        }
      };
    }
    return h;
  }
}
