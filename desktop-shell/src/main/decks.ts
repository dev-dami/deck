import { type ChildProcess, spawn } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";
import { delimiter, isAbsolute, join } from "node:path";
import { createInterface } from "node:readline";
import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import type { LocalDeck, OpenOutcome, RpcMethod, RpcResult, RpcStreamEvent } from "../shared/bridge";

// The deck binary on PATH, or null.
export function findOnPath(name: string, env: NodeJS.ProcessEnv = process.env): string | null {
  const exts = process.platform === "win32" ? [".exe", ".cmd", ""] : [""];
  for (const dir of (env["PATH"] ?? "").split(delimiter)) {
    if (dir === "") continue;
    for (const ext of exts) {
      const p = join(dir, name + ext);
      try {
        accessSync(p, constants.X_OK);
        if (statSync(p).isFile()) return p;
      } catch {
        // Not here; try the next directory.
      }
    }
  }
  return null;
}

// The Deck engine the app runs, like Claude Desktop runs the Claude Code it
// ships: DECK_BIN when set (tests, a custom build), else the binary bundled
// in bundledDir (resources/bin of the packaged app, bin/ in a checkout
// after `npm run build:deck`), else a deck on PATH.
export function findDeck(bundledDir: string, env: NodeJS.ProcessEnv = process.env): string | null {
  const own = env["DECK_BIN"];
  if (own !== undefined && own !== "" && isExecutable(own)) return own;
  const bundled = join(bundledDir, process.platform === "win32" ? "deck.exe" : "deck");
  if (isExecutable(bundled)) return bundled;
  return findOnPath("deck", env);
}

function isExecutable(p: string): boolean {
  try {
    accessSync(p, constants.X_OK);
    return statSync(p).isFile();
  } catch {
    return false;
  }
}

// The one JSON line `deck serve --rpc` prints on stdout once it listens
// (pkg/rpc/socket.go, Handshake).
export interface Handshake {
  v: 1;
  sock: string;
  token: string;
  pid: number;
  version: string;
}

export function parseHandshake(line: string): Handshake | null {
  let v: unknown;
  try {
    v = JSON.parse(line);
  } catch {
    return null;
  }
  if (typeof v !== "object" || v === null) return null;
  const h = v as Record<string, unknown>;
  if (h["v"] !== 1) return null;
  const { sock, token, pid, version } = h;
  if (typeof sock !== "string" || !isAbsolute(sock)) return null;
  if (typeof token !== "string" || !/^[0-9a-f]{32,128}$/.test(token)) return null;
  if (!Number.isInteger(pid) || typeof version !== "string") return null;
  return { v: 1, sock, token, pid: pid as number, version };
}

// The header pkg/rpc checks on every call (TokenHeader).
const TOKEN_HEADER = "authorization";
// pkg/rpc.MaxMessageBytes: a few full-size images.
const MAX_MESSAGE = 64 << 20;
const START_TIMEOUT_MS = 60_000;
const STOP_GRACE_MS = 5_000;

type DeckClient = grpc.Client & Record<string, (...args: unknown[]) => unknown>;
type DeckClientCtor = new (address: string, creds: grpc.ChannelCredentials, options?: grpc.ClientOptions) => DeckClient;

// The Deck service from the .proto. Plain objects in and out: field names as
// written (snake_case), int64 as numbers, oneofs named in `kind`.
export function loadService(protoPath: string): DeckClientCtor {
  const def = protoLoader.loadSync(protoPath, {
    keepCase: true,
    longs: Number,
    enums: String,
    defaults: true,
    oneofs: true,
  });
  const pkg = grpc.loadPackageDefinition(def) as unknown as { deck: { v1: { Deck: DeckClientCtor } } };
  return pkg.deck.v1.Deck;
}

// Adds "authorization: Bearer <token>" to each call. The socket is private
// to this user and has no TLS, so grpc-js would refuse call credentials;
// an interceptor sets the header instead.
function bearer(token: string): grpc.Interceptor {
  return (options, next) =>
    new grpc.InterceptingCall(next(options), {
      start(metadata, listener, nxt) {
        metadata.set(TOKEN_HEADER, `Bearer ${token}`);
        nxt(metadata, listener);
      },
    });
}

