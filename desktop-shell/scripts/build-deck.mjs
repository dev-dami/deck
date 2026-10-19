// Builds the Deck engine the app bundles (bin/deck, bin/deck.exe on
// Windows) from this repository, as Claude Desktop ships Claude Code.
// Needs Go. GOOS and GOARCH pick another platform for a cross build.
import { execFileSync } from "node:child_process";
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const shell = join(here, "..");
const repo = join(shell, "..");
const goos = process.env.GOOS ?? { win32: "windows", darwin: "darwin" }[process.platform] ?? "linux";
const out = join(shell, "bin", goos === "windows" ? "deck.exe" : "deck");
mkdirSync(dirname(out), { recursive: true });
execFileSync("go", ["build", "-trimpath", "-ldflags", "-s -w", "-o", out, "."], {
  cwd: repo,
  stdio: "inherit",
  env: { ...process.env, CGO_ENABLED: process.env.CGO_ENABLED ?? "0" },
});
console.log(`built ${out}`);
