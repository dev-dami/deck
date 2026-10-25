import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// The window's layout under load and at its smallest: a long room renders
// the rows in view only, and 960×600 keeps the header and the composer whole.

let fake: FakeDeck;

test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 100, bigRoom: 500 });
  await attachBridge(page);
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

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

test("a room of 500 messages renders the rows in view and follows the last one", async ({ page }) => {
  await pair(page);
  await sidebar(page).getByRole("button", { name: "Big room", exact: true }).click();
  await expect(page.locator(".msg").last()).toContainText("Big 500");
  // Pages of 100 come in as the person asks for older ones.
  const older = page.getByRole("button", { name: "Load older messages" });
  for (let i = 0; i < 4; i++) {
    await older.click();
    await expect(page.getByRole("button", { name: "Loading…" })).toHaveCount(0);
  }
  await expect(older).toHaveCount(0);
  const count = await page.locator(".msg").count();
  expect(count).toBeGreaterThan(0);
  expect(count).toBeLessThan(100);

  // The person scrolled up: the pill takes them back to the last message.
  const jump = page.getByRole("button", { name: "Jump to latest" });
  await expect(jump).toBeVisible();
  await jump.click();
  await expect(page.locator(".msg", { hasText: "Big 500" })).toBeInViewport();
  await expect(jump).toHaveCount(0);
  expect(await page.locator(".msg").count()).toBeLessThan(100);

  // At the bottom, the timeline follows a new turn and its reply.
  await composer(page).fill("one more");
  await composer(page).press("Enter");
  await expect(page.locator('[data-agent="claude-1"].msg').last()).toContainText("one more");
  await expect(page.locator(".msg").last()).toBeInViewport();
  await expect(jump).toHaveCount(0);
});

test("at 960×600 the header, the lane strip and the composer show whole", async ({ page }) => {
  await page.setViewportSize({ width: 960, height: 600 });
  await pair(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator(".title")).toHaveText("Fix the pairing lockout");
  // Nothing covers the title, the lanes or the prompt.
  const covered = () =>
    page.evaluate(() =>
      [".title", ".lanes", 'textarea[aria-label="Message"]', ".panel-toggles"].map((s) => {
        const el = document.querySelector(s);
        if (el === null) return `${s}: missing`;
        const r = el.getBoundingClientRect();
        const at = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
        return el.contains(at) ? "" : `${s}: covered`;
      }),
    );
  expect(await covered()).toEqual(["", "", "", ""]);
  await expect(page.locator(".topbar")).toBeInViewport({ ratio: 1 });
  await expect(page.locator(".composer-box")).toBeInViewport({ ratio: 1 });
  // The header's parts sit side by side, and the strip wraps in place of a scroll.
  const title = await page.locator(".title").boundingBox();
  const toggles = await page.locator(".panel-toggles").boundingBox();
  expect(title !== null && toggles !== null && title.x + title.width <= toggles.x).toBe(true);
  const lanes = page.locator(".lanes");
  expect(await lanes.evaluate((el) => el.scrollWidth <= el.clientWidth)).toBe(true);

  // Below 1100px the side panel floats over the room, which keeps its width.
  const before = await page.locator("main.center").boundingBox();
  await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  await expect(panel).toBeVisible();
  expect(await panel.evaluate((el) => getComputedStyle(el).position)).toBe("absolute");
  expect((await page.locator("main.center").boundingBox())?.width).toBe(before?.width);});
