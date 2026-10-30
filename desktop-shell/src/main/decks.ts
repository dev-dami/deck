import { type ChildProcess, spawn } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";
import { delimiter, isAbsolute, join } from "node:path";
import { createInterface } from "node:readline";
import * as grpc from "@grpc/grpc-js";
import * as protoLoader from "@grpc/proto-loader";
import type {
  AgentCli,
  LocalDeck,
  LogLevel,
  OpenOutcome,
  RpcMethod,
  RpcResult,
  RpcStreamEvent,
  StartError,
  BrowserStateResponse,
} from "../shared/bridge";
import { conflictFromStatus } from "./artifactConflict";
import type { ArtifactConflict } from "../shared/artifacts";
import type { Browser, BrowserRequest } from "./browser";

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

// The binary the engine looks for, per agent kind (internal/deck/engine:
// Antigravity's CLI is agy, Pi's ACP server pi-acp).
const AGENT_BINARIES: [kind: string, binary: string][] = [
  ["claude", "claude"],
  ["codex", "codex"],
  ["opencode", "opencode"],
  ["antigravity", "agy"],
  ["pi", "pi-acp"],
];

// The agent CLIs on path, as the engine finds them.
export function findAgentClis(path: string): AgentCli[] {
  return AGENT_BINARIES.map(([kind, bin]) => ({ kind, path: findOnPath(bin, { PATH: path }) }));
}

// Whether the engine's stderr says it found no agent CLI on PATH
// (internal/deck/engine, Start).
export function noAgentCli(stderr: string): boolean {
  return /none of [a-z, ]+ was found on PATH/.test(stderr);
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
  browser_capability?: string;
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
  const capability = h["browser_capability"];
  if (capability !== undefined && (typeof capability !== "string" || !/^[0-9a-f]{32,128}$/.test(capability))) return null;
  return { v: 1, sock, token, pid: pid as number, version, ...(typeof capability === "string" ? { browser_capability: capability } : {}) };
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

// The ArtifactConflict a status carries, when it carries one.
function conflictOf(err: grpc.ServiceError): ArtifactConflict | null {
  for (const d of err.metadata?.get("grpc-status-details-bin") ?? []) {
    const found = typeof d === "string" ? null : conflictFromStatus(d);
    if (found !== null) return found;
  }
  return null;
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
      if (err !== null) {
        const failure: RpcResult = { ok: false, code: err.code, message: err.details || err.message };
        const conflict = err.code === grpc.status.ABORTED ? conflictOf(err) : null;
        resolve(conflict === null ? failure : { ...failure, artifact_conflict: conflict });
      } else resolve({ ok: true, value });
    });
  });
}

// A Deck that started and answered Info, not yet kept.
interface Started {
  ok: true;
  deck: LocalDeck;
  child: ChildProcess;
  client: DeckClient;
  tail: Tail;
  browserCapability: string;
}

interface Open {
  deck: LocalDeck;
  child: ChildProcess;
  client: DeckClient;
  stream: grpc.ClientReadableStream<unknown> | null;
  // The output stream of each terminal the window watches.
  terms: Map<string, grpc.ClientReadableStream<unknown>>;
  browserCapability: string;
  browser: Controller | null;
}

interface Controller {
  ref: { id: string; generation: string; desktop_capability: string } | null;
  stream: grpc.ClientReadableStream<unknown> | null;
  seen: Set<string>;
  updates: Promise<void>;
}

type Emit = (id: string, ev: RpcStreamEvent) => void;

// The end of what a Deck wrote to stderr, kept for the life of the process:
// it says why the Deck stopped.
interface Tail {
  text: string;
}

const TAIL_LINES = 12;

function tailLines(t: Tail): string[] {
  return t.text
    .split("\n")
    .filter((l) => l.trim() !== "")
    .slice(-TAIL_LINES);
}

