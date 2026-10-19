import http from "node:http";
import type net from "node:net";
import { authority, connectPinned, originOf, PinError, type Target } from "./pin";

export interface Paired {
  token: string;
  deviceId: string;
  serverId: string;
  expiresAt: string;
}

export type PairResult =
  | { ok: true; paired: Paired }
  | { ok: false; message: string; retryAfterMs: number | null };

export type Connect = (t: Target) => Promise<net.Socket>;

const MAX_BODY = 8 * 1024;
const MAX_CODE_LEN = 64;

const ERRORS: Record<string, string> = {
  unauthorized: "The code is wrong or has expired. Run 'deck remote pair' for a new one.",
  invalid: "The code is wrong or has expired. Run 'deck remote pair' for a new one.",
  forbidden: "Deck refused this app. Check the address.",
  rate_limited: "Too many tries. Wait a minute and try again.",
  internal: "Deck hit an error while it paired. Try again.",
};

export function codeError(code: string): string | null {
  const c = code.trim();
  if (c === "" || c.length > MAX_CODE_LEN || /[\p{Cc}\p{Cf}]/u.test(c)) {
    return "The pairing code is not valid.";
  }
  return null;
}

function failure(message: string, retryAfterMs: number | null = null): PairResult {
  return { ok: false, message, retryAfterMs };
}

function str(v: unknown): string | null {
  return typeof v === "string" && v !== "" ? v : null;
}

// Decodes the pair route's 200 body (§3.5): {token, device_id, server_id,
// expires_at}.
export function decodePaired(body: unknown): Paired | null {
  if (typeof body !== "object" || body === null) return null;
  const b = body as Record<string, unknown>;
  const token = str(b["token"]);
  const deviceId = str(b["device_id"]);
  const serverId = str(b["server_id"]);
  const expiresAt = str(b["expires_at"]);
  if (token === null || deviceId === null || serverId === null || expiresAt === null) return null;
  if (Number.isNaN(Date.parse(expiresAt))) return null;
  return { token, deviceId, serverId, expiresAt };
}

function connectError(e: unknown, t: Target): string {
  if (e instanceof PinError) {
    return "The server's certificate does not match the fingerprint. Do not pair unless you expected a new certificate.";
  }
  const code = (e as { code?: unknown }).code;
  if (code === "ECONNREFUSED") return `Nothing answers at ${authority(t)}. Is deck remote running?`;
  return `Could not reach Deck at ${authority(t)}.`;
}

// POST /api/v1/pair with the code (§3.5). The request goes out only on a
// connection whose certificate matched the pin, with the Origin and content
// type Deck requires (R7).
export async function pairWith(
  t: Target,
  code: string,
  deviceName: string,
  connect: Connect = connectPinned,
): Promise<PairResult> {
  let sock: net.Socket;
  try {
    sock = await connect(t);
  } catch (e) {
    return failure(connectError(e, t));
  }
  const body = JSON.stringify({ code: code.trim(), device_name: deviceName });
  return new Promise<PairResult>((resolve) => {
    const req = http.request(
      {
        createConnection: () => sock,
        method: "POST",
        path: "/api/v1/pair",
        headers: {
          host: authority(t),
          origin: originOf(t),
          "content-type": "application/json",
          "content-length": Buffer.byteLength(body),
        },
        timeout: 15_000,
      },
      (res) => {
        const chunks: Buffer[] = [];
        let size = 0;
        res.on("data", (c: Buffer) => {
          size += c.length;
          if (size > MAX_BODY) {
            req.destroy();
            return;
          }
          chunks.push(c);
        });
        res.on("end", () => {
          let parsed: unknown = null;
          try {
            parsed = JSON.parse(Buffer.concat(chunks).toString("utf8"));
          } catch {
            parsed = null;
          }
          const status = res.statusCode ?? 0;
          if (status === 200) {
            const paired = decodePaired(parsed);
            resolve(paired === null ? failure("Deck sent an unexpected answer.") : { ok: true, paired });
            return;
          }
          const err =
            typeof parsed === "object" && parsed !== null
              ? (parsed as Record<string, unknown>)["error"]
              : undefined;
          const retry = Number.parseInt(String(res.headers["retry-after"] ?? ""), 10);
          resolve(
            failure(
              (typeof err === "string" ? ERRORS[err] : undefined) ?? `Pairing failed (HTTP ${status}).`,
              status === 429 && Number.isFinite(retry) ? retry * 1000 : null,
            ),
          );
        });
      },
    );
    req.on("timeout", () => req.destroy(new Error("timeout")));
    req.on("error", () => resolve(failure(`Deck at ${authority(t)} did not answer.`)));
    req.end(body);
  });
}
