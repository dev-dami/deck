import { readFileSync } from "node:fs";
import { dirname, posix, resolve as resolvePath } from "node:path";
import type { BuildContext, Loader, Message, Plugin } from "esbuild-wasm";
import { type ArtifactDiagnostic, type ArtifactFile, ARTIFACT_LIMITS, limitDiagnostics, utf8Bytes } from "../shared/artifacts";

// Builds a React artifact's files into one script and one stylesheet. The
// code is only transformed here, never run: esbuild reads the revision's
// own files and a short list of packaged modules, and nothing else.

// The only bare imports an artifact may make.
export const REACT_ALLOW: readonly string[] = ["react", "react-dom", "react-dom/client", "react/jsx-runtime"];

const SOURCE_EXTENSIONS = [".tsx", ".ts", ".jsx", ".js"] as const;
const VENDOR_EXTENSIONS = [".js", ".cjs", ".json"] as const;
const ART = "artifact";
const VENDOR = "vendor";
const ENTRY = "deck-entry";

const LOADERS: Record<string, Loader> = {
  ".tsx": "tsx",
  ".ts": "ts",
  ".jsx": "jsx",
  ".js": "js",
  ".css": "css",
  ".json": "json",
  ".svg": "dataurl",
};

export type BuildResult =
  | { ok: true; js: string; css: string }
  | { ok: false; diagnostics: ArtifactDiagnostic[] };

export interface BuildDeps {
  esbuild: Pick<typeof import("esbuild-wasm"), "context">;
  // Resolves an allowlisted package name to a file on disk.
  vendor: (spec: string) => string;
  readVendor?: (file: string) => string;
}

function fail(...messages: string[]): BuildResult {
  return { ok: false, diagnostics: limitDiagnostics(messages.map((m) => ({ file: "", line: 0, column: 0, message: m }))) };
}

// esbuild counts columns from 0 and names a file by namespace and path: an
// artifact file keeps its path, and anything else (Deck's mount file, the
// packaged React) is named for what it is, since the engine accepts only
// paths of the revision.
function toDiagnostics(errors: readonly Message[]): ArtifactDiagnostic[] {
  return limitDiagnostics(
    errors.map((e) => {
      const file = e.location?.file ?? "";
      return {
        file: file.startsWith(`${ART}:`) ? file.slice(ART.length + 1) : file.startsWith(`${ENTRY}:`) ? "<generated>" : file === "" ? "" : "<build>",
        line: e.location?.line ?? 0,
        column: e.location === null || e.location === undefined ? 0 : e.location.column + 1,
        message: e.text,
      };
    }),
  );
}

function importError(spec: string): string {
  return `Cannot import "${spec}". Artifacts can import their own files and: ${REACT_ALLOW.join(", ")}.`;
}

// The mounting file Deck supplies: the entry's default export is the app.
function entrySource(entrypoint: string): string {
  return [
    `import { jsx } from "react/jsx-runtime";`,
    `import { createRoot } from "react-dom/client";`,
    `import App from ${JSON.stringify(`./${entrypoint}`)};`,
    `createRoot(document.getElementById("root")).render(jsx(App, {}));`,
  ].join("\n");
}

