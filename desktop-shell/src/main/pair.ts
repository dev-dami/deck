import http from "node:http";
import type net from "node:net";
import { Data, Deferred, Effect } from "effect";
import { listenNow, runPromise } from "../shared/effect";
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
const ANSWER_MS = 15_000;

// Why pairing did not work, in the words the window shows. retryAfterMs is
// the wait Deck asked for after too many tries.
export class PairFailed extends Data.TaggedError("PairFailed")<{ readonly message: string; readonly retryAfterMs: number | null }> {}

const failed = (message: string, retryAfterMs: number | null = null) => new PairFailed({ message, retryAfterMs });

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

// The pair route's answer: its status, Retry-After and the JSON body (null
// when it is not JSON, or longer than MAX_BODY).
interface Answer {
  status: number;
  retryAfter: string;
  body: unknown;
}

// Sends body on sock and reads the answer, for up to ANSWER_MS. The request
// is destroyed if the effect is interrupted (its timeout does).
function post(sock: net.Socket, t: Target, body: string): Effect.Effect<Answer, PairFailed> {
  const noAnswer = () => failed(`Deck at ${authority(t)} did not answer.`);
  return Effect.suspend(() => {
    let req: http.ClientRequest | undefined;
    const answered = listenNow<Answer, PairFailed>((done) => {
      const r = http.request(
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
        },
        (res) => {
          const chunks: Buffer[] = [];
          let size = 0;
          res.on("data", (c: Buffer) => {
            size += c.length;
            if (size > MAX_BODY) {
              r.destroy();
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
            done(Effect.succeed({ status: res.statusCode ?? 0, retryAfter: String(res.headers["retry-after"] ?? ""), body: parsed }));
          });
        },
      );
      r.on("error", () => done(Effect.fail(noAnswer())));
      r.end(body);
      req = r;
    });
    return Deferred.await(answered).pipe(
      Effect.onInterrupt(() => Effect.sync(() => req?.destroy())),
      Effect.timeoutFail({ duration: ANSWER_MS, onTimeout: noAnswer }),
    );
  });
}

// What an answer means: the pairing, or why not.
function decodeAnswer(a: Answer): Effect.Effect<Paired, PairFailed> {
  if (a.status === 200) {
    const paired = decodePaired(a.body);
    return paired === null ? Effect.fail(failed("Deck sent an unexpected answer.")) : Effect.succeed(paired);
  }
  const err = typeof a.body === "object" && a.body !== null ? (a.body as Record<string, unknown>)["error"] : undefined;
  const retry = Number.parseInt(a.retryAfter, 10);
  return Effect.fail(
    failed(
      (typeof err === "string" ? ERRORS[err] : undefined) ?? `Pairing failed (HTTP ${a.status}).`,
      a.status === 429 && Number.isFinite(retry) ? retry * 1000 : null,
    ),
  );
}

// POST /api/v1/pair with the code (§3.5). The request goes out only on a
// connection whose certificate matched the pin, with the Origin and content
// type Deck requires (R7).
export function pairEffect(t: Target, code: string, deviceName: string, connect: Connect = connectPinned): Effect.Effect<Paired, PairFailed> {
  return Effect.gen(function* () {
    const sock = yield* Effect.tryPromise({ try: () => connect(t), catch: (e) => failed(connectError(e, t)) });
    const answer = yield* post(sock, t, JSON.stringify({ code: code.trim(), device_name: deviceName }));
    return yield* decodeAnswer(answer);
  });
}

export function pairWith(t: Target, code: string, deviceName: string, connect: Connect = connectPinned): Promise<PairResult> {
  return runPromise(
    Effect.match(pairEffect(t, code, deviceName, connect), {
      onFailure: (e): PairResult => failure(e.message, e.retryAfterMs),
      onSuccess: (paired): PairResult => ({ ok: true, paired }),
    }),
  );
}