// Waits for the handshake on the child's stdout. It keeps reading after
// that, so the pipe never fills. Each stderr line goes to onLine.
function readHandshake(child: ChildProcess, tail: Tail, onLine: (line: string) => void): Promise<Handshake | string> {
  return new Promise((resolve) => {
    let done = false;
    const finish = (v: Handshake | string) => {
      if (done) return;
      done = true;
      clearTimeout(timer);
      resolve(v);
    };
    const timer = setTimeout(() => finish("Deck did not start in time."), START_TIMEOUT_MS);
    if (child.stderr !== null) {
      child.stderr.on("data", (c: Buffer) => {
        tail.text = (tail.text + c.toString("utf8")).slice(-2000);
      });
      createInterface({ input: child.stderr }).on("line", onLine);
    }
    if (child.stdout !== null) {
      createInterface({ input: child.stdout }).on("line", (line) => {
        const hs = parseHandshake(line);
        if (hs !== null) finish(hs);
      });
    }
    child.on("error", (e) => finish(`Could not run deck: ${e.message}`));
    child.on("exit", () => {
      // Deck's own error line, not the usage text cobra prints after it.
      const lines = tail.text.trim().split("\n");
      const last = (lines.find((l) => l.startsWith("Error: "))?.slice("Error: ".length) ?? lines.slice(-2).join(" ")).trim();
      finish(`Deck stopped before it was ready${last === "" ? "." : `: ${last}`}`);
    });
  });
}

// The Decks this app runs on this machine, one `deck serve --rpc` per
// folder. Each process lives while its stdin is open: close() and closeAll()
// end stdin, and Deck stops. The token stays here; the window gets the
// LocalDeck, call results and events, none of which carry it.
export class RpcDecks {
  private browser: Browser | null = null;

  setBrowser(browser: Browser): void {
    this.browser = browser;
  }

  directory(id: string): string | null {
    return this.open_.get(id)?.deck.dir ?? null;
  }

  async browserUpdate(id: string, state: BrowserStateResponse): Promise<void> {
    const o = this.open_.get(id),
      control = o?.browser,
      ref = control?.ref;
    if (o === undefined || ref == null) return;
    // Loading/title events can arrive faster than RPC round trips. Serialize
    // them so an older snapshot cannot overwrite a newer takeover state.
    const update = control!.updates.then(async () => {
      if (o.browser !== control) return;
      const out = await unary(o.client, "BrowserUpdate", { controller: ref, state });
      if (!out.ok) throw new Error(out.message);
    });
    control!.updates = update.catch(() => {});
    await update;
  }

  async browserOrigin(id: string, request: BrowserRequest, origin: string): Promise<boolean> {
    const o = this.open_.get(id),
      ref = o?.browser?.ref;
    if (o === undefined || ref == null) return false;
    const out = await unary(
      o.client,
      "BrowserOrigin",
      {
        controller: ref,
        request_id: request.id,
        session_id: request.session_id,
        tab_id: "tab_id" in request.command ? request.command.tab_id : "",
        origin,
      },
      12 * 60 * 60 * 1000,
    );
    return out.ok && (out.value as { allow?: boolean }).allow === true;
  }