function plugin(files: ReadonlyMap<string, string>, entrypoint: string, d: BuildDeps): Plugin {
  const read = d.readVendor ?? ((f: string) => readFileSync(f, "utf8"));
  const pick = (candidate: string): string | null => {
    if (files.has(candidate)) return candidate;
    for (const ext of SOURCE_EXTENSIONS) if (files.has(candidate + ext)) return candidate + ext;
    return null;
  };
  return {
    name: "deck-artifact",
    setup(b) {
      b.onResolve({ filter: /.*/ }, (args) => {
        const spec = args.path;
        if (args.namespace === ENTRY && spec === `./${entrypoint}`) return { path: entrypoint, namespace: ART };
        if (args.namespace === ENTRY || args.namespace === ART) {
          if (spec.startsWith("./") || spec.startsWith("../")) {
            if (args.namespace === ENTRY) return { errors: [{ text: importError(spec) }] };
            const found = pick(posix.normalize(posix.join(posix.dirname(args.importer), spec)));
            return found === null
              ? { errors: [{ text: `Cannot find "${spec}" among the artifact's files.` }] }
              : { path: found, namespace: ART };
          }
          if (REACT_ALLOW.includes(spec)) return { path: d.vendor(spec), namespace: VENDOR };
          return { errors: [{ text: importError(spec) }] };
        }
        // Inside a packaged module: its own relative files and the packages
        // the allowlisted ones depend on, found next to them.
        const from = dirname(args.importer);
        const target = spec.startsWith(".") ? resolvePath(from, spec) : d.vendor(spec);
        for (const ext of ["", ...VENDOR_EXTENSIONS]) {
          try {
            read(target + ext);
            return { path: target + ext, namespace: VENDOR };
          } catch {
            // try the next extension
          }
        }
        return { errors: [{ text: `Cannot resolve "${spec}" in a packaged module.` }] };
      });
      b.onLoad({ filter: /.*/, namespace: ENTRY }, () => ({ contents: entrySource(entrypoint), loader: "js" }));
      b.onLoad({ filter: /.*/, namespace: ART }, (args) => {
        const dot = args.path.lastIndexOf(".");
        const loader = LOADERS[dot < 0 ? "" : args.path.slice(dot).toLowerCase()];
        const contents = files.get(args.path);
        if (loader === undefined || contents === undefined) {
          return { errors: [{ text: `"${args.path}" is not a file type an artifact can import.` }] };
        }
        return { contents, loader };
      });
      b.onLoad({ filter: /.*/, namespace: VENDOR }, (args) => ({
        contents: read(args.path),
        loader: args.path.endsWith(".json") ? "json" : "js",
      }));
    },
  };
}

export async function buildReact(
  entrypoint: string,
  input: readonly ArtifactFile[],
  d: BuildDeps,
  ms: number = ARTIFACT_LIMITS.buildMs,
): Promise<BuildResult> {
  const files = new Map(input.map((f) => [f.path, f.content]));
  if (!files.has(entrypoint)) return fail(`The entry point "${entrypoint}" is not among the artifact's files.`);

  // A build that runs long is cancelled in esbuild itself, not just left
  // behind: its work stops and the next build starts clean.
  let ctx: BuildContext | undefined;
  let late = false;
  const timer = setTimeout(() => {
    late = true;
    void ctx?.cancel().catch(() => {});
  }, ms);
  const tooLong = () => fail(`The build took longer than ${Math.max(1, Math.round(ms / 1000))} seconds and was stopped.`);
  try {
    ctx = await d.esbuild.context({
      entryPoints: [{ in: "entry", out: "app" }],
      bundle: true,
      write: false,
      outdir: "out",
      format: "iife",
      platform: "browser",
      target: "es2020",
      jsx: "automatic",
      logLevel: "silent",
      define: { "process.env.NODE_ENV": '"production"' },
      plugins: [
        {
          name: "deck-entry",
          setup(b) {
            b.onResolve({ filter: /^entry$/ }, () => ({ path: "entry", namespace: ENTRY }));
          },
        },
        plugin(files, entrypoint, d),
      ],
    });
    if (late) return tooLong();
    const out = await ctx.rebuild();
    if (late) return tooLong();
    let js = "";
    let css = "";
    for (const f of out.outputFiles ?? []) {
      if (f.path.endsWith(".js")) js = f.text;
      else if (f.path.endsWith(".css")) css = f.text;
    }
    if (utf8Bytes(js) + utf8Bytes(css) > ARTIFACT_LIMITS.buildOutputBytes) {
      return fail("The built artifact is larger than the 8 MiB limit.");
    }
    return { ok: true, js, css };
  } catch (e) {
    if (late) return tooLong();
    const errors = (e as { errors?: Message[] }).errors;
    if (errors !== undefined && errors.length > 0) return { ok: false, diagnostics: toDiagnostics(errors) };
    return fail(e instanceof Error ? e.message : "The build failed.");
  } finally {
    clearTimeout(timer);
    await ctx?.dispose().catch(() => {});
  }
}
