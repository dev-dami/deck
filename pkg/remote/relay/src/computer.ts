// One ComputerDO per computer (Cloudflare Workers + SQLite storage): the
// coordination atom of relay/v1 (docs/mobile-contract.md §5). It bridges one
// host socket with phone sockets across lanes, holds the mailbox and snapshot
// slot, and fans push out. Plaintext envelope only; content stays E2E.
import { DurableObject } from "cloudflare:workers";
import { allowedPushEndpoint, importVapidPrivate, postPush } from "./push";
import {
  b64ToBytes,
  b64urlToBytes,
  bytesToB64,
  CLOCK_SKEW_MS,
  CLOSE_HOST_REPLACED,
  CLOSE_HOST_RESTARTED,
  CLOSE_RATE_LIMITED,
  CLOSE_REVOKED,
  CLOSE_TOKEN_INVALID,
  CLOSE_UNKNOWN_CID,
  ED25519_RAW_BYTES,
  intField,
  isRecord,
  MAX_LANES_PER_DEVICE,
  MAX_MAIL_BYTES,
  MAX_MAIL_BYTES_PER_CID,
  MAX_MAIL_TTL_MS,
  MAX_MAILS_PER_CID,
  MAX_PAIR_ATTEMPTS,
  MAX_RELAY_FRAME_BYTES,
  MAX_SNAPSHOT_BYTES,
  parseHostFrame,
  RELAY_CHALLENGE_CTX,
  sha256Hex,
  strField,
  tokenHash,
} from "./relay";

export interface RelayEnv {
  COMPUTER: DurableObjectNamespace<ComputerDO>;
  ASSETS?: Fetcher;
  VAPID_SUBJECT?: string;
  VAPID_PRIVATE_KEY?: string;
  VAPID_PUBLIC_KEY?: string;
}

interface Lane {
  id: string;
  deviceHash: string | null;
  pair: boolean;
  phones: Set<WebSocket>;
}

function json(obj: unknown, status = 200, extra?: HeadersInit): Response {
  return new Response(JSON.stringify(obj), {
    status,
    headers: { "content-type": "application/json", "cache-control": "no-store", ...extra },
  });
}

type SqlRow = Record<string, SqlStorageValue>;

function queryOne(
  sql: SqlStorage,
  query: string,
  ...params: SqlStorageValue[]
): SqlRow | undefined {
  const rows = sql.exec(query, ...params).toArray();
  return rows[0];
}

function close(ws: WebSocket, code: number, reason = ""): void {
  try {
    ws.close(code, reason);
  } catch {
    // Already gone.
  }
}

export class ComputerDO extends DurableObject<RelayEnv> {
  private sql: SqlStorage;
  private cid = "";
  private relayPub: Uint8Array | null = null;
  private devices = new Set<string>();
  private host: WebSocket | null = null;
  private hostAuthed = false;
  // Host sockets that have connected but not yet proved they own the cid. They
  // never affect the authenticated host, so knowing a cid is not enough to
  // knock a computer offline.
  private candidates = new Map<
    WebSocket,
    { challenge: string; timer: ReturnType<typeof setTimeout> }
  >();
  private lanes = new Map<string, Lane>();
  private waitingDevice = new Map<string, Set<WebSocket>>();
  private waitingPair = new Map<string, Set<WebSocket>>();
  private pendingAuth = new Map<
    WebSocket,
    { kind: "dev" | "pair"; timer: ReturnType<typeof setTimeout> }
  >();
  private mailBytes = 0;
  private mailCount = 0;
  private ipHits = new Map<string, { count: number; start: number }>();

