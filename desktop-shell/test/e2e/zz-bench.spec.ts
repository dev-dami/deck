import { expect, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

const OUT = process.env["BENCH"] ?? "";
test.skip(OUT === "", "BENCH");
test.use({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: Number(process.env["DPR"] ?? 2) });
let fake: FakeDeck;
test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 250 });
  await attachBridge(page);
});
test.afterEach(async () => fake.close());

for (const palette of (process.env["PALETTES"] ?? "glass,riso,stitch").split(","))
  for (const mode of ["dark", "light"] as const) {
    test(`bench ${palette} ${mode}`, async ({ page }) => {
      await page.emulateMedia({ colorScheme: mode });
      await page.addInitScript(
        ([p, m]) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m, palette: p })),
        [palette, mode] as const,
      );
      await page.goto("/");
      await expect(page.locator("html[data-backdrop]")).toBeAttached({ timeout: 10_000 });
      const times: string[] = [];
      for (const w of [1441, 1440, 1441, 1440]) {
        const before = await page.locator("html").getAttribute("data-backdrop-ms");
        await page.setViewportSize({ width: w, height: 900 });
        await expect(page.locator("html")).not.toHaveAttribute("data-backdrop-ms", before ?? "", { timeout: 10_000 });
        times.push((await page.locator("html").getAttribute("data-backdrop-ms")) ?? "");
      }
      console.log(`${palette} ${mode}: ${times.join(" ")}`, await page.evaluate(() => (globalThis as unknown as { __marks?: string }).__marks));
      await page.screenshot({ path: `${OUT}/${palette}-${mode}.png` });
    });
  }
