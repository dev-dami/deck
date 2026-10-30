import { readFileSync } from "node:fs";
import { createRequire } from "node:module";
import type * as Esbuild from "esbuild-wasm";
import type { ArtifactFile } from "../shared/artifacts";
import { buildReact } from "./artifactBuild";

// esbuild's browser API uses a precompiled local module without spawning
// its Node CLI. Its inline WASM shim expects the browser's `self`; the alias
// exists only in this disposable utility process, never in main or the page.
Object.defineProperty(globalThis, "self", { value: globalThis, configurable: true });
const req = createRequire(__filename);
const esbuild = req("esbuild-wasm/lib/browser.js") as typeof Esbuild;
const start = Date.now();
const trace = (stage: string) => { if (process.env["DECK_ARTIFACT_TRACE"] === "1") console.info("ARTIFACT_TRACE", stage, Date.now() - start); };

async function compile(input: { entrypoint: string; files: ArtifactFile[] }) {
  trace("wasm-start");
  // Node supplies WebAssembly at runtime; the main-process TS target omits DOM globals.
  const wasm = (globalThis as unknown as { WebAssembly: { compile(bytes: Uint8Array): Promise<WebAssembly.Module> } }).WebAssembly;
  const wasmModule = await wasm.compile(readFileSync(req.resolve("esbuild-wasm/esbuild.wasm")));
  trace("wasm-compiled");
  await esbuild.initialize({ wasmModule, worker: false });
  trace("wasm-initialized");
  const result = await buildReact(input.entrypoint, input.files, { esbuild, vendor: (spec) => req.resolve(spec) });
  process.parentPort.postMessage(result);
}

process.parentPort.once("message", (event) => {
  void compile(event.data as { entrypoint: string; files: ArtifactFile[] }).catch((error: unknown) => process.parentPort.postMessage({
    ok: false,
    diagnostics: [{ file: "", line: 0, column: 0, message: error instanceof Error ? error.message : "Could not initialize the compiler." }],
  }));
});
