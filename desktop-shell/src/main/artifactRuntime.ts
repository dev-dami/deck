import { join } from "node:path";
import { createHash } from "node:crypto";
import { utilityProcess, type UtilityProcess } from "electron";
import { ARTIFACT_LIMITS, type ArtifactFile } from "../shared/artifacts";
import type { BuildResult } from "./artifactBuild";

// Keep a few immutable source snapshots hot for Preview/Source toggles and
// revision restores. Content hashes cannot mix files from different revisions.
const bundles = new Map<string, { result: Extract<BuildResult, { ok: true }>; bytes: number }>();
let bundleBytes = 0;

// The browser WASM compiler runs in Electron's disposable Node utility
// process. Its address space and startup state are independent of main, so
// cancelling a cold build cannot strand another compiler's module loading.
// No external Node executable, network fetch or artifact evaluation is needed.
export function buildArtifactReact(entrypoint: string, files: readonly ArtifactFile[], signal?: AbortSignal): Promise<BuildResult> {
  return new Promise((resolve) => {
    const start = Date.now();
    const trace = (stage: string) => { if (process.env["DECK_ARTIFACT_TRACE"] === "1") console.info("ARTIFACT_TRACE", stage, Date.now() - start); };
    const fail = (message: string): BuildResult => ({ ok: false, diagnostics: [{ file: "", line: 0, column: 0, message }] });
    if (signal?.aborted) { resolve(fail("Build cancelled.")); return; }
    const key = createHash("sha256").update(JSON.stringify([entrypoint, files])).digest("hex");
    const cached = bundles.get(key);
    if (cached !== undefined) {
      bundles.delete(key); bundles.set(key, cached);
      trace("bundle-cache-hit");
      resolve(cached.result);
      return;
    }
    let done = false;
    let worker: UtilityProcess;
    let stderr = "";
    try {
      worker = utilityProcess.fork(join(__dirname, "artifactWorker.js"), [], {
        // Main's preloads/inspector loaders do not belong in the compiler.
        execArgv: ["--max-old-space-size=256"],
        stdio: "pipe",
        serviceName: "Deck Artifact Compiler",
      });
    } catch (error) { resolve(fail(error instanceof Error ? error.message : "Could not start the compiler.")); return; }
    trace("worker-created");
    const finish = (result: BuildResult) => {
      if (done) return;
      done = true;
      trace(result.ok ? "build-complete" : "build-failed");
      if (result.ok) {
        const bytes = Buffer.byteLength(result.js, "utf8") + Buffer.byteLength(result.css, "utf8");
        const previous = bundles.get(key);
        if (previous !== undefined) bundleBytes -= previous.bytes;
        bundles.delete(key); bundles.set(key, { result, bytes }); bundleBytes += bytes;
        while (bundles.size > 4 || bundleBytes > 16 << 20) {
          const oldest = bundles.keys().next().value;
          if (oldest === undefined) break;
          bundleBytes -= bundles.get(oldest)!.bytes; bundles.delete(oldest);
        }
      }
      clearTimeout(timer);
      signal?.removeEventListener("abort", abort);
      if (worker.pid !== undefined) worker.kill();
      resolve(result);
    };
    const abort = () => finish(fail("Build cancelled."));
    const timer = setTimeout(() => finish(fail(`The build exceeded ${ARTIFACT_LIMITS.buildMs / 1000} seconds and was stopped.`)), ARTIFACT_LIMITS.buildMs);
    worker.once("spawn", () => {
      trace("worker-spawned");
      if (done) { worker.kill(); return; }
      try { worker.postMessage({ entrypoint, files }); }
      catch (error) { finish(fail(error instanceof Error ? error.message : "Could not send files to the compiler.")); }
    });
    worker.stderr?.on("data", (data: Buffer) => { stderr = (stderr + data.toString("utf8")).slice(-2048); });
    worker.stdout?.on("data", (data: Buffer) => { if (process.env["DECK_ARTIFACT_TRACE"] === "1") console.info(data.toString("utf8").trim()); });
    worker.once("message", (result: BuildResult) => finish(result));
    worker.once("error", (type, location) => finish(fail(`The compiler failed: ${type} at ${location}.`)));
    worker.once("exit", (code) => { if (!done) finish(fail(`The compiler stopped before producing a bundle (${code}). ${stderr}`.trim())); });
    signal?.addEventListener("abort", abort, { once: true });
    if (signal?.aborted) abort();
  });
}