  private startBrowser(id: string, o: Open): void {
    if (this.browser === null || !o.browserCapability || o.browser !== null) return;
    const control: Controller = { ref: null, stream: null, seen: new Set(), updates: Promise.resolve() };
    o.browser = control;
    const mine = () => this.open_.get(id) === o && o.browser === control;
    void unary(o.client, "RegisterBrowserController", { desktop_capability: o.browserCapability }).then((out) => {
      if (!out.ok || !mine()) {
        if (mine()) o.browser = null;
        return;
      }
      const c = out.value as { id: string; generation: string };
      control.ref = { id: c.id, generation: c.generation, desktop_capability: o.browserCapability };
      const stream = (o.client["Events"] as (r: object) => grpc.ClientReadableStream<unknown>).call(o.client, {
        browser_controller: control.ref,
      });
      control.stream = stream;
      stream.on("data", (event: { browser_request?: BrowserRequest }) => {
        const r = event.browser_request;
        if (!mine() || r === undefined || r.controller_id !== c.id || r.controller_generation !== c.generation) return;
        if (r.cancelled) {
          this.browser?.cancel(r.id);
          return;
        }
        if (control.seen.has(r.id)) return;
        control.seen.add(r.id);
        void this.browser!.execute(id, r)
          .then(async (response) => {
            if (!mine()) return;
            // Publish the full state before resolving open/close, including the
            // empty final-tab update. BrowserState is immediately consistent.
            await this.browserUpdate(id, this.browser!.state(id, r.session_id));
            if (mine()) await unary(o.client, "BrowserResult", { controller: control.ref, id: r.id, response });
          })
          .catch(() => {
            this.stopBrowser(id, o);
          });
      });
      const ended = () => {
        if (mine()) this.stopBrowser(id, o);
      };
      stream.on("error", ended);
      stream.on("end", ended);
    });
  }

  private stopBrowser(id: string, o: Open): void {
    const c = o.browser;
    o.browser = null;
    c?.stream?.cancel();
    this.browser?.closeDeck(id);
    if (c?.ref != null) void unary(o.client, "UnregisterBrowserController", c.ref, 5000);
  }
  private readonly open_ = new Map<string, Open>();
  // Decks whose process ended by itself, until restart() or close().
  private readonly stopped_ = new Map<string, LocalDeck>();
  private readonly restarting = new Map<string, Promise<OpenOutcome>>();
  private next = 1;

  constructor(
    private readonly deck: string | null,
    private readonly Service: DeckClientCtor,
    private readonly emit: Emit,
    // The PATH the engine runs with (shellPath.ts); the app's own when unset.
    private readonly path: Promise<string> | null = null,
    // The app's log: each engine's stderr, and why one did not start.
    private readonly log: (level: LogLevel, message: string) => void = () => {},
  ) {}

  async open(dir: string): Promise<OpenOutcome> {
    for (const o of this.open_.values()) if (o.deck.dir === dir) return { ok: true, deck: o.deck };
    // A reloaded window opens its folder again: a stopped Deck restarts.
    for (const d of this.stopped_.values()) if (d.dir === dir) return this.restart(d.id);
    const out = await this.start(dir, `local-${this.next++}`);
    return out.ok ? { ok: true, deck: this.keep(out) } : out;
  }

  // Starts a Deck that stopped by itself again, in its folder and under the
  // same id, so the window keeps its project. Calls made while it starts
  // share the one start.
  restart(id: string): Promise<OpenOutcome> {
    const o = this.open_.get(id);
    if (o !== undefined) return Promise.resolve({ ok: true, deck: o.deck });
    const d = this.stopped_.get(id);
    if (d === undefined) return Promise.resolve({ ok: false, message: "That Deck is closed." });
    let p = this.restarting.get(id);
    if (p === undefined) {
      p = this.start(d.dir, id).then((out): OpenOutcome => {
        if (!out.ok) return out;
        // The window closed the project while the Deck started.
        if (this.stopped_.get(id) !== d) {
          out.client.close();
          out.child.stdin?.end();
          return { ok: false, message: "That Deck is closed." };
        }
        this.stopped_.delete(id);
        return { ok: true, deck: this.keep(out) };
      });
      p = p.finally(() => this.restarting.delete(id));
      this.restarting.set(id, p);
    }
    return p;
  }

