import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// The art directions (docs/design-glass.md, "Art directions"): one picture
// printed through each theme's process behind the window and on the first
// screens, with the room's sheet opaque over it.

let fake: FakeDeck;
test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 250 });
  await attachBridge(page);
});
test.afterEach(async () => fake.close());

async function start(page: Page, palette: string, mode: "dark" | "light"): Promise<void> {
  await page.emulateMedia({ colorScheme: mode });
  await page.addInitScript(([p, m]) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m, palette: p })), [palette, mode] as const);
  await page.goto("/");
}

async function pair(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Pair with a Deck" }).click();
  await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
  await page.getByRole("button", { name: "Pair", exact: true }).click();
  await expect(page.locator(".project")).toHaveText("deck");
}

// The pixels of the window's backdrop inside a box, read back from its
// canvas: how many are the riso's pink.
function pinkIn(page: Page, box: { x: number; y: number; w: number; h: number }): Promise<number> {
  return page.evaluate((b) => {
    const src = document.querySelector<HTMLCanvasElement>("canvas.backdrop");
    if (src === null) return -1;
    const k = src.width / window.innerWidth;
    const c = document.createElement("canvas");
    c.width = src.width;
    c.height = src.height;
    const ctx = c.getContext("2d", { willReadFrequently: true });
    if (ctx === null) return -1;
    ctx.drawImage(src, 0, 0);
    const d = ctx.getImageData(Math.round(b.x * k), Math.round(b.y * k), Math.round(b.w * k), Math.round(b.h * k)).data;
    let n = 0;
    for (let i = 0; i < d.length; i += 4) if ((d[i] ?? 0) > 200 && (d[i + 1] ?? 0) < 140 && (d[i + 2] ?? 0) > 120) n++;
    return n;
  }, box);
}

test("each direction prints the first screen's picture through its own process", async ({ page }) => {
  for (const [palette, process] of [
    ["glass", "scene"],
    ["riso", "halftone"],
    ["stitch", "stitch"],
  ] as const) {
    await start(page, palette, "dark");
    await expect(page.locator(".welcome-screen .art-print")).toHaveAttribute("data-print", process, { timeout: 10_000 });
    await expect(page.locator("html")).toHaveAttribute("data-backdrop", process, { timeout: 10_000 });
    // The first screens print their own picture; the window's is hidden.
    await expect(page.locator("canvas.backdrop")).toBeHidden();
    await expect(page.locator(".welcome-screen figcaption")).not.toBeEmpty();
  }
});

test("riso keeps its ink off the gutters and away from the sidebar", async ({ page }) => {
  for (const size of [
    { width: 1360, height: 860 },
    { width: 1024, height: 700 },
  ]) {
    await page.setViewportSize(size);
    for (const mode of ["dark", "light"] as const) {
      await start(page, "riso", mode);
      await expect(page.locator("html")).toHaveAttribute("data-backdrop", "halftone", { timeout: 10_000 });
      const { width: W, height: H } = size;
      // The sidebar and the gutter beside it.
      expect(await pinkIn(page, { x: 0, y: 0, w: 280, h: H })).toBe(0);
      // The window's outer gutters.
      expect(await pinkIn(page, { x: 0, y: H - 10, w: W, h: 10 })).toBe(0);
      expect(await pinkIn(page, { x: W - 10, y: 0, w: 10, h: H })).toBe(0);
      expect(await pinkIn(page, { x: 0, y: 0, w: W, h: 10 })).toBe(0);
      // And the picture is there: the spot colour prints right of the sidebar.
      expect(await pinkIn(page, { x: 280, y: 0, w: W - 280, h: H })).toBeGreaterThan(1000);
    }
  }
});

test("the room's sheet stays opaque, and riso and stitch turn the glass's blur off", async ({ page }) => {
  await start(page, "glass", "dark");
  await pair(page);
  await expect(page.locator("canvas.backdrop")).toBeVisible();
  for (const palette of ["glass", "riso", "stitch"]) {
    for (const mode of ["dark", "light"] as const) {
      await page.emulateMedia({ colorScheme: mode });
      await page.evaluate(([p, m]) => {
        // The theme and mode as appearance.ts writes them on <html>.
        document.documentElement.setAttribute("data-palette", p);
        document.documentElement.setAttribute("data-mode", m);
      }, [palette, mode] as const);
      const look = await page.evaluate(() => {
        const sheet = document.querySelector(".room-sheet");
        const glass = document.querySelector("aside .liquid, .liquid");
        return {
          bg: sheet === null ? "" : getComputedStyle(sheet).backgroundColor,
          blur: glass === null ? "" : getComputedStyle(glass).backdropFilter,
        };
      });
      // An opaque colour: rgb(), or an alpha of 1.
      expect(look.bg, `${palette} ${mode}`).toMatch(/^rgb\(\d+, \d+, \d+\)$|^(color|oklch|lab)\([^/]*\)$/);
      if (palette === "glass") expect(look.blur, `${palette} ${mode}`).toMatch(/blur\([1-9]/);
      else expect(look.blur, `${palette} ${mode}`).not.toMatch(/blur\([1-9]/);
    }
  }
});

test("the picture picker prints another picture, and the theme's own comes back", async ({ page }) => {
  await start(page, "riso", "dark");
  await pair(page);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Appearance" }).click();
  const theme = page.getByRole("radiogroup", { name: "Theme", exact: true });
  await expect(theme.getByRole("radio", { name: "Riso" })).toHaveAttribute("aria-checked", "true");
  const pictures = page.getByRole("radiogroup", { name: "Picture" });
  await expect(pictures.getByRole("radio", { name: /Allium oreophilum/ })).toHaveAttribute("aria-checked", "true");
  await pictures.getByRole("radio", { name: /Under the Wave off Kanagawa/ }).click();
  await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}").wallpaper)).toBe("great-wave");
  await pictures.getByRole("radio", { name: /Allium oreophilum/ }).click();
  await expect.poll(() => page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}").wallpaper)).toBe("theme");
  // Another direction offers its own set, and prints through its process.
  await theme.getByRole("radio", { name: "Stitch" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-backdrop", "stitch", { timeout: 10_000 });
  await expect(pictures.getByRole("radio", { name: /Black Cañon/ })).toHaveAttribute("aria-checked", "true");
});
