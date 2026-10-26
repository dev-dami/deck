import { expect, type Page, test } from "@playwright/test";
import { isAppIcon, pngSize } from "../../src/main/appIcon";
import { APP_ICON_SIZES, type AppIconImage } from "../../src/shared/bridge";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// The app icon: the window draws it and hands it to main through
// setAppIcon, which checks it like any other argument. Here main records
// what it was given; the Dock and the window's icon need Electron.

let fake: FakeDeck;
let sent: AppIconImage[][];

test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 250 });
  sent = [];
  await attachBridge(page, undefined, {
    setAppIcon: (images) => {
      sent.push(images);
      return true;
    },
  });
});

test.afterEach(async () => {
  await fake.close();
});

async function pair(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Pair with a Deck" }).click();
  await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
  await page.getByRole("button", { name: "Pair", exact: true }).click();
  await expect(page.locator(".project")).toHaveText("deck");
}

async function appearance(page: Page): Promise<void> {
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Appearance" }).click();
}

const theme = (page: Page, name: RegExp) =>
  page.getByRole("radiogroup", { name: "Theme", exact: true }).getByRole("radio", { name }).click();

// Waits for icon number n (from 1) and checks it is a whole, valid set.
async function nth(n: number): Promise<AppIconImage[]> {
  await expect.poll(() => sent.length).toBeGreaterThanOrEqual(n);
  const icon = sent[n - 1] as AppIconImage[];
  expect(isAppIcon(icon)).toBe(true);
  expect(icon.map((i) => i.size)).toEqual([...APP_ICON_SIZES.other]);
  for (const i of icon) expect(pngSize(i.png)).toEqual({ width: i.size, height: i.size });
  return icon;
}

// The colour of the tile in the 64px image, left of the mark and halfway
// down, decoded by Chromium.
async function ground(page: Page, icon: AppIconImage[]): Promise<[number, number, number]> {
  const png = icon.find((i) => i.size === 64)?.png;
  if (png === undefined) throw new Error("no 64px image");
  return page.evaluate(async (bytes) => {
    const bmp = await createImageBitmap(new Blob([new Uint8Array(bytes)], { type: "image/png" }));
    const ctx = new OffscreenCanvas(bmp.width, bmp.height).getContext("2d");
    if (ctx === null) throw new Error("no 2d context");
    ctx.drawImage(bmp, 0, 0);
    const [r, g, b] = ctx.getImageData(6, 32, 1, 1).data;
    return [r ?? 0, g ?? 0, b ?? 0] as [number, number, number];
  }, Array.from(png));
}

// Within a few levels of #rrggbb.
function near(rgb: [number, number, number], hex: string, slack = 14): void {
  const want = [1, 3, 5].map((i) => Number.parseInt(hex.slice(i, i + 2), 16));
  rgb.forEach((v, i) => expect(Math.abs(v - (want[i] as number)), `${rgb} vs ${hex}`).toBeLessThanOrEqual(slack));
}

test("the icon follows the theme: picking one sends its icon, valid, to main", async ({ page }) => {
  await pair(page);
  // Liquid Glass, the default: the dusk sky, blue at mid-height.
  const glass = await ground(page, await nth(1));
  expect(glass[2]).toBeGreaterThan(glass[0] + 40);
  await appearance(page);
  await theme(page, /Tokyo Night/);
  await expect(page.locator("html")).toHaveAttribute("data-palette", "tokyo-night");
  near(await ground(page, await nth(2)), "#22253a");
  await theme(page, /GitHub Light/);
  near(await ground(page, await nth(3)), "#f7f9fa");
  // The same theme again draws nothing new.
  await theme(page, /GitHub Light/);
  await page.waitForTimeout(300);
  expect(sent).toHaveLength(3);
});

test("an icon picked by hand stays through theme changes; Match theme follows again", async ({ page }) => {
  await page.addInitScript(() => {
    if (sessionStorage.getItem("seeded") !== null) return;
    sessionStorage.setItem("seeded", "1");
    localStorage.setItem("deck.desktop.prefs", JSON.stringify({ appIcon: "riso-paper" }));
  });
  await pair(page);
  // Riso on paper, although the theme is Liquid Glass.
  near(await ground(page, await nth(1)), "#f3ede1", 20);
  await appearance(page);
  await theme(page, /Nord/);
  await expect(page.locator("html")).toHaveAttribute("data-palette", "nord");
  await page.waitForTimeout(300);
  expect(sent).toHaveLength(1);

  const picker = page.getByRole("radiogroup", { name: "App icon" });
  if ((await picker.count()) > 0) {
    // Settings shows the picker: Match theme comes first and is not picked.
    await expect(picker.getByRole("radio").first()).toHaveAccessibleName("Match theme");
    await expect(picker.getByRole("radio", { name: "Riso on paper" })).toHaveAttribute("aria-checked", "true");
    await picker.getByRole("radio", { name: "Match theme" }).click();
  } else {
    // Until Settings mounts the picker, the pref is set as it would set it.
    const prefs = await page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}"));
    await page.evaluate((p) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ ...p, appIcon: "theme" })), prefs);
    await page.reload();
  }
  // Nord's icon: its tile is Nord's polar night.
  near(await ground(page, await nth(2)), "#353b49");
  const kept = await page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}"));
  expect(kept).toMatchObject({ appIcon: "theme", palette: "nord" });
});
