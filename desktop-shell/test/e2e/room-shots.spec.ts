import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// Screenshots of the room's states, for design review: the timeline with
// its work open, a reply streaming, an approval over the composer, the
// composer's suggestions, the new-thread screen, the graph view and the
// smallest window. SHOTS=<dir> npx playwright test room-shots; without
// SHOTS the e2e run skips them.
const OUT = process.env["SHOTS"] ?? "";
const PALETTE = process.env["PALETTE"] ?? "";
test.skip(OUT === "", "SHOTS names the folder for the screenshots");
let fake: FakeDeck;
test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 700 });
  await attachBridge(page);
});
test.afterEach(async () => fake.close());

async function setup(page: Page, mode: "dark" | "light") {
  await page.emulateMedia({ colorScheme: mode });
  await page.addInitScript(
    ([p, m]) => localStorage.setItem("deck.desktop.prefs", JSON.stringify(p ? { theme: m, palette: p } : { theme: m })),
    [PALETTE, mode] as const,
  );
  await page.goto("/");
  await page.getByRole("button", { name: "Pair with a Deck" }).click();
  await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
  await page.getByRole("button", { name: "Pair", exact: true }).click();
  await expect(page.locator(".project")).toHaveText("deck");
}

const nav = (page: Page) => page.getByRole("navigation", { name: "Threads" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });
const shot = (page: Page, name: string) => page.screenshot({ path: join(OUT, `${name}.png`) });

for (const mode of ["dark", "light"] as const) {
  test(`room shots ${mode}`, async ({ page }) => {
    await setup(page, mode);
    await page.mouse.move(2, 600);
    await composer(page).fill("$claude $codex ");
    await page.waitForTimeout(300);
    await shot(page, `${mode}-r0-new`);
    await composer(page).fill("");

    await nav(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
    await page.locator(".worked").first().click();
    await page.locator(".actions-head").first().click();
    await page.locator("details.tool summary").first().click();
    await page.locator(".changed-files button").first().click();
    await page.mouse.move(700, 300);
    await page.waitForTimeout(400);
    await shot(page, `${mode}-r1-room`);

    await composer(page).fill("$claude $codex compare the fixes and add a test for the per-address limit");
    await composer(page).press("Enter");
    await expect(page.locator(".streaming").first()).toBeVisible();
    await page.waitForTimeout(1600);
    await shot(page, `${mode}-r2-streaming`);
    await expect(page.locator(".streaming")).toHaveCount(0, { timeout: 15_000 });

    await composer(page).fill("$");
    await page.waitForTimeout(250);
    await shot(page, `${mode}-r3-suggest`);
    await composer(page).fill("");

    await page.keyboard.press("Control+Shift+G");
    await page.waitForTimeout(250);
    await shot(page, `${mode}-r4-graph`);
    await page.keyboard.press("Control+Shift+G");

    await nav(page).getByRole("button", { name: "Long history", exact: true }).click();
    await expect(page.locator(".msg").last()).toContainText("Note 130");
    await page.locator(".timeline").evaluate((el) => el.scrollBy(0, -260));
    await expect(page.getByRole("button", { name: "Jump to latest" })).toBeVisible();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-r8-jump`);

    await nav(page).getByRole("button", { name: "Docs pass", exact: true }).click();
    await expect(page.locator('[aria-label="Approvals for this thread"]')).toBeVisible();
    await page.waitForTimeout(400);
    await shot(page, `${mode}-r5-approval`);

    await page.setViewportSize({ width: 960, height: 600 });
    await nav(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await page.waitForTimeout(400);
    await shot(page, `${mode}-r6-narrow`);
    await nav(page).getByRole("button", { name: "Docs pass", exact: true }).click();
    await page.waitForTimeout(400);
    await shot(page, `${mode}-r7-narrow-approval`);
    await page.getByRole("button", { name: "New thread", exact: true }).first().click();
    await expect(page.locator(".title")).toHaveText("New thread");
    await page.waitForTimeout(300);
    await shot(page, `${mode}-r9-narrow-new`);
  });
}
