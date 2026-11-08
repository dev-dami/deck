import { execFile } from "node:child_process";
import { readFileSync } from "node:fs";
import { Data, Effect } from "effect";
import type { PhoneOutcome, PhoneState } from "../shared/bridge";
import { runPromise } from "../shared/effect";

// The phone link is `deck mobile connect --json`: the engine starts the
// relay and daemon, shares a folder and mints the pairing code, and this
// file only runs it and reads its answer. Nothing here handles a key.

export interface PhoneService {
  status(): Promise<PhoneOutcome>;
  link(folder: string | null, relayDir: string | null): Promise<PhoneOutcome>;
  newCode(): Promise<PhoneOutcome>;
  revoke(deviceId: string): Promise<PhoneOutcome>;
  stop(): Promise<PhoneOutcome>;
}

export interface RunResult {
  stdout: string;
  stderr: string;
  // Null when the process could not start or was killed.
  code: number | null;
}

export type Run = (args: string[]) => Promise<RunResult>;

// Starting the relay waits up to 45 s for it, then the daemon up to 45 s more.
const RUN_TIMEOUT_MS = 120_000;

// Runs deck with args. execFile kills it at RUN_TIMEOUT_MS; so does
// interrupting the effect. It never fails: a run that could not start has
// code null and says why on stderr.
export function execRunEffect(deck: string, env: NodeJS.ProcessEnv, args: string[]): Effect.Effect<RunResult> {
  return Effect.async<RunResult>((resume) => {
    const child = execFile(deck, args, { env, timeout: RUN_TIMEOUT_MS, windowsHide: true, maxBuffer: 1 << 20 }, (err, stdout, stderr) => {
      const code = err === null ? 0 : typeof err.code === "number" ? err.code : null;
      resume(Effect.succeed({ stdout: String(stdout), stderr: String(stderr) || (code === null && err !== null ? err.message : ""), code }));
    });
    return Effect.sync(() => void child.kill());
  });
}

export function execRun(deck: string, env: NodeJS.ProcessEnv): Run {
  return (args) => runPromise(execRunEffect(deck, env, args));
}

// Why the phone link did not do what was asked, in the window's words.
export class PhoneFailed extends Data.TaggedError("PhoneFailed")<{ readonly message: string; readonly needsRelayDir: boolean }> {}

type Obj = Record<string, unknown>;
const obj = (v: unknown): Obj => (typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Obj) : {});
const str = (o: Obj, k: string): string => (typeof o[k] === "string" ? (o[k] as string) : "");
const optStr = (o: Obj, k: string): string | null => (typeof o[k] === "string" && o[k] !== "" ? (o[k] as string) : null);

// The one JSON object `deck mobile connect --json` prints, or null.
export function parsePhone(stdout: string): PhoneState | null {
  const line = stdout
    .split("\n")
    .map((l) => l.trim())
    .filter((l) => l.startsWith("{"))
    .pop();
  if (line === undefined) return null;
  let v: unknown;
  try {
    v = JSON.parse(line);
  } catch {
    return null;
  }
  const o = obj(v);
  if (typeof o["online"] !== "boolean") return null;
  const devices = Array.isArray(o["device_list"])
    ? o["device_list"].map(obj).flatMap((d) => (str(d, "id") === "" ? [] : [{ id: str(d, "id"), name: str(d, "name") }]))
    : [];
  const qr = Array.isArray(o["qr"]) && o["qr"].every((r) => typeof r === "string" && /^[01]+$/.test(r)) ? (o["qr"] as string[]) : null;
  return {
    name: str(o, "name"),
    relayUrl: str(o, "relay_url"),
    online: o["online"],
    projects: typeof o["projects"] === "number" ? o["projects"] : 0,
    devices,
    pairUrl: optStr(o, "pair_url"),
    qr: qr !== null && qr.length > 0 ? qr : null,
    expiresAt: optStr(o, "expires_at"),
    message: optStr(o, "message"),
  };
}

// Cobra prints "Error: <message>" on stderr; usage text may follow.
export function failureMessage(stderr: string, code: number | null): string {
  const m = /^Error: (.+)$/m.exec(stderr);
  if (m?.[1] !== undefined) return m[1].trim();
  const first = stderr.trim().split("\n")[0];
  if (first !== undefined && first !== "") return first.slice(0, 300);
  return code === null ? "Deck could not run the phone link." : `Deck exited with code ${code}.`;
}

export function needsRelayDir(message: string): boolean {
  return /relay runtime not found|--relay-dir/.test(message);
}

// The relay folder a build recorded beside the engine (scripts/build-deck.mjs),
// or null. An installed app has no repository to search for it.
export function readRelayHint(file: string): string | null {
  try {
    const dir = readFileSync(file, "utf8").trim();
    return dir === "" ? null : dir;
  } catch {
    return null;
  }
}

export class PhoneLink implements PhoneService {
  constructor(
    // Null when the engine is missing.
    private readonly run: Run | null,
    private readonly relayHint: string | null = null,
  ) {}

  private goEffect(args: string[]): Effect.Effect<PhoneState, PhoneFailed> {
    const run = this.run;
    return Effect.gen(function* () {
      if (run === null) return yield* new PhoneFailed({ message: "This install of Deck is missing its engine. Reinstall Deck.", needsRelayDir: false });
      const r = yield* Effect.promise(() => run(["mobile", "connect", "--json", ...args]));
      if (r.code !== 0) {
        const message = failureMessage(r.stderr, r.code);
        return yield* new PhoneFailed({ message, needsRelayDir: needsRelayDir(message) });
      }
      const phone = parsePhone(r.stdout);
      if (phone === null) return yield* new PhoneFailed({ message: "Deck answered in a form this app does not understand. Update Deck.", needsRelayDir: false });
      return phone;
    });
  }

  private go(args: string[]): Promise<PhoneOutcome> {
    return runPromise(
      Effect.match(this.goEffect(args), {
        onFailure: (e): PhoneOutcome => ({ ok: false, message: e.message, needsRelayDir: e.needsRelayDir }),
        onSuccess: (phone): PhoneOutcome => ({ ok: true, phone }),
      }),
    );
  }

  status(): Promise<PhoneOutcome> {
    return this.go(["--action", "status"]);
  }

  // "link" keeps the relay the person already set up and starts a local Wi-Fi
  // one only on a fresh install: the same as /link in the terminal.
  link(folder: string | null, relayDir: string | null): Promise<PhoneOutcome> {
    const relay = relayDir ?? this.relayHint;
    return this.go([
      "--action",
      "link",
      ...(folder === null ? [] : ["--project", folder]),
      ...(relay === null ? [] : ["--relay-dir", relay]),
    ]);
  }

  newCode(): Promise<PhoneOutcome> {
    return this.go(["--action", "pair"]);
  }

  revoke(deviceId: string): Promise<PhoneOutcome> {
    return this.go(["--action", "revoke", "--device", deviceId]);
  }

  stop(): Promise<PhoneOutcome> {
    return this.go(["--action", "stop"]);
  }
}
