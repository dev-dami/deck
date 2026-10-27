import { lstat, open } from "node:fs/promises";
import { extname, join } from "node:path";

// A project's own icon: the logo or favicon its repo already has, in the
// places projects keep one, for the sidebar's project tile. Main reads it
// and hands the renderer a data: URL (the renderer's CSP allows data:
// images, and an <img> never runs an SVG's scripts).

// Where to look, in order: the one a person puts there for Deck, the repo's
// own logo, then the favicons of the web frameworks and the app icons of
// the desktop ones.
export const ICON_CANDIDATES = [
  ".deck/icon.svg",
  ".deck/icon.png",
  "logo.svg",
  "logo.png",
  "icon.svg",
  "icon.png",
  "favicon.svg",
  "favicon.png",
  "favicon.ico",
  "public/favicon.svg",
  "public/favicon.png",
  "public/favicon.ico",
  "public/logo.svg",
  "public/logo.png",
  "public/icon.svg",
  "public/icon.png",
  "static/favicon.svg",
  "static/favicon.png",
  "static/favicon.ico",
  "app/icon.svg",
  "app/icon.png",
  "app/favicon.ico",
  "src/app/icon.svg",
  "src/app/icon.png",
  "src/app/favicon.ico",
  "assets/logo.svg",
  "assets/logo.png",
  "assets/icon.svg",
  "assets/icon.png",
  ".github/logo.svg",
  ".github/logo.png",
  "docs/logo.svg",
  "docs/logo.png",
  "build/icon.png",
  "resources/icon.png",
] as const;

// Larger than this is a picture, not an icon.
export const MAX_ICON_BYTES = 512 * 1024;

const TYPES: Record<string, string> = {
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".ico": "image/x-icon",
};

// Whether the bytes are what the name says: a PNG's signature, an icon
// directory's header, an <svg> element near the start.
export function looksLike(ext: string, b: Buffer): boolean {
  if (ext === ".png") return b.subarray(0, 8).equals(Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]));
  if (ext === ".ico") return b.length > 6 && b.readUInt16LE(0) === 0 && b.readUInt16LE(2) === 1 && b.readUInt16LE(4) > 0;
  if (ext === ".svg") return /<svg[\s>]/i.test(b.subarray(0, 4096).toString("utf8"));
  return false;
}

// A regular file (not a link out of the folder) of an icon's size, or null.
async function readIcon(path: string): Promise<Buffer | null> {
  try {
    const st = await lstat(path);
    if (!st.isFile() || st.size === 0 || st.size > MAX_ICON_BYTES) return null;
    const f = await open(path, "r");
    try {
      const buf = Buffer.alloc(st.size);
      const { bytesRead } = await f.read(buf, 0, st.size, 0);
      return buf.subarray(0, bytesRead);
    } finally {
      await f.close();
    }
  } catch {
    return null;
  }
}

// The first candidate in dir that is an icon, as a data: URL; null when
// the project has none.
export async function findProjectIcon(dir: string): Promise<string | null> {
  for (const rel of ICON_CANDIDATES) {
    const ext = extname(rel);
    const type = TYPES[ext];
    if (type === undefined) continue;
    const b = await readIcon(join(dir, rel));
    if (b !== null && looksLike(ext, b)) return `data:${type};base64,${b.toString("base64")}`;
  }
  return null;
}
