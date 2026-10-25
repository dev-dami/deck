import { isAbsolute, resolve } from "node:path";
import type { LinkEvent } from "../shared/bridge";

// Longer than any invite line deck remote prints.
const MAX_URL = 2048;

// What a deck:// link asks for: deck://pair?… goes to the pairing form,
// deck://open?path=<folder> opens a folder that exists. Anything else is
// ignored.
export function linkFromUrl(raw: string, isDir: (p: string) => boolean): LinkEvent | null {
  if (raw.length > MAX_URL) return null;
  let u: URL;
  try {
    u = new URL(raw);
  } catch {
    return null;
  }
  if (u.protocol !== "deck:") return null;
  if (u.hostname === "pair") return { type: "pair", url: raw };
  if (u.hostname === "open") {
    const p = u.searchParams.get("path");
    if (p !== null && isAbsolute(p) && isDir(p)) return { type: "open", path: p };
  }
  return null;
}

// The links and folders on a command line, without the executable and the
// app path: `deck-desktop ~/src/app` opens that folder, a deck:// argument
// (how Windows and Linux hand over a link) is read as a link. Switches are
// skipped.
export function linksFromArgs(args: string[], cwd: string, isDir: (p: string) => boolean): LinkEvent[] {
  const out: LinkEvent[] = [];
  for (const a of args) {
    if (a.startsWith("-") || a === "") continue;
    if (/^deck:/i.test(a)) {
      const l = linkFromUrl(a, isDir);
      if (l !== null) out.push(l);
      continue;
    }
    const p = resolve(cwd, a);
    if (isDir(p)) out.push({ type: "open", path: p });
  }
  return out;
}
