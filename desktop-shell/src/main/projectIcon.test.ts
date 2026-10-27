import { mkdirSync, mkdtempSync, symlinkSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { describe, expect, test } from "vitest";
import { fakePng } from "../../test/png";
import { findProjectIcon, looksLike, MAX_ICON_BYTES } from "./projectIcon";

const SVG = '<?xml version="1.0"?>\n<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"/>';
const ICO = Buffer.from([0, 0, 1, 0, 1, 0, 16, 16, 0, 0]);

function folder(files: Record<string, string | Uint8Array>): string {
  const dir = mkdtempSync(join(tmpdir(), "deck-icon-"));
  for (const [rel, data] of Object.entries(files)) {
    mkdirSync(join(dir, rel, ".."), { recursive: true });
    writeFileSync(join(dir, rel), data);
  }
  return dir;
}

describe("a project's own icon", () => {
  test("none in a folder without one", async () => {
    expect(await findProjectIcon(folder({ "README.md": "# hi" }))).toBeNull();
  });

  test("the first candidate wins: the Deck one, then the logo, then a favicon", async () => {
    const dir = folder({ "public/favicon.ico": ICO, "logo.svg": SVG });
    expect(await findProjectIcon(dir)).toBe(`data:image/svg+xml;base64,${Buffer.from(SVG).toString("base64")}`);
    const deck = folder({ "logo.svg": SVG, ".deck/icon.png": fakePng(32) });
    expect(await findProjectIcon(deck)).toMatch(/^data:image\/png;base64,/);
    expect(await findProjectIcon(folder({ "public/favicon.ico": ICO }))).toMatch(/^data:image\/x-icon;base64,/);
  });

  test("a file that is not what its name says, too big, or a link is passed over", async () => {
    const dir = folder({
      "logo.png": "not a png",
      "icon.svg": "<html></html>",
      "favicon.png": Buffer.concat([Buffer.from(fakePng(16)), Buffer.alloc(MAX_ICON_BYTES)]),
      "app/favicon.ico": ICO,
    });
    symlinkSync("/etc/hosts", join(dir, "logo.svg"));
    expect(await findProjectIcon(dir)).toMatch(/^data:image\/x-icon;base64,/);
  });

  test("the bytes are checked by kind", () => {
    expect(looksLike(".png", Buffer.from(fakePng(8)))).toBe(true);
    expect(looksLike(".png", Buffer.from("GIF89a"))).toBe(false);
    expect(looksLike(".ico", ICO)).toBe(true);
    expect(looksLike(".ico", Buffer.from([0, 0, 2, 0, 1, 0, 0]))).toBe(false);
    expect(looksLike(".svg", Buffer.from(SVG))).toBe(true);
    expect(looksLike(".svg", Buffer.from("<svgx>"))).toBe(false);
    expect(looksLike(".gif", Buffer.from("GIF89a"))).toBe(false);
  });
});