export function dial(Service: DeckClientCtor, sock: string, token: string): DeckClient {
  return new Service(`unix:${sock}`, grpc.credentials.createInsecure(), {
    interceptors: [bearer(token)],
    "grpc.max_receive_message_length": MAX_MESSAGE,
    "grpc.max_send_message_length": MAX_MESSAGE,
  });
}

export function unary(client: DeckClient, method: string, request: object, timeoutMs = 120_000): Promise<RpcResult> {
  return new Promise((resolve) => {
    const fn = client[method];
    if (typeof fn !== "function") {
      resolve({ ok: false, code: grpc.status.UNIMPLEMENTED, message: `no method ${method}` });
      return;
    }
    const deadline = new Date(Date.now() + timeoutMs);
    fn.call(client, request, { deadline }, (err: grpc.ServiceError | null, value: unknown) => {
      if (err !== null) resolve({ ok: false, code: err.code, message: err.details || err.message });
      else resolve({ ok: true, value });
    });
  });
}

interface Open {
  deck: LocalDeck;
  child: ChildProcess;
  client: DeckClient;
  stream: grpc.ClientReadableStream<unknown> | null;
  // The output stream of each terminal the window watches.
  terms: Map<string, grpc.ClientReadableStream<unknown>>;
}

type Emit = (id: string, ev: RpcStreamEvent) => void;

// Waits for the handshake on the child's stdout. It keeps reading after
// that, so the pipe never fills.
function readHandshake(child: ChildProcess): Promise<Handshake | string> {
  return new Promise((resolve) => {
    let stderr = "";
    let done = false;
    const finish = (v: Handshake | string) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      resolve(v);
    };
    const timer = setTimeout(() => finish("Deck did not start in time."), START_TIMEOUT_MS);
    child.stderr?.on("data", (c: Buffer) => {
      stderr = (stderr + c.toString("utf8")).slice(-2000);
    });
    if (child.stdout !== null) {
      createInterface({ input: child.stdout }).on("line", (line) => {
        const hs = parseHandshake(line);
        if (hs !== null) finish(hs);
      });
    }
    child.on("error", (e) => finish(`Could not run deck: ${e.message}`));
    child.on("exit", () => {
      const tail = stderr.trim().split("\n").slice(-2).join(" ");
      finish(`Deck stopped before it was ready${tail === "" ? "." : `: ${tail}`}`);
    });
  });
}

// The Decks this app runs on this machine, one `deck serve --rpc` per
// folder. Each process lives while its stdin is open: close() and closeAll()
// end stdin, and Deck stops. The token stays here; the window gets the
// LocalDeck, call results and events, none of which carry it.
export class RpcDecks {
  private readonly open_ = new Map<string, Open>();
  private next = 1;

  constructor(
    private readonly deck: string | null,
    private readonly Service: DeckClientCtor,
    private readonly emit: Emit,
  ) {}

