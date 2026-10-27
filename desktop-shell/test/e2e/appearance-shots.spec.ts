import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// Screenshots of looks mixed from the aspects and of the layouts, for
// design review: SHOTS=<dir> npx playwright test appearance-shots. Without
// SHOTS the e2e run skips them.
const OUT = process.env["SHOTS"] ?? "";
test.skip(OUT === "", "SHOTS names the folder for the screenshots");
let fake: FakeDeck;
test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 250 });
  await attachBridge(page);
});
test.afterEach(async () => fake.close());

async function room(page: Page, mode: "dark" | "light", prefs: Record<string, unknown>) {
  await page.emulateMedia({ colorScheme: mode });
  await page.addInitScript((p) => localStorage.setItem("deck.desktop.prefs", JSON.stringify(p)), { theme: mode, ...prefs });
  await page.goto("/");
  await page.getByRole("button", { name: "Pair with a Deck" }).click();
  await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
  await page.getByRole("button", { name: "Pair", exact: true }).click();
  await expect(page.locator(".project")).toHaveText("deck");
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
  if (prefs["process"] !== "none") await expect(page.locator("html[data-backdrop]")).toBeAttached({ timeout: 10_000 });
  await page.waitForTimeout(600);
}

const LOOKS: ReadonlyArray<[string, "dark" | "light", Record<string, unknown>]> = [
  ["glass", "dark", {}],
  ["riso-inks-on-glass", "dark", { palette: "riso", material: "glass", process: "dither", face: "inter-tight", texture: "none" }],
  ["riso-inks-on-glass", "light", { palette: "riso", material: "glass", process: "halftone", face: "inter-tight", texture: "none" }],
  ["felt-t3", "dark", { palette: "default", material: "felt", process: "stitch", face: "pixelify", texture: "felt" }],
  ["felt-t3", "light", { palette: "default", material: "felt", process: "stitch", face: "pixelify", texture: "felt" }],
  ["paper-tokyo-night", "dark", { palette: "tokyo-night", material: "paper", process: "halftone", face: "space-grotesk", texture: "fibre" }],
  ["plain-nord-none", "dark", { palette: "nord", material: "plain", process: "none", face: "system", texture: "none", corners: "square" }],
  ["mirrored", "dark", { sidebarSide: "right" }],
  ["mirrored-beside-compact", "light", { sidebarSide: "right", panelDock: "beside", density: "compact" }],
  ["stitch-float", "light", { palette: "stitch", material: "felt", process: "stitch", face: "pixelify", texture: "felt", panelDock: "float" }],
];

for (const [name, mode, prefs] of LOOKS) {
  test(`look ${name} ${mode}`, async ({ page }) => {
    await room(page, mode, prefs);
    await page.screenshot({ path: join(OUT, `${name}-${mode}.png`) });
  });
}

// A Mac's chrome: the platform says Mac, and three dots stand where main
// puts the traffic lights (main.ts, trafficLightPosition), to check that
// whichever panel the layout puts at the top left clears them.
const MAC: ReadonlyArray<[string, Record<string, unknown>, boolean]> = [
  ["mac-left", {}, false],
  ["mac-mirrored", { sidebarSide: "right" }, false],
  ["mac-mirrored-beside", { sidebarSide: "right", panelDock: "beside" }, false],
  ["mac-mirrored-hidden", { sidebarSide: "right" }, true],
];
for (const [name, prefs, hide] of MAC) {
  test(`chrome ${name}`, async ({ page }) => {
    await page.addInitScript(() => Object.defineProperty(navigator, "platform", { get: () => "MacIntel" }));
    await room(page, "dark", prefs);
    if (hide) await page.keyboard.press("Meta+b");
    await page.evaluate(() => {
      for (const [i, c] of ["#ff5f57", "#febc2e", "#28c840"].entries()) {
        const d = document.createElement("div");
        d.style.cssText = `position:fixed;z-index:9999;top:19px;left:${18 + i * 20}px;width:12px;height:12px;border-radius:50%;background:${c}`;
        document.body.append(d);
      }
    });
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(OUT, `${name}.png`), clip: { x: 0, y: 0, width: 700, height: 140 } });
  });
}

for (const mode of ["dark", "light"] as const) {
  test(`appearance editor ${mode}`, async ({ page }) => {
    await room(
      page,
      mode,
      mode === "dark"
        ? { palette: "tokyo-night", material: "paper", process: "halftone", face: "space-grotesk", texture: "fibre" }
        : { palette: "glass", material: "glass", process: "dither", face: "inter-tight", texture: "felt", corners: "round" },
    );
    await page.keyboard.press("Control+,");
    await page.getByRole("button", { name: "Appearance" }).click();
    await page.waitForTimeout(1200);
    await page.screenshot({ path: join(OUT, `editor-${mode}-1.png`) });
    const top = (name: string) => page.getByRole("heading", { name, exact: true }).evaluate((e) => e.scrollIntoView({ block: "start" }));
    await top("Mix your own");
    await page.waitForTimeout(800);
    await page.screenshot({ path: join(OUT, `editor-${mode}-2.png`) });
    await top("Layout");
    await page.waitForTimeout(300);
    await page.screenshot({ path: join(OUT, `editor-${mode}-3-layout.png`) });
  });
}
