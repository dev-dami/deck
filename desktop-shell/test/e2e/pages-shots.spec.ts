import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

// Screenshots of the pages (Welcome, Connect, every Settings section,
// Usage, Team, Inbox) in dark and light, and the first screens at 960×600,
// for design review: SHOTS=<dir> npx playwright test pages-shots. Without
// SHOTS the e2e run skips them.
const OUT = process.env["SHOTS"] ?? "";
const PALETTE = process.env["PALETTE"] ?? "";
test.skip(OUT === "", "SHOTS names the folder for the screenshots");

let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });

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

const shot = (page: Page, name: string) => page.screenshot({ path: join(OUT, `${name}.png`) });

for (const mode of ["dark", "light"] as const) {
  test(`pages ${mode}`, async ({ page }) => {
    await setup(page, mode);
    await expect(page.locator('.agent-clis [data-found="true"]').first()).toBeVisible();
    await page.waitForTimeout(400);
    await shot(page, `${mode}-p01-welcome`);
    await page.getByRole("button", { name: "Pair with a Deck" }).click();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-p02-pair`);
    await page.goto("/");
    await page.getByRole("button", { name: "Open a folder" }).click();
    await expect(page.locator(".link")).toHaveText("Connected");

    // An approval and a finished room, for the inbox.
    fake.deck.requestApproval("ses_long", "claude-1", "rm -rf build");
    fake.emit("notice", {
      session_id: "ses_long",
      text: "codex-2 and claude-1 both changed internal/auth/auth.go in the last few minutes; one may undo the other",
      via: "overlap",
    });
    await sidebar(page).getByRole("button", { name: /^Inbox/ }).click();
    await expect(page.getByRole("listbox", { name: "Waiting for you" }).getByRole("option").first()).toBeVisible();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-p03-inbox`);

    await sidebar(page).getByRole("button", { name: "Usage" }).click();
    await expect(page.locator('[data-usage] [data-tile="Tokens"]')).toBeVisible();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-p04-usage`);
    await page.locator("[data-usage] .overflow-y-auto").first().evaluate((e) => e.scrollTo(0, 99999));
    await page.waitForTimeout(200);
    await shot(page, `${mode}-p05-usage-end`);

    await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await sidebar(page).getByRole("button", { name: /teammate/ }).click();
    await expect(page.locator('[data-team] .teammate[data-peer="vps"]')).toBeVisible();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-p06-team`);

    await page.keyboard.press("Control+,");
    for (const [i, s] of ["General", "Appearance", "Agents", "Connection", "Phone", "Keyboard shortcuts"].entries()) {
      await page.getByRole("button", { name: s, exact: true }).click();
      await page.waitForTimeout(300);
      await shot(page, `${mode}-p1${i}-settings-${s.split(" ")[0]!.toLowerCase()}`);
    }
    await page.getByRole("button", { name: "Appearance", exact: true }).click();
    await page.locator("[data-settings] .overflow-y-auto").first().evaluate((e) => e.scrollTo(0, 520));
    await page.waitForTimeout(200);
    await shot(page, `${mode}-p17-settings-appearance-2`);
  });

  // A Deck paired from a terminal keeps no usage or team here: the empty
  // pages, each with its plate.
  test(`empty ${mode}`, async ({ page }) => {
    const paired = await FakeDeck.start({ tickMs: 250 });
    try {
      await setup(page, mode);
      await page.getByRole("button", { name: "Pair with a Deck" }).click();
      await page.getByLabel("Pairing link or invite line").fill(paired.pairLink);
      await page.getByRole("button", { name: "Pair", exact: true }).click();
      await expect(page.locator(".project")).toHaveText("deck");
      await sidebar(page).getByRole("button", { name: "Usage" }).click();
      await expect(page.locator('[data-plate="fuji"]')).toBeVisible();
      await page.waitForTimeout(400);
      await shot(page, `${mode}-p07-usage-empty`);
      await sidebar(page).getByRole("button", { name: /teammate|Team/ }).first().click();
      await expect(page.locator('[data-plate="wave"]')).toBeVisible();
      await page.waitForTimeout(400);
      await shot(page, `${mode}-p08-team-empty`);
      // Answer the one approval, and the inbox is empty.
      await sidebar(page).getByRole("button", { name: /^Inbox/ }).click();
      await page.getByRole("button", { name: "Approve", exact: true }).click();
      await expect(page.locator('[data-plate="sea"]')).toBeVisible();
      await page.waitForTimeout(400);
      await shot(page, `${mode}-p09-inbox-empty`);
    } finally {
      await paired.close();
    }
  });

  test(`small ${mode}`, async ({ page }) => {
    await page.setViewportSize({ width: 960, height: 600 });
    await setup(page, mode);
    await expect(page.locator('.agent-clis [data-found="true"]').first()).toBeVisible();
    await page.waitForTimeout(400);
    await shot(page, `${mode}-s1-welcome`);
    await page.getByRole("button", { name: "Pair with a Deck" }).click();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-s2-pair`);
    await page.goto("/");
    await page.getByRole("button", { name: "Open a folder" }).click();
    await expect(page.locator(".link")).toHaveText("Connected");
    await sidebar(page).getByRole("button", { name: "Usage" }).click();
    await expect(page.locator('[data-usage] [data-tile="Tokens"]')).toBeVisible();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-s3-usage`);
    await page.keyboard.press("Control+,");
    await page.getByRole("button", { name: "Appearance", exact: true }).click();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-s4-settings-appearance`);
  });
}
