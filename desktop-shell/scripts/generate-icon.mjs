// Draws the desktop app's icons into build/: icon.png for Windows (a tile
// that fills the canvas), icons/ with the sizes a Linux desktop looks up,
// and icon-mac.png on macOS's grid (an 824px tile with room for its
// shadow). All have a transparent background.
// electron-builder makes the .ico and .icns from them. Run with node after
// changing the drawing: needs Playwright's Chromium.
import { mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { chromium } from "@playwright/test";

const output = join(dirname(fileURLToPath(import.meta.url)), "../build");

// The Tabletop prompt: an amber chevron and a warm cursor on a warm dark
// tile. inset is the tile's margin on a 1024 canvas.
function svg(inset, radius, shadow) {
  const size = 1024 - 2 * inset;
  // The prompt is drawn for an 824 tile and scaled to this one.
  const k = size / 824;
  const at = (x, y) => `${inset + x * k},${inset + y * k}`;
  return `<svg xmlns="http://www.w3.org/2000/svg" width="1024" height="1024" viewBox="0 0 1024 1024">
  <defs>
    <linearGradient id="tile" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#2b2721"/>
      <stop offset="1" stop-color="#141210"/>
    </linearGradient>
    <linearGradient id="amber" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#f7bb5c"/>
      <stop offset="1" stop-color="#ea972f"/>
    </linearGradient>
    <linearGradient id="sheen" x1="0" y1="0" x2="0" y2="1">
      <stop offset="0" stop-color="#fff3df" stop-opacity="0.24"/>
      <stop offset="0.5" stop-color="#fff3df" stop-opacity="0.04"/>
      <stop offset="1" stop-color="#fff3df" stop-opacity="0.07"/>
    </linearGradient>
    <filter id="shadow" x="-20%" y="-20%" width="140%" height="140%">
      <feDropShadow dx="0" dy="${shadow / 2}" stdDeviation="${shadow}" flood-color="#000" flood-opacity="0.35"/>
    </filter>
  </defs>
  <rect x="${inset}" y="${inset}" width="${size}" height="${size}" rx="${radius}" fill="url(#tile)" ${shadow > 0 ? 'filter="url(#shadow)"' : ""}/>
  <rect x="${inset + 2}" y="${inset + 2}" width="${size - 4}" height="${size - 4}" rx="${radius - 2}" fill="none" stroke="url(#sheen)" stroke-width="4"/>
  <polyline points="${at(234, 278)} ${at(404, 412)} ${at(234, 546)}" fill="none" stroke="url(#amber)" stroke-width="${76 * k}" stroke-linecap="round" stroke-linejoin="round"/>
  <rect x="${inset + 457 * k}" y="${inset + 482 * k}" width="${170 * k}" height="${64 * k}" rx="${32 * k}" fill="#f4eee4"/>
</svg>`;
}

const TILE = { inset: 48, radius: 210, shadow: 0 };
const icons = [
  { file: "icon.png", size: 1024, ...TILE },
  { file: "icon-mac.png", size: 1024, inset: 100, radius: 185, shadow: 14 },
  ...[16, 24, 32, 48, 64, 128, 256, 512, 1024].map((size) => ({ file: `icons/${size}x${size}.png`, size, ...TILE })),
];

mkdirSync(join(output, "icons"), { recursive: true });
const browser = await chromium.launch();
try {
  const page = await browser.newPage({ viewport: { width: 1024, height: 1024 } });
  for (const i of icons) {
    const style = `html,body{margin:0;background:transparent}svg{display:block;width:${i.size}px;height:${i.size}px}`;
    await page.setContent(`<style>${style}</style>${svg(i.inset, i.radius, i.shadow)}`);
    await page.locator("svg").screenshot({ path: join(output, i.file), omitBackground: true });
    console.log(`wrote build/${i.file}`);
  }
} finally {
  await browser.close();
}
