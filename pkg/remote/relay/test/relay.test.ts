import { SELF } from "cloudflare:test";
import { afterEach, describe, expect, test } from "vitest";
import hostAuthVector from "../../wire/testdata/hostauth_ed25519.json";
import { MAX_MAIL_BYTES, MAX_MAIL_BYTES_PER_CID, MAX_MAILS_PER_CID } from "../src/relay";

const ORIGIN = "https://relay.test";
let ipSeq = 10;

const openSockets = new Set<WebSocket>();

afterEach(() => {
  for (const ws of openSockets) {
    if (ws.readyState === WebSocket.OPEN || ws.readyState === WebSocket.CONNECTING) ws.close();
  }
  openSockets.clear();
});

function ip(): string {
  ipSeq += 1;
  return `10.9.0.${ipSeq}`;
}

// Standard base64 with padding: binary payloads inside JSON frames.
function b64(bytes: Uint8Array): string {
  let s = "";
  for (let i = 0; i < bytes.length; i += 0x8000)
    s += String.fromCharCode(...bytes.subarray(i, i + 0x8000));
  return btoa(s);
}

// Unpadded base64url: keys and signatures (contract §5).
function b64u(bytes: Uint8Array): string {
  return b64(bytes).replaceAll("+", "-").replaceAll("/", "_").replaceAll("=", "");
}

async function sha256Hex(s: string): Promise<string> {
  const d = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(s));
  return Array.from(new Uint8Array(d), (x) => x.toString(16).padStart(2, "0")).join("");
}

interface HostKeys {
  pub: string; // raw 32-byte key, unpadded base64url
  priv: CryptoKey;
  cid: string;
}

async function makeHostKeys(): Promise<HostKeys> {
  const kp = (await crypto.subtle.generateKey({ name: "Ed25519" }, true, [
    "sign",
    "verify",
  ])) as CryptoKeyPair;
  const raw = new Uint8Array((await crypto.subtle.exportKey("raw", kp.publicKey)) as ArrayBuffer);
  const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", raw));
  return { pub: b64u(raw), priv: kp.privateKey, cid: b64u(digest).slice(0, 22) };
}

async function api(path: string, init: RequestInit = {}, addr = ip()): Promise<Response> {
  const res = await SELF.fetch(`${ORIGIN}${path}`, {
    ...init,
    headers: { "CF-Connecting-IP": addr, ...(init.headers ?? {}) },
  });
  // Buffer the body here so tests that only assert the status do not leave
  // streams open against Durable Object storage.
  const buf = await res.arrayBuffer();
  return new Response(buf, { status: res.status, headers: res.headers });
}

async function register(addr = ip()): Promise<HostKeys & { response: Response }> {
  const keys = await makeHostKeys();
  const response = await api(
    "/r/v1/register",
    {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ relay_pub: keys.pub }),
    },
    addr,
  );
  return { ...keys, response };
}

async function upgrade(path: string, addr = ip()): Promise<WebSocket> {
  const res = await SELF.fetch(`${ORIGIN}${path}`, {
    headers: { Upgrade: "websocket", "CF-Connecting-IP": addr },
  });
  if (res.status !== 101 || res.webSocket === undefined || res.webSocket === null) {
    throw new Error(`upgrade to ${path} failed: ${res.status}`);
  }
  const ws = res.webSocket;
  ws.accept();
  openSockets.add(ws);
  return ws;
}

// Every message a socket gets, in order, so a test can take them one by one
// without racing the next one past a listener.
class Inbox {
  private queue: Array<string | ArrayBuffer> = [];
  private waiters: Array<(m: string | ArrayBuffer) => void> = [];
  closeCode: number | null = null;
  private closeWaiters: Array<(c: number) => void> = [];

  constructor(readonly ws: WebSocket) {
    ws.addEventListener("message", (ev) => {
      const w = this.waiters.shift();
      if (w !== undefined) w(ev.data as string | ArrayBuffer);
      else this.queue.push(ev.data as string | ArrayBuffer);
    });
    ws.addEventListener("close", (ev) => {
      this.closeCode = ev.code;
      for (const w of this.closeWaiters.splice(0)) w(ev.code);
    });
  }

