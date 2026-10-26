import { extname } from "node:path";

// The content types app:// serves the renderer's files with. The handler
// sends `nosniff`, so a file the table does not know reaches the page as
// application/octet-stream and an <img>, a font or a worker refuses it:
// every kind of file the build emits must be here (mime.test.ts walks the
// art library to keep it so).
const MIME: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".mjs": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".json": "application/json",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".jpg": "image/jpeg",
  ".jpeg": "image/jpeg",
  ".webp": "image/webp",
  ".woff2": "font/woff2",
  ".woff": "font/woff",
};

export function mimeOf(file: string): string {
  return MIME[extname(file).toLowerCase()] ?? "application/octet-stream";
}
