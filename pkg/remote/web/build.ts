// Deterministic production build: same sources in, byte-identical dist out.
// Output lands in pkg/remote/web/dist, which pkg/remote/server embeds and
// which is committed so `go install` needs no JavaScript toolchain.
// scripts/verify-dist.ts rebuilds into a temp dir (--out) and diffs.
import { createHash } from "node:crypto";
import { mkdirSync, readdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { renderIcon } from "./scripts/icons";

const root = import.meta.dir;

// style.css's canvas, the desktop's, dark and light. A theme of the
// person's sets theme-color from its palette once the welcome arrives (R6);
// theme.ts puts these back (data-default) for Deck's own look.
const NEUTRAL_DARK = "#19191b";
const NEUTRAL_LIGHT = "#fbfbfd";

function fail(msg: string): never {
  console.error(`build failed: ${msg}`);
  process.exit(1);
}

export function buildTarget(argv: readonly string[]): "v2" | "hosted" {
  const i = argv.indexOf("--target");
  const given = i >= 0 ? argv[i + 1] : undefined;
  if (given === undefined) return "v2";
  if (given === "hosted" || given === "v2") return given;
  fail(`unknown --target ${given ?? "(missing)"}, want v2 or hosted`);
}

export function outDir(argv: readonly string[]): string {
  const i = argv.indexOf("--out");
  const given = i >= 0 ? argv[i + 1] : undefined;
  if (given !== undefined) return resolve(given);
  return resolve(root, buildTarget(argv) === "hosted" ? "dist-hosted" : "dist");
}

function clean(dir: string): void {
  mkdirSync(dir, { recursive: true });
  for (const name of readdirSync(dir)) {
    // .gitkeep keeps the directory present in a fresh checkout.
    if (name === ".gitkeep") continue;
    rmSync(join(dir, name), { recursive: true, force: true });
  }
}

async function bundle(dist: string, target: "v2" | "hosted"): Promise<{ js: string; css: string }> {
  const entry =
    target === "hosted" ? join(root, "src", "hosted", "main.tsx") : join(root, "src", "main.tsx");
  const result = await Bun.build({
    entrypoints: [entry, join(root, "src", "style.css")],
    outdir: dist,
    target: "browser",
    format: "esm",
    minify: true,
    sourcemap: "none",
    splitting: false,
    naming: {
      entry: "[name]-[hash].[ext]",
      chunk: "[name]-[hash].[ext]",
      asset: "[name]-[hash].[ext]",
    },
    define: { "process.env.NODE_ENV": JSON.stringify("production") },
  });
  if (!result.success) {
    for (const log of result.logs) console.error(log);
    fail("bundling the app");
  }
  const names = result.outputs.map((o) => o.path.slice(dist.length + 1).replaceAll("\\", "/"));
  const js = names.find((n) => n.endsWith(".js"));
  const css = names.find((n) => n.endsWith(".css"));
  if (js === undefined || css === undefined)
    fail(`expected a .js and .css output, got ${names.join(", ")}`);
  return { js, css };
}

// The meta CSP mirrors the server's (§3.6) minus frame-ancestors, which a
// meta tag cannot carry.
const CSP =
  "default-src 'self'; script-src 'self'; style-src 'self'; img-src 'self' data: blob:; " +
  "connect-src 'self'; base-uri 'none'; form-action 'none'; object-src 'none'";

function indexHtml(js: string, css: string, target: "v2" | "hosted"): string {
  const csp =
    target === "hosted" ? CSP.replace("connect-src 'self'", "connect-src 'self' https: wss:") : CSP;
  return `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1, viewport-fit=cover">
<meta http-equiv="Content-Security-Policy" content="${csp}">
<meta name="referrer" content="no-referrer">
<meta name="theme-color" content="${NEUTRAL_LIGHT}" data-default="${NEUTRAL_LIGHT}" media="(prefers-color-scheme: light)">
<meta name="theme-color" content="${NEUTRAL_DARK}" data-default="${NEUTRAL_DARK}" media="(prefers-color-scheme: dark)">
<meta name="mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-capable" content="yes">
<meta name="apple-mobile-web-app-title" content="Deck">
<title>Deck</title>
<link rel="manifest" href="./manifest.webmanifest">
<link rel="icon" href="./icon.svg" type="image/svg+xml">
<link rel="apple-touch-icon" href="./apple-touch-icon.png">
<link rel="stylesheet" href="./${css}">
</head>
<body>
<div id="app"></div>
<noscript>Deck Remote needs JavaScript.</noscript>
<script type="module" src="./${js}"></script>
</body>
</html>
`;
}

const manifest = {
  name: "Deck Remote",
  short_name: "Deck",
  description: "Control your Deck agents from your phone",
  start_url: "./",
  scope: "./",
  display: "standalone",
  background_color: NEUTRAL_DARK,
  theme_color: NEUTRAL_DARK,
  icons: [
    { src: "./icon-192.png", sizes: "192x192", type: "image/png", purpose: "any" },
    { src: "./icon-512.png", sizes: "512x512", type: "image/png", purpose: "any" },
    { src: "./icon-maskable-512.png", sizes: "512x512", type: "image/png", purpose: "maskable" },
    { src: "./icon.svg", sizes: "any", type: "image/svg+xml", purpose: "any" },
  ],
};

async function buildWorker(
  dist: string,
  shell: string[],
  cacheId: string,
  hosted: boolean,
): Promise<void> {
  const result = await Bun.build({
    entrypoints: [join(root, "src", "sw.ts")],
    outdir: dist,
    target: "browser",
    format: "esm",
    minify: true,
    sourcemap: "none",
    naming: "sw.js",
    define: {
      __SHELL__: JSON.stringify(shell),
      __CACHE__: JSON.stringify(cacheId),
      __HOSTED__: JSON.stringify(hosted),
    },
  });
  if (!result.success) {
    for (const log of result.logs) console.error(log);
    fail("bundling the service worker");
  }
}

export async function build(
  dist: string,
  quiet = false,
  target: "v2" | "hosted" = "v2",
): Promise<void> {
  clean(dist);
  const write = (name: string, data: string | Uint8Array) => writeFileSync(join(dist, name), data);
  const { js, css } = await bundle(dist, target);

  write("index.html", indexHtml(js, css, target));
  write("manifest.webmanifest", `${JSON.stringify(manifest, null, 2)}\n`);
  write("icon.svg", readFileSync(join(root, "public", "icon.svg")));
  write("icon-192.png", renderIcon(192));
  write("icon-512.png", renderIcon(512));
  write("icon-maskable-512.png", renderIcon(512, true));
  write("apple-touch-icon.png", renderIcon(180));

  // The shell is everything needed to boot offline. Content-hashed names change
  // when code changes; the rest are hashed here so the cache name tracks them.
  const shell = [
    "./",
    "./index.html",
    `./${js}`,
    `./${css}`,
    "./manifest.webmanifest",
    "./icon.svg",
    "./icon-192.png",
    "./apple-touch-icon.png",
  ];
  const h = createHash("sha256");
  for (const entry of [...shell].sort()) {
    const file = entry === "./" ? "index.html" : entry.slice(2);
    h.update(entry);
    h.update(readFileSync(join(dist, file)));
  }
  await buildWorker(dist, shell, h.digest("hex").slice(0, 12), target === "hosted");

  if (quiet) return;
  const files = readdirSync(dist)
    .filter((n) => n !== ".gitkeep")
    .sort();
  for (const f of files) {
    const size = readFileSync(join(dist, f)).length;
    console.log(`${f.padEnd(36)} ${String(size).padStart(8)} B`);
  }
}

if (import.meta.main)
  await build(outDir(process.argv.slice(2)), false, buildTarget(process.argv.slice(2)));