  async open(dir: string): Promise<OpenOutcome> {
    if (this.deck === null) return { ok: false, message: "This install of Deck is missing its engine. Reinstall Deck." };
    try {
      if (!statSync(dir).isDirectory()) return { ok: false, message: "That is not a folder." };
    } catch {
      return { ok: false, message: "That folder does not exist." };
    }
    for (const o of this.open_.values()) if (o.deck.dir === dir) return { ok: true, deck: o.deck };

    const child = spawn(this.deck, ["serve", "--rpc"], {
      cwd: dir,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    const hs = await readHandshake(child);
    if (typeof hs === "string") {
      child.kill("SIGTERM");
      return { ok: false, message: hs };
    }
    const client = dial(this.Service, hs.sock, hs.token);
    const info = await unary(client, "Info", {}, 15_000);
    if (!info.ok) {
      client.close();
      child.stdin?.end();
      return { ok: false, message: `Deck did not answer: ${info.message}` };
    }
    const project = (info.value as { project?: unknown }).project;
    const id = `local-${this.next++}`;
    const deck: LocalDeck = {
      id,
      dir,
      project: typeof project === "string" && project !== "" ? project : dir,
      version: hs.version,
    };
    this.open_.set(id, { deck, child, client, stream: null, terms: new Map() });
    child.on("exit", (code) => {
      if (this.open_.get(id)?.child !== child) return;
      this.drop(id);
      this.emit(id, { type: "exit", message: code === 0 ? "Deck stopped." : `Deck stopped (exit ${code}).` });
    });
    return { ok: true, deck };
  }

  call(id: string, method: RpcMethod, request: Record<string, unknown>): Promise<RpcResult> {
    const o = this.open_.get(id);
    if (o === undefined) return Promise.resolve({ ok: false, code: grpc.status.UNAVAILABLE, message: "That Deck is closed." });
    return unary(o.client, method, request);
  }

  subscribe(id: string, epoch: string, seq: number): void {
    const o = this.open_.get(id);
    if (o === undefined) {
      this.emit(id, { type: "exit", message: "That Deck is closed." });
      return;
    }
    o.stream?.cancel();
    const stream = (o.client["Events"] as (req: object) => grpc.ClientReadableStream<unknown>).call(o.client, {
      epoch,
      seq,
    });
    o.stream = stream;
    stream.on("data", (event: unknown) => this.emit(id, { type: "event", event }));
    stream.on("error", (e: grpc.ServiceError) => {
      // A stream this class cancelled ends quietly.
      if (o.stream !== stream || e.code === grpc.status.CANCELLED) return;
      o.stream = null;
      this.emit(id, { type: "end", code: e.code, message: e.details || e.message });
    });
    stream.on("end", () => {
      if (o.stream !== stream) return;
      o.stream = null;
      this.emit(id, { type: "end", code: grpc.status.UNAVAILABLE, message: "The event stream ended." });
    });
  }

  // Streams a terminal's output to the window: its scrollback first, then
  // what it prints, then its exit. A second subscribe replaces the first.
  termSubscribe(id: string, term: string): void {
    const o = this.open_.get(id);
    if (o === undefined) {
      this.emit(id, { type: "term-end", term, code: grpc.status.UNAVAILABLE, message: "That Deck is closed." });
      return;
    }
    o.terms.get(term)?.cancel();
    const stream = (o.client["TerminalOutput"] as (req: object) => grpc.ClientReadableStream<unknown>).call(
      o.client,
      { terminal_id: term },
    );
    o.terms.set(term, stream);
    const mine = () => o.terms.get(term) === stream;
    stream.on("data", (chunk: { data?: Uint8Array; exited?: boolean; exit_code?: number }) => {
      if (!mine()) return;
      if (chunk.data !== undefined && chunk.data.length > 0) {
        this.emit(id, { type: "term", term, data: Buffer.from(chunk.data).toString("base64") });
      }
      if (chunk.exited === true) this.emit(id, { type: "term-exit", term, code: chunk.exit_code ?? 0 });
    });
    stream.on("error", (e: grpc.ServiceError) => {
      if (!mine() || e.code === grpc.status.CANCELLED) return;
      o.terms.delete(term);
      this.emit(id, { type: "term-end", term, code: e.code, message: e.details || e.message });
    });
    stream.on("end", () => {
      if (mine()) o.terms.delete(term);
    });
  }

  termUnsubscribe(id: string, term: string): void {
    const o = this.open_.get(id);
    const s = o?.terms.get(term);
    if (o === undefined || s === undefined) return;
    o.terms.delete(term);
    s.cancel();
  }

  // Ends the event streams and keeps the Decks: a reloaded window opens
  // its folder again and gets the same Deck.
  cancelStreams(): void {
    for (const o of this.open_.values()) {
      const s = o.stream;
      o.stream = null;
      s?.cancel();
      for (const t of o.terms.values()) t.cancel();
      o.terms.clear();
    }
  }

  close(id: string): void {
    const o = this.open_.get(id);
    if (o === undefined) return;
    this.drop(id);
    // Deck stops when its stdin closes; a kill is the fallback.
    o.child.stdin?.end();
    const kill = setTimeout(() => o.child.kill("SIGTERM"), STOP_GRACE_MS);
    kill.unref();
    o.child.once("exit", () => clearTimeout(kill));
  }

  closeAll(): void {
    for (const id of [...this.open_.keys()]) this.close(id);
  }

  private drop(id: string): void {
    const o = this.open_.get(id);
    if (o === undefined) return;
    this.open_.delete(id);
    const s = o.stream;
    o.stream = null;
    s?.cancel();
    for (const t of o.terms.values()) t.cancel();
    o.terms.clear();
    o.client.close();
  }
}
