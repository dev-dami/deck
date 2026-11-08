import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// The starter tour (ui/Tour.tsx). A window a test drives never starts it
// by itself; the palette and Settings start it, and a real first run (the
// driver hidden) walks the Welcome's steps, then the project's.
// SHOTS=<dir> also saves a picture of some steps.
const OUT = process.env["SHOTS"] ?? "";
let fake: FakeDeck;

test.beforeEach(async () => {
  fake = await FakeDeck.start({ tickMs: 250 });
});
test.afterEach(async () => fake.close());

async function pair(page: Page): Promise<void> {
  await page.getByRole("button", { name: "Pair with a Deck" }).click();
  await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
  await page.getByRole("button", { name: "Pair", exact: true }).click();
  await expect(page.locator(".project")).toHaveText("deck");
}

const card = (page: Page) => page.locator(".tour-card");
const at = (page: Page, step: string) => expect(page.locator(`.tour[data-step="${step}"]`)).toBeVisible();
const shot = async (page: Page, name: string) => {
  if (OUT === "") return;
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(OUT, `tour-${name}.png`) });
};

test("the palette starts the tour, which steps past what is not there and never takes a click", async ({ page }) => {
  await attachBridge(page);
  await page.goto("/");
  await expect(page.locator(".welcome-screen")).toBeVisible();
  await pair(page);
  // Under a test driver it does not start by itself.
  await page.waitForTimeout(1500);
  await expect(page.locator(".tour")).toHaveCount(0);

  await page.keyboard.press("Control+k");
  await page.getByRole("textbox", { name: "Search commands and sessions" }).fill("> tour");
  await page.keyboard.press("Enter");
  await at(page, "project");
  await expect(card(page)).toContainText("A quick look around");
  await expect(card(page)).toContainText("Tour · 1 of");
  await expect(card(page).getByRole("button", { name: "Back" })).toBeDisabled();
  await expect(card(page).getByRole("button", { name: "Next" })).toBeFocused();

  // The spotlight rings the sidebar.
  await card(page).getByRole("button", { name: "Next" }).click();
  await at(page, "threads");
  const nav = await page.getByRole("navigation", { name: "Threads" }).boundingBox();
  await expect
    .poll(async () => {
      const spot = await page.locator(".tour-spot").boundingBox();
      // The sidebar grown by a few pixels, inside the window.
      if (spot === null || nav === null) return false;
      const right = (b: { x: number; width: number }) => b.x + b.width;
      return spot.x <= nav.x && right(spot) >= right(nav) && spot.width - nav.width <= 12;
    })
    .toBe(true);
  await shot(page, "threads");

  // The arrow keys step, both ways.
  await page.keyboard.press("ArrowRight");
  await at(page, "composer");
  await expect(card(page)).toContainText("$claude");
  await shot(page, "composer");
  // A new thread has no mode dial: its step shows in the middle.
  await page.keyboard.press("ArrowRight");
  await at(page, "mode");
  await expect(page.locator(".tour-spot")).toHaveAttribute("data-empty", "");
  await page.keyboard.press("ArrowLeft");
  await at(page, "composer");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  // The new thread shows the home cards, not the views: those are passed.
  await at(page, "home");
  await shot(page, "home");
  await page.keyboard.press("ArrowRight");
  await at(page, "panels");
  await page.keyboard.press("ArrowLeft");
  await at(page, "home");
  await page.keyboard.press("ArrowRight");
  await at(page, "panels");

  // The veil takes no click: a thread opens under it, and its views and
  // its dial now make steps of their own.
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator(".title")).toHaveText("Fix the pairing lockout");
  // The room took the focus to its composer, where the arrows move the
  // caret, not the tour: Back steps.
  await page.keyboard.press("ArrowLeft");
  await at(page, "panels");
  await card(page).getByRole("button", { name: "Back" }).click();
  await at(page, "views");
  await shot(page, "views");
  await page.keyboard.press("ArrowLeft");
  await at(page, "mode");
  await expect(page.locator(".tour-spot")).not.toHaveAttribute("data-empty", "");
  await shot(page, "mode");
  await page.keyboard.press("ArrowRight");
  await page.keyboard.press("ArrowRight");
  await at(page, "panels");
  await page.keyboard.press("ArrowRight");
  await at(page, "palette");
  await expect(page.locator(".tour-spot")).toHaveAttribute("data-empty", "");
  await page.keyboard.press("ArrowRight");
  await at(page, "appearance");
  await shot(page, "appearance");
  await card(page).getByRole("button", { name: "Done" }).click();
  await expect(page.locator(".tour")).toHaveCount(0);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}").tour)).toBe("done");
});

test("Settings shows the tour over the room, and Esc closes it", async ({ page }) => {
  await attachBridge(page);
  await page.goto("/");
  await pair(page);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Show the tour" }).click();
  await at(page, "project");
  // Settings gave way to the room the tour is about.
  await expect(page.locator(".composer")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(page.locator(".tour")).toHaveCount(0);
  await expect(page.locator(".composer")).toBeVisible();
});

test("a real first run walks the Welcome's steps, then the project's", async ({ page }) => {
  // The driver hidden: what a person's first launch looks like.
  await page.addInitScript(() => Object.defineProperty(Navigator.prototype, "webdriver", { get: () => false }));
  await attachBridge(page, undefined, {
    appInfo: { version: "0.1.0", electron: "test", chromium: "test", platform: process.platform, automated: false },
  });
  await page.emulateMedia({ reducedMotion: "reduce" });
  await page.goto("/");
  await at(page, "welcome");
  await expect(card(page)).toContainText("Welcome to Deck");
  await shot(page, "welcome");
  await page.keyboard.press("ArrowRight");
  await at(page, "open-folder");
  await shot(page, "open-folder");
  await page.keyboard.press("ArrowRight");
  await at(page, "pair");
  await page.keyboard.press("ArrowRight");
  await at(page, "agents");
  await card(page).getByRole("button", { name: "Got it" }).click();
  await expect(page.locator(".tour")).toHaveCount(0);
  // Still new: the project's part comes once a project is open.
  await pair(page);
  await at(page, "project");
  await shot(page, "project");
  await card(page).getByRole("button", { name: "Skip" }).click();
  await expect(page.locator(".tour")).toHaveCount(0);
  expect(await page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}").tour)).toBe("done");
  // Done is done: a new window does not start it again.
  await page.reload();
  await expect(page.locator(".composer")).toBeVisible();
  await page.waitForTimeout(1500);
  await expect(page.locator(".tour")).toHaveCount(0);
});
