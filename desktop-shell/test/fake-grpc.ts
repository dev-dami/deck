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

  // The project's work tree, as the git calls change it.
  readonly git = {
    branch: "main",
    branches: ["main", "feature/limiter"],
    dirty: 2,
    ahead: 1,
    commits: [] as string[],
    pushed: 0,
    pr: null as Obj | null,
  };
  // Each terminal: what it printed and who watches it.
  private readonly terms = new Map<string, { out: string; line: string; subs: Set<grpc.ServerWritableStream<Obj, Obj>> }>();

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
    const unary = (name: string, f: (r: Obj) => Obj) => {
      h[name] = (call: grpc.ServerUnaryCall<Obj, Obj>, cb: grpc.sendUnaryData<Obj>) => {
        if (!this.authorized(call.metadata)) return cb({ code: grpc.status.UNAUTHENTICATED, details: "missing or wrong token" });
        this.calls.push(name);
        try {
          cb(null, f(call.request));
        } catch (e) {
          cb({ code: grpc.status.FAILED_PRECONDITION, details: e instanceof Error ? e.message : "fake failed" });
        }
      };
    };
    const g = this.git;
    unary("GitStatus", () => ({
      repo: true,
      branch: g.branch,
      upstream: `origin/${g.branch}`,
      ahead: g.ahead,
      behind: 0,
      dirty: g.dirty,
      default_branch: "main",
      gh: true,
      branches: g.branches.map((name, i) => ({ name, upstream: `origin/${name}`, committed_at: Date.now() - i * 3_600_000, subject: `Work on ${name}` })),
      pull_request: g.pr,
    }));
    unary("GitCheckout", (r) => {
      const b = String(r["branch"]);
      if (r["create"] === true) g.branches.unshift(b);
      else if (!g.branches.includes(b)) throw new Error(`error: pathspec '${b}' did not match any file(s) known to git`);
      g.branch = b;
      return {};
    });
    unary("GitCommit", (r) => {
      g.commits.push(String(r["message"]));
      g.dirty = 0;
      g.ahead++;
      return {};
    });
    unary("GitPush", () => {
      g.pushed++;
      g.ahead = 0;
      return {};
    });
    unary("GitPull", () => ({}));
    unary("GitCreatePR", (r) => {
      g.pr = { number: 7723, title: r["title"], state: "OPEN", url: "https://github.com/dev-dami/deck/pull/7723", draft: r["draft"] === true, base: "main", checks: [{ name: "ci", bucket: "pending", link: "" }] };
      return g.pr;
    });
    unary("GitMergePR", () => {
      if (g.pr !== null) g.pr = { ...g.pr, state: "MERGED" };
      return {};
    });
    unary("Usage", (r) => {
      const days = Number(r["days"] ?? 0) || 30;
      const day = 86_400_000;
      const midnight = new Date(new Date().toDateString()).getTime();
      return {
        available: true,
        overview: { turns: 42, all_turns: 120, input_tokens: 1_250_000, output_tokens: 310_000, cached_tokens: 900_000, cost_usd: 18.42, duration_ms: 5_400_000, ttft_avg_ms: 1800, ttft_p95_ms: 4200, cache_hit_pct: 62.5, done: 38, interrupted: 3, failed: 1 },
        days: Array.from({ length: days }, (_, i) => ({ day: midnight - (days - 1 - i) * day, turns: (i * 7) % 9, tokens: ((i * 37) % 11) * 40_000, cost_usd: ((i * 37) % 11) * 0.4 })),
        kinds: [
          { kind: "claude", turns: 25, input_tokens: 800_000, output_tokens: 200_000, cost_usd: 12.1 },
          { kind: "codex", turns: 17, input_tokens: 450_000, output_tokens: 110_000, cost_usd: 6.32 },
        ],
        models: [
          { kind: "claude", model: "opus", turns: 15, input_tokens: 600_000, output_tokens: 150_000, cost_usd: 10 },
          { kind: "claude", model: "sonnet", turns: 10, input_tokens: 200_000, output_tokens: 50_000, cost_usd: 2.1 },
          { kind: "codex", model: "gpt-5", turns: 17, input_tokens: 450_000, output_tokens: 110_000, cost_usd: 6.32 },
        ],
        sessions: [{ session_id: "ses_main", title: "Fix the pairing lockout", turns: 20, tokens: 700_000, cost_usd: 9.5 }],
        limits: [{ agent_kind: "claude", window_id: "five_hour", kind: "session", label: "5-hour", used_pct: 64, resets_at: Date.now() + 2 * 3_600_000, plan: "max" }],
        heat: Array.from({ length: 140 }, (_, i) => ({ day: midnight - (139 - i) * day, turns: (i * 13) % 7, tokens: ((i * 13) % 7) * 50_000 })),
        habits: { active_days: 18, streak: 6, hours: Array.from({ length: 168 }, (_, i) => ((i % 24) >= 9 && (i % 24) <= 18 ? (i * 5) % 6 : 0)), review_median_ms: 95_000, review_p90_ms: 600_000, approval_median_ms: 2_400, approval_fast_pct: 35, user_turns: 40, redirects: 4, follow_ups: 12, prompt_median: 140, edit_turns: 20, edit_files: 64, lines_added: 2300, lines_removed: 800, reverts: 2, fit: [{ kind: "claude", class: "edit", calls: 120 }, { kind: "codex", class: "run", calls: 90 }] },
      };
    });
    const termOut = (id: string, text: string) => {
      const t = this.terms.get(id);
      if (t === undefined) return;
      t.out += text;
      for (const sub of t.subs) sub.write({ data: Buffer.from(text) });
    };
    unary("TerminalOpen", () => {
      const id = `term_${this.terms.size + 1}`;
      this.terms.set(id, { out: "fake shell\r\n$ ", line: "", subs: new Set() });
      return { terminal_id: id };
    });
    unary("TerminalWrite", (r) => {
      const id = String(r["terminal_id"]);
      const t = this.terms.get(id);
      if (t === undefined) throw new Error("no such terminal");
      for (const ch of Buffer.from(r["data"] as Uint8Array).toString("utf8")) {
        if (ch === "\r") {
          termOut(id, `\r\nran: ${t.line}\r\n$ `);
          t.line = "";
        } else {
          t.line += ch;
          termOut(id, ch);
        }
      }
      return {};
    });
    unary("TerminalResize", () => ({}));
    unary("TerminalClose", (r) => {
      const t = this.terms.get(String(r["terminal_id"]));
      for (const sub of t?.subs ?? []) {
        sub.write({ exited: true, exit_code: 0 });
        sub.end();
      }
      this.terms.delete(String(r["terminal_id"]));
      return {};
    });
    h["TerminalOutput"] = (call: grpc.ServerWritableStream<Obj, Obj>) => {
      const t = this.terms.get(String(call.request["terminal_id"]));
      if (!this.authorized(call.metadata) || t === undefined) {
        call.destroy({ code: grpc.status.NOT_FOUND, details: "no such terminal" } as grpc.ServiceError);
        return;
      }
      this.calls.push("TerminalOutput");
      call.write({ data: Buffer.from(t.out) });
      t.subs.add(call);
      call.on("cancelled", () => t.subs.delete(call));
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
