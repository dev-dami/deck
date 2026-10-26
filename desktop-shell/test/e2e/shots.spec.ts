import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// Screenshots of the main screens in dark and light, for design review:
// SHOTS=<dir> npm run shots. Without SHOTS the e2e run skips them.
const OUT = process.env["SHOTS"] ?? "";
const PALETTE = process.env["PALETTE"] ?? "";
test.skip(OUT === "", "SHOTS names the folder for the screenshots");
let fake: FakeDeck;
test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 250 });
  await attachBridge(page);
});
test.afterEach(async () => fake.close());

async function setup(page: Page, mode: "dark" | "light") {
  await page.emulateMedia({ colorScheme: mode });
  await page.addInitScript(([p, m]) => {
    const prefs: Record<string, unknown> = { theme: m };
    if (p) prefs["palette"] = p;
    localStorage.setItem("deck.desktop.prefs", JSON.stringify(prefs));
  }, [PALETTE, mode] as const);
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "One deck for all your agents" })).toBeVisible();
}

for (const mode of ["dark", "light"] as const) {
  test(`shots ${mode}`, async ({ page }) => {
    await setup(page, mode);
    await page.waitForTimeout(300);
    // The backdrop alone, at full size, and how long it took to print.
    await expect(page.locator("html[data-backdrop]")).toBeAttached({ timeout: 10_000 });
    const ms = await page.locator("html").getAttribute("data-backdrop-ms");
    console.log(`backdrop ${PALETTE || "glass"} ${mode}: ${ms} ms`);
    const show = (v: string) =>
      page.evaluate((vis) => {
        for (const el of document.querySelectorAll<HTMLElement>(".app > :not(.backdrop)")) el.style.visibility = vis;
        // The first screens hide the window's backdrop; this shot shows it.
        const b = document.querySelector<HTMLElement>(".backdrop");
        if (b !== null) b.style.visibility = vis === "hidden" ? "visible" : "";
      }, v);
    await show("hidden");
    await page.screenshot({ path: join(OUT, `${mode}-0-backdrop.png`) });
    await show("");
    await page.screenshot({ path: join(OUT, `${mode}-1-welcome.png`) });
    await page.getByRole("button", { name: "Pair with a Deck" }).click();
    await page.waitForTimeout(200);
    await page.screenshot({ path: join(OUT, `${mode}-2-pair.png`) });
    await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
    await page.getByRole("button", { name: "Pair", exact: true }).click();
    await expect(page.locator(".project")).toHaveText("deck");
    const nav = page.getByRole("navigation", { name: "Threads" });
    await nav.getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
    await page.waitForTimeout(500);
    await page.screenshot({ path: join(OUT, `${mode}-3-room.png`) });
    await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(OUT, `${mode}-4-diff.png`) });
    await page.keyboard.press("Control+k");
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(OUT, `${mode}-5-palette.png`) });
    await page.keyboard.press("Escape");
    await page.keyboard.press("Control+,");
    await page.waitForTimeout(400);
    await page.screenshot({ path: join(OUT, `${mode}-6-settings.png`) });
  });
}