  next(timeoutMs = 15000): Promise<string | ArrayBuffer> {
    const held = this.queue.shift();
    if (held !== undefined) return Promise.resolve(held);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("message timeout")), timeoutMs);
      this.waiters.push((m) => {
        clearTimeout(timer);
        resolve(m);
      });
    });
  }

  async json(): Promise<Record<string, unknown>> {
    const m = await this.next();
    if (typeof m !== "string") throw new Error("expected text frame");
    return JSON.parse(m) as Record<string, unknown>;
  }

  // The next JSON frame of type t, skipping others.
  async frame(t: string): Promise<Record<string, unknown>> {
    for (;;) {
      const f = await this.json();
      if (f["t"] === t) return f;
    }
  }

  closed(timeoutMs = 15000): Promise<number> {
    if (this.closeCode !== null) return Promise.resolve(this.closeCode);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => reject(new Error("close timeout")), timeoutMs);
      this.closeWaiters.push((c) => {
        clearTimeout(timer);
        resolve(c);
      });
    });
  }

  // True if nothing arrives for ms.
  async quiet(ms: number): Promise<boolean> {
    if (this.queue.length > 0) return false;
    return new Promise((resolve) => {
      const timer = setTimeout(() => {
        const i = this.waiters.indexOf(onMsg);
        if (i >= 0) this.waiters.splice(i, 1);
        resolve(true);
      }, ms);
      const onMsg = (m: string | ArrayBuffer) => {
        clearTimeout(timer);
        this.queue.unshift(m);
        resolve(false);
      };
      this.waiters.push(onMsg);
    });
  }
}

async function signChallenge(keys: HostKeys, nonce: string): Promise<string> {
  const msg = new TextEncoder().encode(`deck-relay-v1|${keys.cid}|${nonce}`);
  return b64u(new Uint8Array(await crypto.subtle.sign({ name: "Ed25519" }, keys.priv, msg)));
}

// Connects a host socket and answers its challenge.
async function connectHost(keys: HostKeys, addr = ip()): Promise<Inbox> {
  const ws = await upgrade(`/r/v1/c/${keys.cid}/host`, addr);
  const box = new Inbox(ws);
  const challenge = await box.json();
  if (challenge["t"] !== "challenge" || typeof challenge["nonce"] !== "string") {
    throw new Error("no challenge");
  }
  ws.send(JSON.stringify({ t: "auth", sig: await signChallenge(keys, challenge["nonce"]) }));
  const ready = await box.json();
  if (ready["t"] !== "ready") throw new Error(`no ready: ${JSON.stringify(ready)}`);
  return box;
}

interface AuthedHost extends HostKeys {
  host: Inbox;
}

async function authHost(addr = ip()): Promise<AuthedHost> {
  const reg = await register(addr);
  if (reg.response.status !== 200) throw new Error(`register failed: ${reg.response.status}`);
  const body = (await reg.response.json()) as { cid: string };
  if (body.cid !== reg.cid) throw new Error("cid mismatch");
  return { pub: reg.pub, priv: reg.priv, cid: reg.cid, host: await connectHost(reg, addr) };
}

