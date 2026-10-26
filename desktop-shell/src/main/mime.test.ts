import { readdirSync } from "node:fs";
import { extname, join } from "node:path";
import { describe, expect, test } from "vitest";
import { mimeOf } from "./mime";

const OCTET = "application/octet-stream";

// Every file under a folder, recursively.
function walk(dir: string): string[] {
  return readdirSync(dir, { withFileTypes: true }).flatMap((e) => (e.isDirectory() ? walk(join(dir, e.name)) : [join(dir, e.name)]));
}

describe("mimeOf", () => {
  test("names the pictures, fonts and scripts the renderer loads", () => {
    expect(mimeOf("/r/assets/alpine-braun-1870-abc.jpg")).toBe("image/jpeg");
    expect(mimeOf("/r/assets/x.JPEG")).toBe("image/jpeg");
    expect(mimeOf("/r/assets/x.webp")).toBe("image/webp");
    expect(mimeOf("/r/assets/alps-abc.png")).toBe("image/png");
    expect(mimeOf("/r/assets/inter-tight-latin-wght-normal.woff2")).toBe("font/woff2");
    expect(mimeOf("/r/assets/pixelify-sans-latin-500-normal.woff")).toBe("font/woff");
    expect(mimeOf("/r/assets/worker-abc.js")).toBe("text/javascript; charset=utf-8");
    expect(mimeOf("/r/index.html")).toBe("text/html; charset=utf-8");
  });

  test("leaves what it does not know as bytes", () => {
    expect(mimeOf("/r/a.exe")).toBe(OCTET);
    expect(mimeOf("/r/noext")).toBe(OCTET);
  });

  // The build copies these files as they are, so their kinds are the
  // kinds app:// must name; under nosniff an unknown one would not show.
  test("knows every kind of picture in the art library", () => {
    const art = join(__dirname, "..", "renderer", "art");
    const kinds = new Set(
      walk(art)
        .map((f) => extname(f).toLowerCase())
        .filter((e) => e !== ".md" && e !== ".json"),
    );
    expect(kinds.size).toBeGreaterThan(0);
    for (const k of kinds) expect(mimeOf(`x${k}`), k).not.toBe(OCTET);
  });
});
