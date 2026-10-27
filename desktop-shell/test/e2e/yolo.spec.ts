import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The Yolo shockwave over the fake rpc Deck, which allows yolo.
let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  host = await attachBridge(page, async () => dir);
  // Yolo asks first; the tests say yes.
  page.on("dialog", (d) => void d.accept());
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });
const fx = (page: Page) => page.locator("canvas.yolo-fx");
const threads = (page: Page) => page.getByRole("navigation", { name: "Threads" });

async function openFolder(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
}

async function openMain(page: Page): Promise<void> {
  await openFolder(page);
  await threads(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
}

// Records when the effect's canvas mounts and when it goes, from now on.
type FxTimes = { seen: number | null; gone: number | null };
async function watchFx(page: Page): Promise<void> {
  await page.evaluate(() => {
    const times: FxTimes = { seen: null, gone: null };
    (window as unknown as { yoloFx: FxTimes }).yoloFx = times;
    new MutationObserver(() => {
      const on = document.querySelector("canvas.yolo-fx") !== null;
      if (on && times.seen === null) times.seen = performance.now();
      if (!on && times.seen !== null && times.gone === null) times.gone = performance.now();
    }).observe(document.body, { childList: true, subtree: true });
  });
}
const fxTimes = (page: Page) => page.evaluate(() => (window as unknown as { yoloFx: FxTimes }).yoloFx);

// Every region's inline transform and filter, which the effect gives back.
const regionStyles = (page: Page) =>
  page.evaluate(() => [...document.querySelectorAll<HTMLElement>("[data-yolo-region]")].map((el) => `${el.style.transform}|${el.style.filter}`));

test("Ctrl+Shift+Y plays the shockwave once; the composer keeps focus and the window goes back", async ({ page }) => {
  await openMain(page);
  const regions = await regionStyles(page);
  expect(regions.length).toBeGreaterThanOrEqual(5);
  const input = composer(page);
  await input.click();
  await input.fill("abc");
  await watchFx(page);
  await input.press("Control+Shift+Y");
  await expect(fx(page)).toHaveCount(1);
  // The regions move while it plays.
  const moved = await page.evaluate(
    () =>
      new Promise<boolean>((resolve) => {
        const end = performance.now() + 700;
        const look = () => {
          const any = [...document.querySelectorAll<HTMLElement>("[data-yolo-region]")].some((el) => el.style.transform !== "");
          if (any || performance.now() > end) resolve(any);
          else requestAnimationFrame(look);
        };
        look();
      }),
  );
  expect(moved).toBe(true);
  // Typing goes on through it.
  await page.keyboard.type("def");
  await expect(input).toBeFocused();
  await expect(fx(page)).toHaveCount(0, { timeout: 2000 });
  const { seen, gone } = await fxTimes(page);
  expect(seen !== null && gone !== null && gone - seen < 2000).toBe(true);
  await expect(input).toHaveValue("abcdef");
  await expect(input).toBeFocused();
  expect(await regionStyles(page)).toEqual(regions);
  await expect(page.locator(".mode-picker")).toHaveAttribute("data-mode", "yolo");
});

test("the mode picker plays it, and a click ends it at once", async ({ page }) => {
  await openMain(page);
  const regions = await regionStyles(page);
  const box = await page.locator(".timeline").boundingBox();
  await watchFx(page);
  await page.locator(".mode-picker").click();
  await page.getByRole("menuitemradio", { name: /Yolo/ }).click();
  await expect(fx(page)).toHaveCount(1);
  // A raw click: a locator's click waits for the moving regions to settle.
  await page.mouse.click(box!.x + 20, box!.y + 20);
  await expect(fx(page)).toHaveCount(0);
  const { seen, gone } = await fxTimes(page);
  expect(seen !== null && gone !== null && gone - seen < 1000).toBe(true);
  expect(await regionStyles(page)).toEqual(regions);
});

test("Escape ends it, and another device's switch plays it too", async ({ page }) => {
  await openMain(page);
  fake.deck.invoke("mode.set", { session_id: "ses_main", mode: "yolo" });
  await expect(fx(page)).toHaveCount(1);
  await page.keyboard.press("Escape");
  await expect(fx(page)).toHaveCount(0, { timeout: 300 });
});

test("with reduced motion the canvas never mounts; the mode chip flashes", async ({ page }) => {
  await page.emulateMedia({ reducedMotion: "reduce" });
  await openMain(page);
  await expect(page.locator("html")).toHaveAttribute("data-motion", "reduced");
  await watchFx(page);
  fake.deck.invoke("mode.set", { session_id: "ses_main", mode: "yolo" });
  await expect(page.locator(".mode-picker[data-yolo-flash]")).toHaveCount(1);
  await expect(page.locator(".mode-picker")).toHaveAttribute("data-mode", "yolo");
  await page.waitForTimeout(1600);
  expect((await fxTimes(page)).seen).toBeNull();
});

test("opening a room already in yolo does not play, nor does a room in the background", async ({ page }) => {
  await openMain(page);
  await watchFx(page);
  fake.deck.invoke("mode.set", { session_id: "ses_long", mode: "yolo" });
  await page.waitForTimeout(400);
  await threads(page).getByRole("button", { name: "Long history", exact: true }).click();
  await expect(page.locator(".mode-picker")).toHaveAttribute("data-mode", "yolo");
  await page.waitForTimeout(1600);
  expect((await fxTimes(page)).seen).toBeNull();
});