function send(ws: WebSocket, obj: unknown): void {
  ws.send(JSON.stringify(obj));
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

// A paired device: its token goes to the relay hashed, via the host's devices frame.
async function addDevice(h: AuthedHost, name: string): Promise<{ token: string; hash: string }> {
  const token = `tok-${h.cid}-${name}`;
  const hash = await sha256Hex(token);
  return { token, hash };
}

async function connectPhone(h: AuthedHost, token: string): Promise<Inbox> {
  const ws = await upgrade(`/r/v1/c/${h.cid}/dev`);
  const box = new Inbox(ws);
  ws.send(JSON.stringify({ token }));
  return box;
}

describe("register", () => {
  test("derives the cid from the raw key", async () => {
    const reg = await register();
    expect(reg.response.status).toBe(200);
    expect(await reg.response.json()).toEqual({ cid: reg.cid });
  });

  test("is idempotent for the same key", async () => {
    const reg = await register();
    const again = await api("/r/v1/register", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ relay_pub: reg.pub }),
    });
    expect(await again.json()).toEqual({ cid: reg.cid });
  });

  test("rejects bad bodies and keys of the wrong shape", async () => {
    const addr = ip();
    const spki = new Uint8Array(44); // the DER-wrapped form is not what the daemon sends
    for (const body of [
      `{}`,
      `{"relay_pub":"nope"}`,
      `{"relay_pub":"${"A".repeat(64)}"}`,
      `{"relay_pub":"${b64(spki)}"}`,
      `{"relay_pub":"${b64u(new Uint8Array(31))}"}`,
      `{"relay_pub":"${b64u(new Uint8Array(33))}"}`,
      `{"relay_pub":"${b64u(new Uint8Array(32))}=="}`,
    ]) {
      const res = await api(
        "/r/v1/register",
        { method: "POST", headers: { "content-type": "application/json" }, body },
        addr,
      );
      expect(res.status, body).toBe(400);
    }
  });

  test("unknown computer routes 404 over HTTP and 4404 over WS", async () => {
    const addr = ip();
    const res = await api(`/r/v1/c/${"A".repeat(22)}/status`, {}, addr);
    expect(res.status).toBe(404);
    const ws = await upgrade(`/r/v1/c/${"B".repeat(22)}/dev`, addr);
    await expect(new Inbox(ws).closed()).resolves.toBe(4404);
  });

  test("serves the hosted PWA from the same origin", async () => {
    const res = await api("/", {}, ip());
    expect(res.status).toBe(200);
    expect(res.headers.get("content-type") ?? "").toContain("text/html");
  });

  test("the VAPID public key is served only when configured", async () => {
    const res = await api("/r/v1/vapid", {}, ip());
    expect([200, 404]).toContain(res.status);
    if (res.status === 200) {
      const body = (await res.json()) as { public_key: string };
      expect(body.public_key.length).toBeGreaterThan(40);
    }
  });
});

describe("host auth encodings agree with the Go daemon", () => {
  test("a signature from the Go vector verifies under the relay's rules", async () => {
    for (const c of hostAuthVector.cases) {
      const raw = Uint8Array.from(
        atob(c.relay_pub.replaceAll("-", "+").replaceAll("_", "/") + "="),
        (x) => x.charCodeAt(0),
      );
      const digest = new Uint8Array(await crypto.subtle.digest("SHA-256", raw));
      expect(b64u(digest).slice(0, 22), "cid derivation").toBe(c.cid);
      expect(c.sig).toMatch(/^[A-Za-z0-9_-]{86}$/);
      const key = await crypto.subtle.importKey("raw", raw, { name: "Ed25519" }, false, ["verify"]);
      const sig = Uint8Array.from(
        atob(c.sig.replaceAll("-", "+").replaceAll("_", "/") + "=="),
        (x) => x.charCodeAt(0),
      );
      const ok = await crypto.subtle.verify(
        { name: "Ed25519" },
        key,
        sig,
        new TextEncoder().encode(c.message),
      );
      expect(ok, `signature for ${c.cid}`).toBe(true);
    }
  });

  test("the relay accepts the Go vector's key and registers the same cid", async () => {
    const c = hostAuthVector.cases[0];
    if (c === undefined) throw new Error("empty vector");
    const res = await api("/r/v1/register", {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ relay_pub: c.relay_pub }),
    });
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({ cid: c.cid });
  });

  test("a signature with url-safe characters authenticates", async () => {
    // Signatures are random; keep generating hosts until one has '-' or '_'.
    for (let i = 0; i < 40; i++) {
      const reg = await register();
      const ws = await upgrade(`/r/v1/c/${reg.cid}/host`);
      const box = new Inbox(ws);
      const nonce = (await box.json())["nonce"] as string;
      const sig = await signChallenge(reg, nonce);
      ws.send(JSON.stringify({ t: "auth", sig }));
      expect((await box.json())["t"]).toBe("ready");
      ws.close();
      if (/[-_]/.test(sig)) return;
    }
    throw new Error("never saw a url-safe signature in 40 tries");
  });

  test("a padded or standard-base64 signature is refused", async () => {
    const reg = await register();
    const ws = await upgrade(`/r/v1/c/${reg.cid}/host`);
    const box = new Inbox(ws);
    const nonce = (await box.json())["nonce"] as string;
    const sig = await signChallenge(reg, nonce);
    ws.send(JSON.stringify({ t: "auth", sig: `${sig}==` }));
    await expect(box.closed()).resolves.toBe(4401);
  });
});