  constructor(state: DurableObjectState, env: RelayEnv) {
    super(state, env);
    this.sql = state.storage.sql;
    state.blockConcurrencyWhile(async () => {
      this.sql.exec(`CREATE TABLE IF NOT EXISTS meta (k TEXT PRIMARY KEY, v TEXT NOT NULL)`);
      this.sql.exec(
        `CREATE TABLE IF NOT EXISTS devices (token_hash TEXT PRIMARY KEY, created_at INTEGER NOT NULL)`,
      );
      this.sql.exec(
        `CREATE TABLE IF NOT EXISTS mail (id TEXT PRIMARY KEY, device_hash TEXT NOT NULL, b64 TEXT NOT NULL, size INTEGER NOT NULL, expires_at INTEGER NOT NULL, created_at INTEGER NOT NULL)`,
      );
      this.sql.exec(
        `CREATE TABLE IF NOT EXISTS snapshots (device_hash TEXT PRIMARY KEY, b64 TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
      );
      this.sql.exec(
        `CREATE TABLE IF NOT EXISTS push_endpoints (device_hash TEXT PRIMARY KEY, endpoint TEXT NOT NULL, updated_at INTEGER NOT NULL)`,
      );
      this.sql.exec(
        `CREATE TABLE IF NOT EXISTS pair_windows (pair_id_hash TEXT PRIMARY KEY, exp INTEGER NOT NULL, attempts INTEGER NOT NULL)`,
      );
      this.sql.exec(
        `CREATE TABLE IF NOT EXISTS lanes (id TEXT PRIMARY KEY, device_hash TEXT, pair INTEGER NOT NULL)`,
      );
      const cidRow = queryOne(this.sql, `SELECT v FROM meta WHERE k = 'cid'`);
      this.cid = typeof cidRow?.["v"] === "string" ? (cidRow["v"] as string) : "";
      const pubRow = queryOne(this.sql, `SELECT v FROM meta WHERE k = 'relay_pub'`);
      if (typeof pubRow?.["v"] === "string" && (pubRow["v"] as string) !== "") {
        const raw = b64ToBytes(pubRow["v"] as string);
        this.relayPub = raw;
      }
      for (const row of this.sql.exec(`SELECT token_hash FROM devices`).toArray()) {
        if (typeof row["token_hash"] === "string") this.devices.add(row["token_hash"]);
      }
      for (const row of this.sql.exec(`SELECT id, device_hash, pair FROM lanes`).toArray()) {
        const id = row["id"];
        if (typeof id === "string") {
          this.lanes.set(id, {
            id,
            deviceHash: typeof row["device_hash"] === "string" ? row["device_hash"] : null,
            pair: row["pair"] === 1,
            phones: new Set(),
          });
        }
      }
      const mailRow = queryOne(
        this.sql,
        `SELECT COUNT(*) AS c, COALESCE(SUM(size), 0) AS s FROM mail`,
      );
      this.mailCount = typeof mailRow?.["c"] === "number" ? (mailRow["c"] as number) : 0;
      this.mailBytes = typeof mailRow?.["s"] === "number" ? (mailRow["s"] as number) : 0;
      // Sockets hibernated across an eviction re-auth: the host gets a fresh
      // challenge below, phones wait for lanes to re-open.
      for (const ws of this.ctx.getWebSockets()) {
        const tag = ws.deserializeAttachment() as {
          kind?: string;
          hash?: string;
          lane?: string;
          authed?: boolean;
        } | null;
        if (tag?.kind === "host" && tag.authed === true) {
          // An authenticated host survives the eviction as it was.
          this.host = ws;
          this.hostAuthed = true;
        } else if (tag?.kind === "dev" && typeof tag.hash === "string") {
          this.rehydratePhone(ws, tag.lane, tag.hash, false);
        } else if (tag?.kind === "pair" && typeof tag.hash === "string") {
          this.rehydratePhone(ws, tag.lane, tag.hash, true);
        } else {
          // An unauthenticated socket does not survive: it re-connects.
          close(ws, CLOSE_TOKEN_INVALID);
        }
      }
    });
  }

  private rateLimited(ip: string, key: string, max: number, windowMs: number): boolean {
    const now = Date.now();
    const k = `${ip}|${key}`;
    const hit = this.ipHits.get(k);
    if (hit === undefined || now - hit.start > windowMs) {
      this.ipHits.set(k, { count: 1, start: now });
      return false;
    }
    hit.count += 1;
    return hit.count > max;
  }

  // ---- HTTP (called via the Worker's stub.fetch) ----------------------------

  override async fetch(req: Request): Promise<Response> {
    const url = new URL(req.url);
    const path = url.pathname;
    const ip = req.headers.get("x-client-ip") ?? "unknown";
    if (path.endsWith("/internal/register") && req.method === "POST") {
      return this.handleRegister(req);
    }
    if (
      path.endsWith("/internal/host") ||
      path.endsWith("/internal/dev") ||
      path.endsWith("/internal/pair")
    ) {
      const kind = path.endsWith("/host") ? "host" : path.endsWith("/dev") ? "dev" : "pair";
      if (req.headers.get("upgrade")?.toLowerCase() !== "websocket") {
        return new Response("want websocket", { status: 426 });
      }
      return this.acceptSocket(kind, ip);
    }
    if (path.endsWith("/mail") && req.method === "POST") return this.handleMailPost(req, ip);
    if (path.endsWith("/snap") && req.method === "GET") return this.handleSnapGet(req);
    if (path.endsWith("/push") && req.method === "PUT") return this.handlePushPut(req);
    if (path.endsWith("/status") && req.method === "GET") return this.handleStatus();
    return new Response("not found", { status: 404 });
  }

  private async handleRegister(req: Request): Promise<Response> {
    let body: unknown;
    try {
      body = await req.json();
    } catch {
      return json({ error: "invalid" }, 400);
    }
    if (!isRecord(body) || typeof body["relay_pub"] !== "string")
      return json({ error: "invalid" }, 400);
    const raw = b64urlToBytes(body["relay_pub"]);
    if (raw === null || raw.length !== ED25519_RAW_BYTES) return json({ error: "invalid" }, 400);
    try {
      await crypto.subtle.importKey("raw", raw, { name: "Ed25519" }, false, ["verify"]);
    } catch {
      return json({ error: "invalid" }, 400);
    }
    const digest = await crypto.subtle.digest("SHA-256", raw);
    const bytes = new Uint8Array(digest);
    let b64 = bytesToB64(bytes).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
    b64 = b64.slice(0, 22);
    if (this.cid === "") {
      this.cid = b64;
      this.sql.exec(
        `INSERT OR REPLACE INTO meta (k, v) VALUES ('cid', ?), ('relay_pub', ?)`,
        b64,
        bytesToB64(raw),
      );
      this.relayPub = raw;
    } else if (this.cid !== b64) {
      return json({ error: "invalid" }, 400);
    }
    return json({ cid: this.cid });
  }

  private bearer(req: Request): string | null {
    const h = req.headers.get("authorization") ?? "";
    const m = /^Bearer (.+)$/.exec(h);
    return m?.[1] !== undefined && m[1] !== "" ? m[1] : null;
  }

  private async deviceOf(req: Request): Promise<string | null> {
    const token = this.bearer(req);
    if (token === null) return null;
    const hash = await tokenHash(token);
    return this.devices.has(hash) ? hash : null;
  }

  private async handleMailPost(req: Request, ip: string): Promise<Response> {
    if (this.cid === "") return json({ error: "unknown_cid" }, 404);
    if (this.rateLimited(ip, "mail", 60, 60_000)) {
      return json({ error: "rate_limited" }, 429, { "retry-after": "60" });
    }
    const deviceHash = await this.deviceOf(req);
    if (deviceHash === null) return json({ error: "unauthorized" }, 401);
    let body: unknown;
    try {
      body = await req.json();
    } catch {
      return json({ error: "invalid" }, 400);
    }
    if (!isRecord(body)) return json({ error: "invalid" }, 400);
    const id = body["id"];
    const b64 = body["b64"];
    const expires = body["expires_at"];
    if (
      typeof id !== "string" ||
      id === "" ||
      typeof b64 !== "string" ||
      typeof expires !== "string"
    ) {
      return json({ error: "invalid" }, 400);
    }
    const raw = b64ToBytes(b64);
    if (raw === null || raw.length > MAX_MAIL_BYTES) return json({ error: "too_large" }, 413);
    const expMs = Date.parse(expires);
    if (
      Number.isNaN(expMs) ||
      expMs < Date.now() - CLOCK_SKEW_MS ||
      expMs > Date.now() + MAX_MAIL_TTL_MS
    ) {
      return json({ error: "expired" }, 400);
    }
    if (
      this.mailCount >= MAX_MAILS_PER_CID ||
      this.mailBytes + raw.length > MAX_MAIL_BYTES_PER_CID
    ) {
      return json({ error: "too_large" }, 413);
    }
    const now = Date.now();
    const exists = queryOne(this.sql, `SELECT size FROM mail WHERE id = ?`, id);
    if (exists !== undefined) return json({ ok: true });
    this.sql.exec(
      `INSERT INTO mail (id, device_hash, b64, size, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
      id,
      deviceHash,
      b64,
      raw.length,
      expMs,
      now,
    );
    this.mailCount += 1;
    this.mailBytes += raw.length;
    await this.scheduleAlarm();
    if (this.host !== null && this.hostAuthed) {
      this.sendHost({ t: "mail", id, device: deviceHash, b64 });
    }
    return json({ ok: true });
  }

  private async handleSnapGet(req: Request): Promise<Response> {
    if (this.cid === "") return json({ error: "unknown_cid" }, 404);
    const deviceHash = await this.deviceOf(req);
    if (deviceHash === null) return json({ error: "unauthorized" }, 401);
    const row = queryOne(this.sql, `SELECT b64 FROM snapshots WHERE device_hash = ?`, deviceHash);
    if (row === undefined || typeof row["b64"] !== "string")
      return json({ error: "not_found" }, 404);
    const seen = queryOne(this.sql, `SELECT v FROM meta WHERE k = 'last_seen'`);
    return json({
      b64: row["b64"],
      computer: {
        online: this.hostAuthed,
        last_seen: this.hostAuthed
          ? new Date().toISOString()
          : typeof seen?.["v"] === "string"
            ? (seen["v"] as string)
            : null,
      },
    });
  }

  private async handlePushPut(req: Request): Promise<Response> {
    if (this.cid === "") return json({ error: "unknown_cid" }, 404);
    const deviceHash = await this.deviceOf(req);
    if (deviceHash === null) return json({ error: "unauthorized" }, 401);
    let body: unknown;
    try {
      body = await req.json();
    } catch {
      return json({ error: "invalid" }, 400);
    }
    if (!isRecord(body) || typeof body["endpoint"] !== "string")
      return json({ error: "invalid" }, 400);
    let endpoint: URL;
    try {
      endpoint = new URL(body["endpoint"]);
    } catch {
      return json({ error: "invalid" }, 400);
    }
    if (!allowedPushEndpoint(endpoint)) return json({ error: "invalid" }, 400);
    this.sql.exec(
      `INSERT OR REPLACE INTO push_endpoints (device_hash, endpoint, updated_at) VALUES (?, ?, ?)`,
      deviceHash,
      endpoint.toString(),
      Date.now(),
    );
    return json({ ok: true });
  }

  private handleStatus(): Response {
    if (this.cid === "") return json({ error: "unknown_cid" }, 404);
    const seen = queryOne(this.sql, `SELECT v FROM meta WHERE k = 'last_seen'`);
    return json({
      online: this.hostAuthed,
      last_seen: this.hostAuthed
        ? new Date().toISOString()
        : typeof seen?.["v"] === "string"
          ? seen["v"]
          : null,
    });
  }

  // ---- WebSockets -----------------------------------------------------------

  private acceptSocket(kind: "host" | "dev" | "pair", ip: string): Response {
    const pair = new WebSocketPair();
    const client = pair[0];
    const server = pair[1];
    if (this.cid === "") {
      // Never registered: complete the upgrade, then refuse with 4404 so a
      // phone can tell "no such computer" from "bad token".
      this.ctx.acceptWebSocket(server, ["gone"]);
      close(server, CLOSE_UNKNOWN_CID);
      return new Response(null, { status: 101, webSocket: client });
    }
    if (kind !== "host" && this.rateLimited(ip, `ws-${kind}`, 30, 60_000)) {
      // A socket must be accepted before it can be closed with a code.
      this.ctx.acceptWebSocket(server, ["gone"]);
      close(server, CLOSE_RATE_LIMITED, "too many connections");
      return new Response(null, { status: 101, webSocket: client });
    }
    if (kind === "host") {
      if (this.candidates.size >= 4) {
        this.ctx.acceptWebSocket(server, ["gone"]);
        close(server, CLOSE_RATE_LIMITED, "too many connections");
        return new Response(null, { status: 101, webSocket: client });
      }
      this.ctx.acceptWebSocket(server, ["host"]);
      server.serializeAttachment({ kind: "host", authed: false });
      this.startCandidate(server);
    } else {
      this.ctx.acceptWebSocket(server, [kind]);
      server.serializeAttachment({ kind });
      const timer = setTimeout(() => {
        this.pendingAuth.delete(server);
        close(server, CLOSE_TOKEN_INVALID, "auth timeout");
      }, 5000);
      this.pendingAuth.set(server, { kind, timer });
    }
    return new Response(null, { status: 101, webSocket: client });
  }

  // A host socket gets its own challenge and five seconds to answer it.
  private startCandidate(ws: WebSocket): void {
    const rand = crypto.getRandomValues(new Uint8Array(16));
    const challenge = Array.from(rand, (x) => x.toString(16).padStart(2, "0")).join("");
    const timer = setTimeout(() => {
      if (this.candidates.delete(ws)) close(ws, CLOSE_TOKEN_INVALID, "auth timeout");
    }, 5000);
    this.candidates.set(ws, { challenge, timer });
    try {
      ws.send(JSON.stringify({ t: "challenge", nonce: challenge }));
    } catch {
      // The socket is gone; the close handler cleans up.
    }
  }

  private sendHost(frame: Record<string, unknown>): void {
    if (this.host === null || !this.hostAuthed) return;
    try {
      this.host.send(JSON.stringify(frame));
    } catch {
      // The socket is gone; the close handler cleans up.
    }
  }

  override async webSocketMessage(ws: WebSocket, message: string | ArrayBuffer): Promise<void> {
    if (ws === this.host) {
      if (typeof message !== "string") return;
      await this.onHostFrame(message);
      return;
    }
    if (this.candidates.has(ws)) {
      if (typeof message === "string") await this.onCandidateFrame(ws, message);
      else this.dropCandidate(ws, CLOSE_TOKEN_INVALID);
      return;
    }
    const pending = this.pendingAuth.get(ws);
    if (pending !== undefined) {
      if (typeof message !== "string") {
        this.dropPending(ws, CLOSE_TOKEN_INVALID);
        return;
      }
      await this.onFirstPhoneFrame(ws, pending.kind, message);
      return;
    }
    // Authed phone socket: binary frames ride the lane to the host.
    if (typeof message === "string") return;
    const lane = this.laneOf(ws);
    if (lane === null || this.host === null || !this.hostAuthed) return;
    const bytes = new Uint8Array(message);
    if (bytes.length > MAX_RELAY_FRAME_BYTES) return;
    this.sendHost({ t: "data", lane: lane.id, b64: bytesToB64(bytes) });
  }

  override async webSocketClose(ws: WebSocket, code: number): Promise<void> {
    await this.detach(ws, code);
  }

  override async webSocketError(ws: WebSocket): Promise<void> {
    await this.detach(ws, 1011);
  }

  override async alarm(): Promise<void> {
    const now = Date.now();
    const expired = this.sql.exec(`SELECT id, size FROM mail WHERE expires_at < ?`, now).toArray();
    for (const row of expired) {
      if (typeof row["id"] === "string") {
        this.sql.exec(`DELETE FROM mail WHERE id = ?`, row["id"]);
        this.mailCount -= 1;
        this.mailBytes -= typeof row["size"] === "number" ? row["size"] : 0;
      }
    }
    this.sql.exec(`DELETE FROM pair_windows WHERE exp * 1000 < ?`, now);
    if (this.hostAuthed) {
      this.sql.exec(
        `INSERT OR REPLACE INTO meta (k, v) VALUES ('last_seen', ?)`,
        new Date(now).toISOString(),
      );
    }
    await this.scheduleAlarm();
  }

  private async scheduleAlarm(): Promise<void> {
    const pending = queryOne(this.sql, `SELECT COUNT(*) AS c FROM mail`);
    if (
      (typeof pending?.["c"] === "number" ? (pending["c"] as number) : 0) > 0 ||
      this.hostAuthed
    ) {
      const current = await this.ctx.storage.getAlarm();
      if (current === null) await this.ctx.storage.setAlarm(Date.now() + 60_000);
    }
  }

  private dropPending(ws: WebSocket, code: number): void {
    const p = this.pendingAuth.get(ws);
    if (p !== undefined) {
      clearTimeout(p.timer);
      this.pendingAuth.delete(ws);
    }
    close(ws, code);
  }

  private laneOf(ws: WebSocket): Lane | null {
    for (const lane of this.lanes.values()) {
      if (lane.phones.has(ws)) return lane;
    }
    return null;
  }

  private newLaneId(): string {
    const rand = crypto.getRandomValues(new Uint8Array(12));
    return Array.from(rand, (x) => x.toString(16).padStart(2, "0")).join("");
  }

  // A phone that has authenticated gets its own lane and the host is told to
  // open the other end. With no host connected it waits, and gets its lane the
  // moment one authenticates.
  private attachPhone(ws: WebSocket, key: string, pair: boolean): void {
    if (!this.hostAuthed) {
      const waiting = pair ? this.waitingPair : this.waitingDevice;
      let set = waiting.get(key);
      if (set === undefined) {
        set = new Set();
        waiting.set(key, set);
      }
      set.add(ws);
      return;
    }
    if (!pair) {
      let count = 0;
      for (const l of this.lanes.values()) if (l.deviceHash === key) count += 1;
      if (count >= MAX_LANES_PER_DEVICE) {
        close(ws, CLOSE_RATE_LIMITED, "too many connections");
        return;
      }
    }
    const id = this.newLaneId();
    this.lanes.set(id, { id, deviceHash: pair ? null : key, pair, phones: new Set([ws]) });
    this.sql.exec(
      `INSERT OR REPLACE INTO lanes (id, device_hash, pair) VALUES (?, ?, ?)`,
      id,
      pair ? null : key,
      pair ? 1 : 0,
    );
    ws.serializeAttachment({ kind: pair ? "pair" : "dev", hash: key, lane: id });
    this.sendHost(
      pair ? { t: "open", lane: id, pair: true } : { t: "open", lane: id, device: key },
    );
  }

  // Phones that connected while no host was there get their lanes now.
  private openWaiting(): void {
    for (const [key, set] of this.waitingDevice) {
      for (const ws of set) this.attachPhone(ws, key, false);
      set.clear();
    }
    for (const [key, set] of this.waitingPair) {
      for (const ws of set) this.attachPhone(ws, key, true);
      set.clear();
    }
  }

  // After an eviction a phone socket comes back with the lane it had, if that
  // lane is still known; otherwise it waits for a new one.
  private rehydratePhone(
    ws: WebSocket,
    laneId: string | undefined,
    key: string,
    pair: boolean,
  ): void {
    const lane = laneId !== undefined ? this.lanes.get(laneId) : undefined;
    if (lane !== undefined && this.hostAuthed) {
      lane.phones.add(ws);
      return;
    }
    this.attachPhone(ws, key, pair);
  }

  private async onFirstPhoneFrame(
    ws: WebSocket,
    kind: "dev" | "pair",
    text: string,
  ): Promise<void> {
    let v: unknown;
    try {
      v = JSON.parse(text);
    } catch {
      this.dropPending(ws, CLOSE_TOKEN_INVALID);
      return;
    }
    if (!isRecord(v)) {
      this.dropPending(ws, CLOSE_TOKEN_INVALID);
      return;
    }
    const pending = this.pendingAuth.get(ws);
    if (pending === undefined) {
      close(ws, CLOSE_TOKEN_INVALID);
      return;
    }
    clearTimeout(pending.timer);
    this.pendingAuth.delete(ws);
    if (kind === "dev") {
      const token = v["token"];
      if (typeof token !== "string") {
        close(ws, CLOSE_TOKEN_INVALID);
        return;
      }
      const hash = await tokenHash(token);
      if (!this.devices.has(hash)) {
        close(ws, this.cid === "" ? CLOSE_UNKNOWN_CID : CLOSE_TOKEN_INVALID);
        return;
      }
      ws.serializeAttachment({ kind: "dev", hash });
      this.attachPhone(ws, hash, false);
    } else {
      const pairId = v["pair_id"];
      if (typeof pairId !== "string") {
        close(ws, CLOSE_TOKEN_INVALID);
        return;
      }
      const hash = await sha256Hex(pairId);
      const win = queryOne(
        this.sql,
        `SELECT exp, attempts FROM pair_windows WHERE pair_id_hash = ?`,
        hash,
      );
      const now = Date.now();
      if (
        win === undefined ||
        typeof win["exp"] !== "number" ||
        (win["exp"] as number) * 1000 < now
      ) {
        await this.burnPairOnMiss();
        close(
          ws,
          this.pairMisses() >= MAX_PAIR_ATTEMPTS ? CLOSE_RATE_LIMITED : CLOSE_TOKEN_INVALID,
        );
        return;
      }
      const attempts = typeof win["attempts"] === "number" ? win["attempts"] : 0;
      if (attempts >= MAX_PAIR_ATTEMPTS) {
        this.sql.exec(`DELETE FROM pair_windows WHERE pair_id_hash = ?`, hash);
        this.sendHost({ t: "pair_close" });
        close(ws, CLOSE_RATE_LIMITED, "too many attempts");
        return;
      }
      this.sql.exec(
        `UPDATE pair_windows SET attempts = ? WHERE pair_id_hash = ?`,
        attempts + 1,
        hash,
      );
      ws.serializeAttachment({ kind: "pair", hash });
      this.attachPhone(ws, hash, true);
    }
  }

  private pairMisses(): number {
    const row = queryOne(this.sql, `SELECT v FROM meta WHERE k = 'pair_misses'`);
    return typeof row?.["v"] === "string" ? Number.parseInt(row["v"] as string, 10) || 0 : 0;
  }

  // A wrong pair id is a guessing attempt: after MAX_PAIR_ATTEMPTS misses the
  // window burns so the code cannot be brute-forced through the relay. The
  // daemon still burns the slot itself after failed handshakes.
  private async burnPairOnMiss(): Promise<void> {
    const misses = this.pairMisses() + 1;
    this.sql.exec(`INSERT OR REPLACE INTO meta (k, v) VALUES ('pair_misses', ?)`, String(misses));
    if (misses >= MAX_PAIR_ATTEMPTS) {
      this.sql.exec(`DELETE FROM pair_windows`);
      this.sendHost({ t: "pair_close" });
    }
  }

  private async detach(ws: WebSocket, code: number): Promise<void> {
    const pending = this.pendingAuth.get(ws);
    if (pending !== undefined) {
      clearTimeout(pending.timer);
      this.pendingAuth.delete(ws);
    }
    const cand = this.candidates.get(ws);
    if (cand !== undefined) {
      clearTimeout(cand.timer);
      this.candidates.delete(ws);
      return;
    }
    if (ws === this.host) {
      this.host = null;
      this.hostAuthed = false;
      this.sql.exec(
        `INSERT OR REPLACE INTO meta (k, v) VALUES ('last_seen', ?)`,
        new Date().toISOString(),
      );
      // The daemon's end of every lane died with its connection, so the phones
      // on them cannot be served by the next one: let them reconnect.
      for (const [id, lane] of this.lanes) {
        for (const phone of lane.phones)
          close(phone, CLOSE_HOST_RESTARTED, "computer reconnecting");
        this.lanes.delete(id);
      }
      this.sql.exec(`DELETE FROM lanes`);
      return;
    }
    for (const [id, lane] of this.lanes) {
      if (!lane.phones.delete(ws)) continue;
      if (lane.phones.size === 0) {
        this.lanes.delete(id);
        this.sql.exec(`DELETE FROM lanes WHERE id = ?`, id);
        this.sendHost({ t: "close", lane: id, code });
      }
    }
    for (const set of this.waitingDevice.values()) set.delete(ws);
    for (const set of this.waitingPair.values()) set.delete(ws);
  }

  private async verifyHostAuth(sigB64: string, challenge: string): Promise<boolean> {
    if (this.relayPub === null || this.cid === "") return false;
    const sig = b64urlToBytes(sigB64);
    if (sig === null || sig.length !== 64) return false;
    let key: CryptoKey;
    try {
      key = await crypto.subtle.importKey("raw", this.relayPub, { name: "Ed25519" }, false, [
        "verify",
      ]);
    } catch {
      return false;
    }
    const msg = new TextEncoder().encode(`${RELAY_CHALLENGE_CTX}${this.cid}|${challenge}`);
    try {
      return await crypto.subtle.verify({ name: "Ed25519" }, key, sig, msg);
    } catch {
      return false;
    }
  }

  private dropCandidate(ws: WebSocket, code: number): void {
    const c = this.candidates.get(ws);
    if (c !== undefined) clearTimeout(c.timer);
    this.candidates.delete(ws);
    close(ws, code);
  }

  // A host socket proves it holds the computer's key. Only then does it replace
  // the current host; a failed or abandoned attempt changes nothing.
  private async onCandidateFrame(ws: WebSocket, text: string): Promise<void> {
    const cand = this.candidates.get(ws);
    const frame = parseHostFrame(text);
    if (cand === undefined || frame === null || frame.t !== "auth") {
      if (cand !== undefined) this.dropCandidate(ws, CLOSE_TOKEN_INVALID);
      return;
    }
    const sig = strField(frame.p, "sig");
    if (sig === null || !(await this.verifyHostAuth(sig, cand.challenge))) {
      this.dropCandidate(ws, CLOSE_TOKEN_INVALID);
      return;
    }
    if (!this.candidates.has(ws)) return; // closed while verifying
    clearTimeout(cand.timer);
    this.candidates.delete(ws);

    const old = this.host;
    this.host = ws;
    this.hostAuthed = true;
    ws.serializeAttachment({ kind: "host", authed: true });
    if (old !== null && old !== ws) {
      // Detach the old host's lanes first: they belong to its connection.
      this.host = ws;
      for (const [id, lane] of this.lanes) {
        for (const phone of lane.phones)
          close(phone, CLOSE_HOST_RESTARTED, "computer reconnecting");
        this.lanes.delete(id);
      }
      this.sql.exec(`DELETE FROM lanes`);
      close(old, CLOSE_HOST_REPLACED, "replaced");
    }
    this.sendHost({ t: "ready" });
    this.openWaiting();
    await this.flushMail();
  }

  private async onHostFrame(text: string): Promise<void> {
    const frame = parseHostFrame(text);
    if (frame === null) return;
    const { t, p } = frame;
    switch (t) {
      case "devices": {
        const hashes = p["hashes"];
        if (!Array.isArray(hashes) || !hashes.every((x): x is string => typeof x === "string"))
          return;
        const next = new Set(hashes);
        for (const gone of this.devices) {
          if (!next.has(gone)) await this.revokeDevice(gone);
        }
        this.sql.exec(`DELETE FROM devices`);
        const now = Date.now();
        for (const h of next) {
          this.sql.exec(
            `INSERT OR IGNORE INTO devices (token_hash, created_at) VALUES (?, ?)`,
            h,
            now,
          );
        }
        this.devices = next;
        break;
      }
      case "pair_open": {
        const hash = strField(p, "pair_id_hash");
        const exp = intField(p, "exp");
        if (hash === null || exp === null) return;
        this.sql.exec(
          `INSERT OR REPLACE INTO pair_windows (pair_id_hash, exp, attempts) VALUES (?, ?, 0)`,
          hash,
          exp,
        );
        this.sql.exec(`INSERT OR REPLACE INTO meta (k, v) VALUES ('pair_misses', '0')`);
        break;
      }
      case "pair_close": {
        this.sql.exec(`DELETE FROM pair_windows`);
        for (const set of this.waitingPair.values()) {
          for (const ws of set) close(ws, CLOSE_TOKEN_INVALID);
          set.clear();
        }
        break;
      }
      case "data": {
        const lane = strField(p, "lane");
        const b64 = strField(p, "b64");
        if (lane === null || b64 === null) return;
        const l = this.lanes.get(lane);
        if (l === undefined || l.phones.size === 0) return;
        const raw = b64ToBytes(b64);
        if (raw === null || raw.length > MAX_RELAY_FRAME_BYTES) return;
        for (const ws of l.phones) {
          try {
            ws.send(raw);
          } catch {
            // Detach on next close event.
          }
        }
        break;
      }
      case "close": {
        const lane = strField(p, "lane");
        const code = intField(p, "code") ?? 1000;
        if (lane === null) return;
        const l = this.lanes.get(lane);
        if (l === undefined) return;
        for (const ws of l.phones) close(ws, code);
        this.lanes.delete(lane);
        this.sql.exec(`DELETE FROM lanes WHERE id = ?`, lane);
        break;
      }
      case "ack": {
        const id = strField(p, "id");
        if (id === null) return;
        const row = queryOne(this.sql, `SELECT size FROM mail WHERE id = ?`, id);
        if (row !== undefined) {
          this.sql.exec(`DELETE FROM mail WHERE id = ?`, id);
          this.mailCount -= 1;
          this.mailBytes -= typeof row["size"] === "number" ? row["size"] : 0;
        }
        break;
      }
      case "snap": {
        const hash = strField(p, "device_hash");
        const b64 = strField(p, "b64");
        if (hash === null || b64 === null) return;
        const raw = b64ToBytes(b64);
        if (raw === null || raw.length > MAX_SNAPSHOT_BYTES) return;
        this.sql.exec(
          `INSERT OR REPLACE INTO snapshots (device_hash, b64, updated_at) VALUES (?, ?, ?)`,
          hash,
          b64,
          Date.now(),
        );
        break;
      }
      case "push": {
        const hash = strField(p, "device_hash");
        const b64 = strField(p, "b64");
        const ttl = intField(p, "ttl") ?? 3600;
        const urgency = strField(p, "urgency");
        if (hash === null || b64 === null) return;
        if (urgency !== "normal" && urgency !== "high") return;
        const raw = b64ToBytes(b64);
        if (raw === null) return;
        const ep = queryOne(
          this.sql,
          `SELECT endpoint FROM push_endpoints WHERE device_hash = ?`,
          hash,
        );
        if (ep === undefined || typeof ep["endpoint"] !== "string") return;
        const env = this.env as RelayEnv;
        if (env.VAPID_PRIVATE_KEY === undefined || env.VAPID_PUBLIC_KEY === undefined) return;
        // The stored endpoint is checked again: it is what the relay will POST to.
        let target: URL;
        try {
          target = new URL(ep["endpoint"]);
        } catch {
          return;
        }
        if (!allowedPushEndpoint(target)) return;
        let key: CryptoKey;
        try {
          key = await importVapidPrivate(env.VAPID_PRIVATE_KEY);
        } catch {
          return;
        }
        this.ctx.waitUntil(
          postPush(
            {
              subject: env.VAPID_SUBJECT ?? "mailto:relay@example.invalid",
              privateKey: key,
              publicB64url: env.VAPID_PUBLIC_KEY,
            },
            { endpoint: ep["endpoint"], payload: raw, ttl, urgency },
          ).then(
            () => undefined,
            () => undefined,
          ),
        );
        break;
      }
      default:
        break;
    }
  }

  private async revokeDevice(hash: string): Promise<void> {
    for (const [id, lane] of this.lanes) {
      if (lane.deviceHash === hash) {
        for (const ws of lane.phones) close(ws, CLOSE_REVOKED);
        this.lanes.delete(id);
        this.sql.exec(`DELETE FROM lanes WHERE id = ?`, id);
      }
    }
    const waiting = this.waitingDevice.get(hash);
    if (waiting !== undefined) {
      for (const ws of waiting) close(ws, CLOSE_REVOKED);
      waiting.clear();
    }
    this.sql.exec(`DELETE FROM snapshots WHERE device_hash = ?`, hash);
    this.sql.exec(`DELETE FROM push_endpoints WHERE device_hash = ?`, hash);
    const dropped = queryOne(
      this.sql,
      `SELECT COUNT(*) AS c, COALESCE(SUM(size), 0) AS s FROM mail WHERE device_hash = ?`,
      hash,
    );
    this.sql.exec(`DELETE FROM mail WHERE device_hash = ?`, hash);
    this.mailCount -= typeof dropped?.["c"] === "number" ? (dropped["c"] as number) : 0;
    this.mailBytes -= typeof dropped?.["s"] === "number" ? (dropped["s"] as number) : 0;
  }

  private async flushMail(): Promise<void> {
    if (this.host === null || !this.hostAuthed) return;
    const now = Date.now();
    for (const row of this.sql
      .exec(`SELECT id, device_hash, b64 FROM mail WHERE expires_at >= ? ORDER BY created_at`, now)
      .toArray()) {
      if (typeof row["id"] === "string" && typeof row["b64"] === "string") {
        this.sendHost({ t: "mail", id: row["id"], device: row["device_hash"], b64: row["b64"] });
      }
    }
  }
}
