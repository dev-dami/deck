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

// A link event and how it reached the app: through a deck:// link, or as a
// folder typed on the command line.
export interface Arrival {
  link: LinkEvent;
  fromLink: boolean;
}

// Any web page can open a deck:// link, so the person confirms a folder
// that a link opens. A folder typed on the command line opens at once, and
// a pair link only fills the form, where the person clicks Pair.
export function mustConfirm(a: Arrival): boolean {
  return a.fromLink && a.link.type === "open";
}

// The links and folders on a command line, without the executable and the
// app path: `deck-desktop ~/src/app` opens that folder, a deck:// argument
// (how Windows and Linux hand over a link) is read as a link. Switches are
// skipped.
export function linksFromArgs(args: string[], cwd: string, isDir: (p: string) => boolean): Arrival[] {
  const out: Arrival[] = [];
  for (const a of args) {
    if (a.startsWith("-") || a === "") continue;
    if (/^deck:/i.test(a)) {
      const link = linkFromUrl(a, isDir);
      if (link !== null) out.push({ link, fromLink: true });
      continue;
    }
    const p = resolve(cwd, a);
    if (isDir(p)) out.push({ link: { type: "open", path: p }, fromLink: false });
  }
  return out;
}