describe("host auth", () => {
  test("challenge, auth, ready", async () => {
    const h = await authHost();
    const res = await api(`/r/v1/c/${h.cid}/status`, {}, ip());
    expect(await res.json()).toEqual({ online: true, last_seen: expect.any(String) });
    h.host.ws.close();
  });

  test("wrong signature closes 4401", async () => {
    const reg = await register();
    const ws = await upgrade(`/r/v1/c/${reg.cid}/host`);
    const box = new Inbox(ws);
    await box.json();
    ws.send(JSON.stringify({ t: "auth", sig: b64u(new Uint8Array(64)) }));
    await expect(box.closed()).resolves.toBe(4401);
  });

  test("a signature over another challenge is refused", async () => {
    const reg = await register();
    const ws = await upgrade(`/r/v1/c/${reg.cid}/host`);
    const box = new Inbox(ws);
    await box.json();
    ws.send(JSON.stringify({ t: "auth", sig: await signChallenge(reg, "not-the-nonce") }));
    await expect(box.closed()).resolves.toBe(4401);
  });

  test("a socket that never answers is closed", async () => {
    const reg = await register();
    const ws = await upgrade(`/r/v1/c/${reg.cid}/host`);
    const box = new Inbox(ws);
    await box.json();
    await expect(box.closed(8000)).resolves.toBe(4401);
  });

  test("a newer AUTHENTICATED host replaces the older with 4409", async () => {
    const h = await authHost();
    const second = await connectHost(h);
    await expect(h.host.closed()).resolves.toBe(4409);
    const res = await api(`/r/v1/c/${h.cid}/status`, {}, ip());
    expect(await res.json()).toEqual({ online: true, last_seen: expect.any(String) });
    second.ws.close();
  });

  test("knowing a cid is not enough to knock the host offline", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "d");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);

    // An attacker opens host sockets for the cid and never authenticates.
    const attackers: Inbox[] = [];
    for (let i = 0; i < 6; i++) {
      const ws = await upgrade(`/r/v1/c/${h.cid}/host`);
      attackers.push(new Inbox(ws));
    }
    // And one that answers with a bad signature.
    const bad = attackers[0];
    if (bad === undefined) throw new Error("no attacker");
    await bad.json();
    bad.ws.send(JSON.stringify({ t: "auth", sig: b64u(new Uint8Array(64)) }));
    await expect(bad.closed()).resolves.toBe(4401);

    // The real host is still connected and still serves phones.
    expect(h.host.closeCode).toBeNull();
    const res = await api(`/r/v1/c/${h.cid}/status`, {}, ip());
    expect(await res.json()).toEqual({ online: true, last_seen: expect.any(String) });
    const phone = await connectPhone(h, dev.token);
    const open = await h.host.frame("open");
    expect(open["device"]).toBe(dev.hash);
    send(h.host.ws, {
      t: "data",
      lane: open["lane"],
      b64: b64(new TextEncoder().encode("still up")),
    });
    expect(new TextDecoder().decode((await phone.next()) as ArrayBuffer)).toBe("still up");
    h.host.ws.close();
  });
});

