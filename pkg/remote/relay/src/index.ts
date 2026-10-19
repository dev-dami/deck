// Deck relay Worker: one origin for the hosted PWA and the relay/v1 API.
// Every computer route resolves to that computer's ComputerDO by cid; the
// Worker itself keeps no per-computer state. Rate limiting on /register is
// best-effort in Worker memory; per-device and per-address limits inside a
// computer live in its DO.
import { ComputerDO, type RelayEnv } from "./computer";
import { b64urlToBytes, cidFromPub, ED25519_RAW_BYTES, isCid } from "./relay";

export { ComputerDO };

function clientIp(req: Request): string {
  return (
    req.headers.get("CF-Connecting-IP") ??
    req.headers.get("x-forwarded-for")?.split(",")[0]?.trim() ??
    "unknown"
  );
}

const registerHits = new Map<string, { count: number; start: number }>();

function registerAllowed(ip: string): boolean {
  const now = Date.now();
  const hit = registerHits.get(ip);
  if (hit === undefined || now - hit.start > 60_000) {
    registerHits.set(ip, { count: 1, start: now });
    return true;
  }
  hit.count += 1;
  return hit.count <= 10;
}

export default {
  async fetch(req: Request, env: RelayEnv): Promise<Response> {
    const url = new URL(req.url);
    const path = url.pathname;

    if (path === "/r/v1/register" && req.method === "POST") {
      const ip = clientIp(req);
      if (!registerAllowed(ip)) {
        return Response.json(
          { error: "rate_limited" },
          { status: 429, headers: { "retry-after": "60" } },
        );
      }
      let body: unknown;
      try {
        body = await req.json();
      } catch {
        return Response.json({ error: "invalid" }, { status: 400 });
      }
      const pub =
        typeof body === "object" && body !== null
          ? (body as Record<string, unknown>)["relay_pub"]
          : null;
      if (typeof pub !== "string") return Response.json({ error: "invalid" }, { status: 400 });
      const raw = b64urlToBytes(pub);
      if (raw === null || raw.length !== ED25519_RAW_BYTES) {
        return Response.json({ error: "invalid" }, { status: 400 });
      }
      const cid = await cidFromPub(raw);
      const stub = env.COMPUTER.getByName(cid);
      return stub.fetch(
        new Request(new URL("/internal/register", url), {
          method: "POST",
          headers: { "content-type": "application/json", "x-client-ip": ip },
          body: JSON.stringify({ relay_pub: pub }),
        }),
      );
    }

    if (path === "/r/v1/vapid" && req.method === "GET") {
      // The public half of the relay's VAPID key: a phone subscribes to push
      // with it as applicationServerKey. It is not secret.
      if (env.VAPID_PUBLIC_KEY === undefined || env.VAPID_PUBLIC_KEY === "") {
        return Response.json({ error: "push_unavailable" }, { status: 404 });
      }
      return Response.json(
        { public_key: env.VAPID_PUBLIC_KEY },
        { headers: { "cache-control": "public, max-age=3600" } },
      );
    }

    const m = /^\/r\/v1\/c\/([A-Za-z0-9_-]+)\/(host|dev|pair|mail|snap|push|status)$/.exec(path);
    if (m?.[1] === undefined || m[2] === undefined) {
      if (path.startsWith("/r/")) return new Response("not found", { status: 404 });
      if (env.ASSETS !== undefined) return env.ASSETS.fetch(req);
      return new Response("no assets", { status: 404 });
    }
    const cid = m[1];
    const route = m[2];
    if (!isCid(cid)) return new Response("unknown computer", { status: 404 });

    const stub = env.COMPUTER.getByName(cid);
    const headers = new Headers(req.headers);
    headers.set("x-client-ip", clientIp(req));
    const inner = new URL(req.url);
    if (route === "host" || route === "dev" || route === "pair") {
      if (req.headers.get("upgrade")?.toLowerCase() !== "websocket") {
        return new Response("want websocket", { status: 426 });
      }
      inner.pathname = `/internal/${route}`;
      return stub.fetch(new Request(inner, { headers }));
    }
    const method = { mail: "POST", snap: "GET", push: "PUT", status: "GET" } as const;
    const want = method[route as keyof typeof method];
    if (req.method !== want) return new Response("method not allowed", { status: 405 });
    inner.pathname = `/${route}`;
    return stub.fetch(new Request(inner, { method: req.method, headers, body: req.body }));
  },
} satisfies ExportedHandler<RelayEnv>;