  private async start(dir: string, id: string): Promise<Started | { ok: false; message: string; start?: StartError }> {
    if (this.deck === null) return { ok: false, message: "This install of Deck is missing its engine. Reinstall Deck." };
    try {
      if (!statSync(dir).isDirectory()) return { ok: false, message: "That is not a folder." };
    } catch {
      return { ok: false, message: "That folder does not exist." };
    }
    const env = this.path === null ? process.env : { ...process.env, PATH: await this.path };
    const child = spawn(this.deck, ["serve", "--rpc"], {
      cwd: dir,
      env,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    const tail: Tail = { text: "" };
    const hs = await readHandshake(child, tail, (line) => this.log("info", `deck (${dir}): ${line}`));
    if (typeof hs === "string") {
      this.log("error", `deck (${dir}) did not start: ${hs}`);
      child.kill("SIGTERM");
      return { ok: false, message: hs, start: { lines: tailLines(tail), noAgents: noAgentCli(tail.text) } };
    }
    const client = dial(this.Service, hs.sock, hs.token);
    const info = await unary(client, "Info", {}, 15_000);
    if (!info.ok) {
      this.log("error", `deck (${dir}) did not answer: ${info.message}`);
      client.close();
      child.stdin?.end();
      return { ok: false, message: `Deck did not answer: ${info.message}`, start: { lines: tailLines(tail), noAgents: false } };
    }
    this.log("info", `deck ${hs.version} started in ${dir}`);
    const project = (info.value as { project?: unknown }).project;
    const deck: LocalDeck = {
      id,
      dir,
      project: typeof project === "string" && project !== "" ? project : dir,
      version: hs.version,
    };
    return { ok: true, deck, child, client, tail, browserCapability: hs.browser_capability ?? "" };
  }

  // Keeps a started Deck. When its process ends by itself, the window gets
  // "stopped" with the last stderr lines, and the project stays open.
  private keep({ deck, child, client, tail, browserCapability }: Started): LocalDeck {
    const id = deck.id;
    const o: Open = { deck, child, client, stream: null, terms: new Map(), browserCapability, browser: null };
    this.open_.set(id, o);
    this.startBrowser(id, o);
    child.on("exit", (code) => {
      if (this.open_.get(id)?.child !== child) return;
      this.log(code === 0 ? "info" : "warn", `deck (${deck.dir}) stopped (exit ${code})`);
      this.drop(id);
      this.stopped_.set(id, deck);
      this.emit(id, {
        type: "stopped",
        message: code === 0 ? "Deck stopped." : `Deck stopped (exit ${code}).`,
        stderr: tailLines(tail),
      });
    });
    return deck;
  }

  // Whether dir is a folder this app has a Deck open in.
  isOpenDir(dir: string): boolean {
    for (const o of this.open_.values()) if (o.deck.dir === dir) return true;
    return false;
  }

  call(id: string, method: RpcMethod, request: Record<string, unknown>): Promise<RpcResult> {
    const o = this.open_.get(id);
    if (o === undefined) return Promise.resolve({ ok: false, code: grpc.status.UNAVAILABLE, message: "That Deck is closed." });
    return unary(o.client, method, request, method === "BrowserAction" ? 12 * 60 * 60 * 1000 : 120_000);
  }

  subscribe(id: string, epoch: string, seq: number): void {
    const o = this.open_.get(id);
    if (o === undefined) {
      // A stopped Deck waits for a restart; the window already knows.
      if (!this.stopped_.has(id)) this.emit(id, { type: "exit", message: "That Deck is closed." });
      return;
    }
    this.startBrowser(id, o);
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
    const stream = (o.client["TerminalOutput"] as (req: object) => grpc.ClientReadableStream<unknown>).call(o.client, {
      terminal_id: term,
    });
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
    for (const [id, o] of this.open_) {
      this.stopBrowser(id, o);
      const s = o.stream;
      o.stream = null;
      s?.cancel();
      for (const t of o.terms.values()) t.cancel();
      o.terms.clear();
    }
  }

  close(id: string): void {
    this.stopped_.delete(id);
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
    this.stopBrowser(id, o);
    this.open_.delete(id);
    const s = o.stream;
    o.stream = null;
    s?.cancel();
    for (const t of o.terms.values()) t.cancel();
    o.terms.clear();
    o.client.close();
  }
}