describe("lanes", () => {
  test("the relay opens a lane for an arriving phone and bridges data both ways", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "a");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);

    const phone = await connectPhone(h, dev.token);
    const open = await h.host.frame("open");
    expect(open["device"]).toBe(dev.hash);
    expect(open["pair"]).toBeUndefined();
    const lane = open["lane"] as string;
    expect(lane).toMatch(/^[0-9a-f]{24}$/);

    send(h.host.ws, { t: "data", lane, b64: b64(new TextEncoder().encode("hello phone")) });
    expect(new TextDecoder().decode((await phone.next()) as ArrayBuffer)).toBe("hello phone");

    phone.ws.send(new TextEncoder().encode("hello host"));
    const up = await h.host.frame("data");
    expect(up["lane"]).toBe(lane);
    expect(atob(up["b64"] as string)).toBe("hello host");

    send(h.host.ws, { t: "close", lane, code: 4403 });
    await expect(phone.closed()).resolves.toBe(4403);
    h.host.ws.close();
  });

  test("each phone gets its own lane", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "two");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    const p1 = await connectPhone(h, dev.token);
    const l1 = (await h.host.frame("open"))["lane"];
    const p2 = await connectPhone(h, dev.token);
    const l2 = (await h.host.frame("open"))["lane"];
    expect(l1).not.toBe(l2);
    send(h.host.ws, { t: "data", lane: l2, b64: b64(new TextEncoder().encode("only p2")) });
    expect(new TextDecoder().decode((await p2.next()) as ArrayBuffer)).toBe("only p2");
    expect(await p1.quiet(200)).toBe(true);
    h.host.ws.close();
  });

  test("a host-sent open is ignored: lanes belong to the relay", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "fake");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    send(h.host.ws, { t: "open", lane: "made-up", device: dev.hash });
    await sleep(100);
    send(h.host.ws, { t: "data", lane: "made-up", b64: b64(new TextEncoder().encode("x")) });
    const phone = await connectPhone(h, dev.token);
    const open = await h.host.frame("open");
    expect(open["lane"]).not.toBe("made-up");
    expect(await phone.quiet(200)).toBe(true);
    h.host.ws.close();
  });

  test("a phone that arrives while the computer is away gets its lane when it returns", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "wait");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    h.host.ws.close();
    await sleep(150);

    const phone = await connectPhone(h, dev.token);
    await sleep(150);
    const back = await connectHost(h);
    const open = await back.frame("open");
    expect(open["device"]).toBe(dev.hash);
    send(back.ws, {
      t: "data",
      lane: open["lane"],
      b64: b64(new TextEncoder().encode("welcome back")),
    });
    expect(new TextDecoder().decode((await phone.next()) as ArrayBuffer)).toBe("welcome back");
    back.ws.close();
  });

  test("losing the host connection closes its phones so they reconnect", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "drop");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    const phone = await connectPhone(h, dev.token);
    await h.host.frame("open");
    h.host.ws.close();
    await expect(phone.closed()).resolves.toBe(1012);
  });

  test("a phone leaving tells the host and frees the lane", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "leave");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    // More connections than the per-device lane limit, one after another.
    for (let i = 0; i < 12; i++) {
      const phone = await connectPhone(h, dev.token);
      const open = await h.host.frame("open");
      phone.ws.close();
      const gone = await h.host.frame("close");
      expect(gone["lane"]).toBe(open["lane"]);
    }
    h.host.ws.close();
  });

  test("a ninth simultaneous phone for one device is refused", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "many");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    const kept: Inbox[] = [];
    for (let i = 0; i < 8; i++) {
      kept.push(await connectPhone(h, dev.token));
      await h.host.frame("open");
    }
    const ninth = await connectPhone(h, dev.token);
    await expect(ninth.closed()).resolves.toBe(4429);
    h.host.ws.close();
  });

  test("wrong token closes 4401", async () => {
    const h = await authHost();
    const phone = await connectPhone(h, "wrong");
    await expect(phone.closed()).resolves.toBe(4401);
    h.host.ws.close();
  });

  test("revoking a device drops its phone with 4403", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "rev");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    const phone = await connectPhone(h, dev.token);
    await h.host.frame("open");
    send(h.host.ws, { t: "devices", hashes: [] });
    await expect(phone.closed()).resolves.toBe(4403);
    h.host.ws.close();
  });
});

