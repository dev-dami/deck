import { type ChildProcess, spawn } from "node:child_process";
import { accessSync, constants, statSync } from "node:fs";
import { delimiter, join } from "node:path";
import { createInterface } from "node:readline";
import { normalizeFingerprint, type Target } from "./pin";

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

// The first line of `deck remote pair --json`.
export interface PairOffer {
  target: Target;
  code: string;
}

export function parsePairOffer(line: string): PairOffer | null {
  let v: unknown;
  try {
    v = JSON.parse(line);
  } catch {
    return null;
  }
  if (typeof v !== "object" || v === null) return null;
  const o = v as Record<string, unknown>;
  if (typeof o["url"] !== "string" || typeof o["code"] !== "string") return null;
  if (typeof o["port"] !== "number" || typeof o["tls"] !== "boolean") return null;
  let host: string;
  try {
    host = new URL(o["url"]).hostname.replace(/^\[|\]$/g, "");
  } catch {
    return null;
  }
  let fingerprint: string | null = null;
  if (o["tls"]) {
    fingerprint = typeof o["fingerprint"] === "string" ? normalizeFingerprint(o["fingerprint"]) : null;
    if (fingerprint === null) return null;
  }
  return { target: { host, port: o["port"], tls: o["tls"], fingerprint }, code: o["code"] };
}

const START_TIMEOUT_MS = 30_000;

// Waits until `deck remote` has shown its first pairing code, which it does
// last when it starts: from then on a code from `deck remote pair` stays
// armed. Resolves null when ready, else the reason (the end of stderr).
function serverReady(server: ChildProcess): Promise<string | null> {
  return new Promise((resolve) => {
    let out = "";
    let err = "";
    const fail = () => resolve(err.trim().split("\n").slice(-2).join(" "));
    const timer = setTimeout(() => {
      server.kill("SIGTERM");
      fail();
    }, START_TIMEOUT_MS);
    server.stdout?.on("data", (c: Buffer) => {
      // The output is read for as long as the server runs, so the pipe
      // never fills.
      out = (out + c.toString("utf8")).slice(-4000);
      if (out.includes("#pair=")) {
        clearTimeout(timer);
        resolve(null);
      }
    });
    server.stderr?.on("data", (c: Buffer) => {
      err = (err + c.toString("utf8")).slice(-2000);
    });
    server.on("error", (e) => {
      clearTimeout(timer);
      err = e.message;
      fail();
    });
    server.on("exit", () => {
      clearTimeout(timer);
      fail();
    });
  });
}

// Starts `deck remote` in a folder and pairs with it through
// `deck remote pair --json`. The servers this app started stop with it.
export class LocalServers {
  private readonly children = new Set<ChildProcess>();

  constructor(private readonly deck: string) {}

  // Starts the server and waits for its first pairing code. The pair
  // command stays alive until the code is used or expires, so pair(offer)
  // must run before the caller lets go of the returned stop function.
  async start(dir: string): Promise<{ offer: PairOffer; done: () => void } | { error: string }> {
    try {
      if (!statSync(dir).isDirectory()) return { error: "That is not a folder." };
    } catch {
      return { error: "That folder does not exist." };
    }
    // A Deck that already serves this folder answers at once. A second
    // server would arm a code of its own and void the one we pair with.
    const running = await this.pairCommand(dir);
    if ("offer" in running) return running;

    const server = spawn(this.deck, ["remote", "--port", "0"], {
      cwd: dir,
      stdio: ["pipe", "pipe", "pipe"],
      windowsHide: true,
    });
    this.children.add(server);
    server.on("exit", () => this.children.delete(server));
    const started = await serverReady(server);
    if (started !== null) return { error: `Deck did not start${started === "" ? "." : `: ${started}`}` };
    return this.pairCommand(dir);
  }

  stopAll(): void {
    for (const c of this.children) c.kill("SIGTERM");
    this.children.clear();
  }

  private pairCommand(dir: string): Promise<{ offer: PairOffer; done: () => void } | { error: string }> {
    return new Promise((resolve) => {
      const p = spawn(this.deck, ["remote", "pair", "--json"], {
        cwd: dir,
        stdio: ["ignore", "pipe", "pipe"],
        windowsHide: true,
      });
      let stderr = "";
      let settled = false;
      const settle = (v: { offer: PairOffer; done: () => void } | { error: string }) => {
        if (settled) return;
        settled = true;
        resolve(v);
      };
      p.stderr?.on("data", (c: Buffer) => {
        stderr = (stderr + c.toString("utf8")).slice(-2000);
      });
      const lines = createInterface({ input: p.stdout });
      lines.once("line", (line) => {
        const offer = parsePairOffer(line);
        if (offer === null) {
          p.kill("SIGTERM");
          settle({ error: "deck remote pair printed something this app cannot read." });
          return;
        }
        // The command exits by itself once the code is used; done() ends it
        // early when pairing failed, which also disarms the code.
        settle({ offer, done: () => p.exitCode === null && p.kill("SIGTERM") });
      });
      p.on("error", (e) => settle({ error: e.message }));
      p.on("exit", () => settle({ error: stderr.trim() }));
    });
  }
}
