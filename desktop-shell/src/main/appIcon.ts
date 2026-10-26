import { mkdirSync, readFileSync, renameSync, writeFileSync } from "node:fs";
import { dirname } from "node:path";
import { APP_ICON_LIMITS, type AppIconImage } from "../shared/bridge";

// The app icon the renderer drew (renderer/appIcon), checked before the
// system decodes it, and kept in app-icon.json under userData so the next
// start shows it before the window loads. Nothing here touches the
// installed app: its bundle, launcher and shortcuts keep their own icon.

const SIGNATURE = [0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a];
// "IHDR": the first chunk of every PNG, 13 bytes long.
const IHDR = [0x49, 0x48, 0x44, 0x52];

const isBytes = (v: unknown): v is Uint8Array => Object.prototype.toString.call(v) === "[object Uint8Array]";

// A PNG's width and height from its header, or null when the bytes are not
// a PNG.
export function pngSize(png: Uint8Array): { width: number; height: number } | null {
  if (png.length < 33) return null;
  if (!SIGNATURE.every((b, i) => png[i] === b)) return null;
  const view = new DataView(png.buffer, png.byteOffset, png.byteLength);
  if (view.getUint32(8) !== 13 || !IHDR.every((b, i) => png[12 + i] === b)) return null;
  return { width: view.getUint32(16), height: view.getUint32(20) };
}

// Whether v is a set of icon images main may hand to the system: one to
// eight objects of exactly { size, png }, each size allowed and used once,
// each png a PNG of size × size pixels, within the byte caps.
export function isAppIcon(v: unknown): v is AppIconImage[] {
  if (!Array.isArray(v) || v.length === 0 || v.length > APP_ICON_LIMITS.sizes.length) return false;
  const seen = new Set<number>();
  let total = 0;
  for (const item of v) {
    if (typeof item !== "object" || item === null || Object.getPrototypeOf(item) !== Object.prototype) return false;
    const keys = Object.keys(item);
    if (keys.length !== 2 || !keys.includes("size") || !keys.includes("png")) return false;
    const { size, png } = item as Record<string, unknown>;
    if (!(APP_ICON_LIMITS.sizes as readonly unknown[]).includes(size) || seen.has(size as number)) return false;
    if (!isBytes(png) || png.length > APP_ICON_LIMITS.bytes) return false;
    const dims = pngSize(png);
    if (dims === null || dims.width !== size || dims.height !== size) return false;
    seen.add(size as number);
    total += png.length;
  }
  return total <= APP_ICON_LIMITS.total;
}

// The images as representations of one native image: the smallest at scale
// 1, each larger one at its size over the smallest, so the system picks the
// one drawn for the size it shows (a 16px title bar, a 32px taskbar, a
// 256px Dock tile) instead of scaling one down.
export function representations(images: AppIconImage[]): { scaleFactor: number; png: Uint8Array }[] {
  const sorted = [...images].sort((a, b) => a.size - b.size);
  const base = sorted[0]?.size ?? 1;
  return sorted.map((i) => ({ scaleFactor: i.size / base, png: i.png }));
}

// The last icon, on disk. file is null for one kept in memory only.
export class AppIconFile {
  private last: string | null = null;

  constructor(private readonly file: string | null) {}

  // The icon saved last, checked again: a file edited or cut short by hand
  // is ignored, and the app keeps its own icon.
  load(): AppIconImage[] | null {
    if (this.file === null) return null;
    try {
      const raw = readFileSync(this.file, "utf8");
      const v: unknown = JSON.parse(raw);
      const list = typeof v === "object" && v !== null ? (v as Record<string, unknown>)["images"] : undefined;
      if (!Array.isArray(list)) return null;
      const images = list.map((i: unknown) => {
        const r = (typeof i === "object" && i !== null ? i : {}) as Record<string, unknown>;
        return { size: r["size"], png: typeof r["png"] === "string" ? new Uint8Array(Buffer.from(r["png"], "base64")) : null };
      });
      if (!isAppIcon(images)) return null;
      this.last = raw;
      return images;
    } catch {
      return null;
    }
  }

  // Writes the icon, unless it is the one already there (the window sends
  // its icon at every start).
  save(images: AppIconImage[]): void {
    if (this.file === null) return;
    const body = `${JSON.stringify({ images: images.map((i) => ({ size: i.size, png: Buffer.from(i.png).toString("base64") })) })}\n`;
    if (body === this.last) return;
    mkdirSync(dirname(this.file), { recursive: true, mode: 0o700 });
    const tmp = `${this.file}.tmp`;
    writeFileSync(tmp, body, { mode: 0o600 });
    renameSync(tmp, this.file);
    this.last = body;
  }
}
