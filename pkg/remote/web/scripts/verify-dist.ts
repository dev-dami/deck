// Rebuilds into a temporary directory and compares it with the committed
// dist/, so CI fails on a stale build. Exit 0 when identical.
import { mkdtempSync, readdirSync, readFileSync, rmSync, statSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { build } from "../build";

const committed = resolve(import.meta.dir, "..", "dist");

function listFiles(dir: string, prefix = ""): string[] {
  const out: string[] = [];
  for (const name of readdirSync(dir).sort()) {
    if (name === ".gitkeep") continue;
    const rel = prefix === "" ? name : `${prefix}/${name}`;
    const full = join(dir, name);
    if (statSync(full).isDirectory()) out.push(...listFiles(full, rel));
    else out.push(rel);
  }
  return out;
}

export function compareDirs(a: string, b: string): string[] {
  const problems: string[] = [];
  const filesA = listFiles(a);
  const filesB = new Set(listFiles(b));
  for (const f of filesA) {
    if (!filesB.has(f)) {
      problems.push(`only in committed dist: ${f}`);
      continue;
    }
    filesB.delete(f);
    const x = readFileSync(join(a, f));
    const y = readFileSync(join(b, f));
    if (!x.equals(y)) problems.push(`differs: ${f}`);
  }
  for (const f of filesB) problems.push(`missing from committed dist: ${f}`);
  return problems;
}

async function main(): Promise<void> {
  const fresh = mkdtempSync(join(tmpdir(), "deck-remote-dist-"));
  try {
    await build(fresh, true);
    const problems = compareDirs(committed, fresh);
    if (problems.length > 0) {
      for (const p of problems) console.error(p);
      console.error("dist/ is stale: run `bun run build` and commit the result");
      process.exit(1);
    }
    console.log(`dist/ matches a fresh build (${listFiles(committed).length} files)`);
  } finally {
    rmSync(fresh, { recursive: true, force: true });
  }
}

if (import.meta.main) await main();
