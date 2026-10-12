import { readdirSync, readFileSync } from "node:fs";
import { join } from "node:path";

// Agent output is untrusted. Every path that turns a string into live markup
// is banned in app sources, so rendering stays text-only by construction.
const BANNED = [
  /\binnerHTML\b/,
  /\bouterHTML\b/,
  /\bdangerouslySetInnerHTML\b/,
  /\binsertAdjacentHTML\b/,
  /\bdocument\.write(ln)?\b/,
  /\bsrcdoc\b/,
  /\bcreateContextualFragment\b/,
  /\beval\s*\(/,
  /\bnew\s+Function\s*\(/,
];

export interface Violation {
  file: string;
  line: number;
  text: string;
}

function walk(dir: string, out: string[]): string[] {
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const p = join(dir, entry.name);
    if (entry.isDirectory()) walk(p, out);
    else if (/\.(ts|tsx|js|jsx)$/.test(entry.name)) out.push(p);
  }
  return out;
}

export function findViolations(srcDir: string): Violation[] {
  const found: Violation[] = [];
  for (const file of walk(srcDir, [])) {
    const lines = readFileSync(file, "utf8").split("\n");
    lines.forEach((text, i) => {
      if (BANNED.some((re) => re.test(text))) found.push({ file, line: i + 1, text: text.trim() });
    });
  }
  return found;
}