describe("pairing", () => {
  test("a pair window opens a pair lane and bridges data", async () => {
    const h = await authHost();
    send(h.host.ws, {
      t: "pair_open",
      pair_id_hash: await sha256Hex("pair-1"),
      exp: Math.floor(Date.now() / 1000) + 300,
    });
    await sleep(100);
    const ws = await upgrade(`/r/v1/c/${h.cid}/pair`);
    const phone = new Inbox(ws);
    ws.send(JSON.stringify({ pair_id: "pair-1" }));
    const open = await h.host.frame("open");
    expect(open["pair"]).toBe(true);
    expect(open["device"]).toBeUndefined();
    send(h.host.ws, {
      t: "data",
      lane: open["lane"],
      b64: b64(new TextEncoder().encode("pair-ok")),
    });
    expect(new TextDecoder().decode((await phone.next()) as ArrayBuffer)).toBe("pair-ok");
    ws.send(new TextEncoder().encode("msg1"));
    expect(atob((await h.host.frame("data"))["b64"] as string)).toBe("msg1");
    h.host.ws.close();
  });

  test("a wrong pair id never reaches the host", async () => {
    const h = await authHost();
    send(h.host.ws, {
      t: "pair_open",
      pair_id_hash: await sha256Hex("pair-real"),
      exp: Math.floor(Date.now() / 1000) + 300,
    });
    await sleep(100);
    const ws = await upgrade(`/r/v1/c/${h.cid}/pair`);
    const phone = new Inbox(ws);
    ws.send(JSON.stringify({ pair_id: "pair-guess" }));
    await expect(phone.closed()).resolves.toBe(4401);
    expect(await h.host.quiet(200)).toBe(true);
    h.host.ws.close();
  });

  test("ten bad attempts burn the window", async () => {
    const h = await authHost();
    send(h.host.ws, {
      t: "pair_open",
      pair_id_hash: await sha256Hex("pair-good"),
      exp: Math.floor(Date.now() / 1000) + 300,
    });
    for (let i = 0; i < 9; i++) {
      const phone = new Inbox(await upgrade(`/r/v1/c/${h.cid}/pair`));
      phone.ws.send(JSON.stringify({ pair_id: `pair-bad-${i}` }));
      await expect(phone.closed()).resolves.toBe(4401);
    }
    const tenth = new Inbox(await upgrade(`/r/v1/c/${h.cid}/pair`));
    tenth.ws.send(JSON.stringify({ pair_id: "pair-bad-9" }));
    await expect(tenth.closed()).resolves.toBe(4429);
    expect(await h.host.frame("pair_close")).toEqual({ t: "pair_close" });
    const last = new Inbox(await upgrade(`/r/v1/c/${h.cid}/pair`));
    last.ws.send(JSON.stringify({ pair_id: "pair-good" }));
    await expect(last.closed()).resolves.toBe(4429);
    h.host.ws.close();
  });
});

describe("mailbox and snapshot", () => {
  async function postMail(h: AuthedHost, token: string, id: string, bytes: Uint8Array) {
    return api(`/r/v1/c/${h.cid}/mail`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
      body: JSON.stringify({
        id,
        expires_at: new Date(Date.now() + 3600_000).toISOString(),
        b64: b64(bytes),
      }),
    });
  }

  test("mail waits offline, drains on connect, acks exactly once", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "mail");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    h.host.ws.close();
    await sleep(100);
    expect(
      (await postMail(h, dev.token, "0192f3a0-mail-1", new TextEncoder().encode("sealed-action")))
        .status,
    ).toBe(200);

    const host2 = await connectHost(h);
    const mail = await host2.frame("mail");
    expect(mail["id"]).toBe("0192f3a0-mail-1");
    expect(mail["device"]).toBe(dev.hash);
    expect(atob(mail["b64"] as string)).toBe("sealed-action");
    host2.ws.send(JSON.stringify({ t: "ack", id: "0192f3a0-mail-1" }));
    await sleep(100);
    host2.ws.close();
    await sleep(100);

    const host3 = await connectHost(h);
    expect(await host3.quiet(300)).toBe(true); // nothing left to deliver
    host3.ws.close();
  });

  test("mail posted while the host is connected is pushed at once", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "live");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    await postMail(h, dev.token, "live-1", new TextEncoder().encode("now"));
    expect((await h.host.frame("mail"))["id"]).toBe("live-1");
    h.host.ws.close();
  });

  test("twenty mails of 60 KiB are all accepted (the cap is 1000 mails / 16 MiB)", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "bulk");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    h.host.ws.close();
    await sleep(100);
    for (let i = 0; i < 20; i++) {
      const res = await postMail(h, dev.token, `bulk-${i}`, new Uint8Array(60 * 1024).fill(i + 1));
      expect(res.status, `mail ${i}`).toBe(200);
    }
  });

  test("the mailbox limits are the contract's", () => {
    // Filling 16 MiB through the test runtime takes minutes; the behaviour under
    // the cap is covered above, so pin the numbers themselves.
    expect(MAX_MAIL_BYTES_PER_CID).toBe(16 * 1024 * 1024);
    expect(MAX_MAILS_PER_CID).toBe(1000);
    expect(MAX_MAIL_BYTES).toBe(64 * 1024);
  });

  test("revoking a phone forgets its waiting mail", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "forget");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    h.host.ws.close();
    await sleep(100);
    await postMail(h, dev.token, "forget-1", new TextEncoder().encode("x"));
    const host2 = await connectHost(h);
    await host2.frame("mail");
    send(host2.ws, { t: "devices", hashes: [] });
    await sleep(100);
    host2.ws.close();
    await sleep(100);
    const host3 = await connectHost(h);
    expect(await host3.quiet(300)).toBe(true);
    host3.ws.close();
  });

  test("expired, oversized and anonymous mail are refused", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "limits");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    const expired = await api(`/r/v1/c/${h.cid}/mail`, {
      method: "POST",
      headers: { "content-type": "application/json", authorization: `Bearer ${dev.token}` },
      body: JSON.stringify({
        id: "x1",
        expires_at: new Date(Date.now() - 10 * 60_000).toISOString(),
        b64: b64(new TextEncoder().encode("old")),
      }),
    });
    expect(expired.status).toBe(400);
    expect((await postMail(h, dev.token, "x2", new Uint8Array(65537))).status).toBe(413);
    const anon = await api(`/r/v1/c/${h.cid}/mail`, {
      method: "POST",
      headers: { "content-type": "application/json" },
      body: JSON.stringify({ id: "x3", expires_at: new Date().toISOString(), b64: "AA==" }),
    });
    expect(anon.status).toBe(401);
    h.host.ws.close();
  });

  test("snapshot slot round-trips with online state", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "snap");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    const payload = b64(new TextEncoder().encode("encrypted-snapshot"));
    send(h.host.ws, { t: "snap", device_hash: dev.hash, b64: payload });
    await sleep(100);
    const res = await api(`/r/v1/c/${h.cid}/snap`, {
      headers: { authorization: `Bearer ${dev.token}` },
    });
    expect(res.status).toBe(200);
    expect(await res.json()).toEqual({
      b64: payload,
      computer: { online: true, last_seen: expect.any(String) },
    });
    h.host.ws.close();
    await sleep(100);
    const status = await api(`/r/v1/c/${h.cid}/status`, {}, ip());
    expect(await status.json()).toEqual({ online: false, last_seen: expect.any(String) });
  });
});

describe("push endpoints", () => {
  async function put(h: AuthedHost, token: string, endpoint: string) {
    return api(
      `/r/v1/c/${h.cid}/push`,
      {
        method: "PUT",
        headers: { "content-type": "application/json", authorization: `Bearer ${token}` },
        body: JSON.stringify({ endpoint }),
      },
      ip(),
    );
  }

  test("accepts known push services and refuses everything else", async () => {
    const h = await authHost();
    const dev = await addDevice(h, "push");
    send(h.host.ws, { t: "devices", hashes: [dev.hash] });
    await sleep(100);
    for (const ok of [
      "https://fcm.googleapis.com/fcm/send/abc",
      "https://updates.push.services.mozilla.com/wpush/v2/abc",
      "https://web.push.apple.com/abc",
      "https://wns2-par02p.notify.windows.com/w/?token=abc",
    ]) {
      expect((await put(h, dev.token, ok)).status, ok).toBe(200);
    }
    for (const bad of [
      "http://fcm.googleapis.com/fcm/send/abc",
      "https://fcm.googleapis.com:8443/fcm/send/abc",
      "https://user@fcm.googleapis.com/fcm/send/abc",
      "https://evil.example/fcm/send/abc",
      "https://fcm.googleapis.com.evil.example/x",
      "https://169.254.169.254/latest/meta-data",
      "https://localhost/x",
      "not-a-url",
    ]) {
      expect((await put(h, dev.token, bad)).status, bad).toBe(400);
    }
    // A push frame without VAPID keys configured is dropped silently.
    send(h.host.ws, {
      t: "push",
      device_hash: dev.hash,
      b64: b64(new TextEncoder().encode("ciphertext")),
      ttl: 3600,
      urgency: "high",
    });
    await sleep(200);
    h.host.ws.close();
  });
});

describe("rate limits", () => {
  test("register throttles one address", async () => {
    const addr = ip();
    const keys = await makeHostKeys();
    const body = JSON.stringify({ relay_pub: keys.pub });
    let last = 0;
    for (let i = 0; i < 11; i++) {
      const res = await api(
        "/r/v1/register",
        { method: "POST", headers: { "content-type": "application/json" }, body },
        addr,
      );
      last = res.status;
    }
    expect(last).toBe(429);
    const other = await api(
      "/r/v1/register",
      { method: "POST", headers: { "content-type": "application/json" }, body },
      ip(),
    );
    expect(other.status).toBe(200);
  });
});
