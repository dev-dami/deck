import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { crewAtWork, FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The Browser tab of the side panel over a fake engine that serves
// BrowserState and BrowserAction: the strip of the room's tabs, what an
// agent is doing, Take control and Release. The pages themselves are
// native views; the real ones are checked by test/browser-integration.cjs.
const SID = "ses_main";
let fake: FakeRpcDeck;
let host: TestHost;

const tab = (id: string, owner: string, over: Record<string, unknown> = {}) => ({
  id,
  session_id: SID,
  owner,
  url: "https://example.com/",
  title: "",
  loading: false,
  can_back: false,
  can_forward: false,
  user_in_control: owner === "",
  snapshot_version: "",
  activity: "",
  asleep: false,
  ...over,
});

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 400 });
  crewAtWork(fake);
  fake.serveBrowser([
    tab("t-docs", "claude-1", { title: "Rate limiting - Guide", url: "https://docs.example.test/limits" }),
    tab("t-shop", "codex-1", { title: "Keyboards - Corner Shop", url: "https://shop.example.test/keyboards", asleep: true }),
    tab("t-app", "", { title: "Pairing lockout", url: "http://localhost:5173/" }),
  ]);
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-browser-"));
  host = await attachBridge(page, async () => dir, { browserView: async () => {} });
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const panel = (page: Page) => page.getByRole("complementary", { name: "Side panel" });
const browser = (page: Page) => panel(page).getByRole("region", { name: "Browser" });

async function openBrowser(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).not.toHaveText("");
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  if ((await panel(page).count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel(page).getByRole("tab", { name: "Browser", exact: true }).click();
  await expect(browser(page)).toBeVisible();
}

test("the strip has a tab for each page: the agents' with their marks, the person's with a globe", async ({ page }) => {
  await openBrowser(page);
  const strip = browser(page).getByRole("tablist", { name: "Open pages" });
  await expect(strip.getByRole("tab")).toHaveCount(3);
  await expect(strip.locator('[data-tab="t-docs"]')).toHaveAttribute("data-owner", "claude-1");
  await expect(strip.locator('[data-tab="t-app"]')).toHaveAttribute("data-owner", "you");
  // A page let go to save memory is marked, and keeps its address.
  await expect(strip.locator('[data-tab="t-shop"]')).toHaveAttribute("data-asleep", "");
  await strip.getByRole("tab", { name: "Keyboards - Corner Shop" }).click();
  await expect(browser(page).getByRole("textbox", { name: "Address" })).toHaveValue("https://shop.example.test/keyboards");
});

test("a working agent's tab pulses and the bar says what it is doing", async ({ page }) => {
  await openBrowser(page);
  const strip = browser(page).getByRole("tablist", { name: "Open pages" });
  const docs = strip.locator('[data-tab="t-docs"]');
  await strip.getByRole("tab", { name: "Rate limiting - Guide" }).click();
  fake.setTabs(SID, [tab("t-docs", "claude-1", { title: "Rate limiting - Guide", url: "https://docs.example.test/limits", activity: "clicking e12" })]);
  await expect(docs).toHaveAttribute("data-working", "");
  await expect(docs.getByRole("tab")).toHaveAttribute("title", "@claude-1 · clicking e12");
  // The tab keeps its name for assistive technology while it works.
  await expect(strip.getByRole("tab", { name: "Rate limiting - Guide" })).toBeVisible();
  await expect(browser(page).getByRole("status")).toContainText("@claude-1 · clicking e12");
  fake.setTabs(SID, [tab("t-docs", "claude-1", { title: "Rate limiting - Guide", url: "https://docs.example.test/limits" })]);
  await expect(docs).not.toHaveAttribute("data-working", "");
  await expect(browser(page).getByRole("status")).toContainText("claude-1 is driving");
});

test("Take control stops the agent and Release hands the tab back", async ({ page }) => {
  await openBrowser(page);
  const strip = browser(page).getByRole("tablist", { name: "Open pages" });
  await strip.getByRole("tab", { name: "Rate limiting - Guide" }).click();
  await browser(page).getByRole("button", { name: "Take control", exact: true }).click();
  await expect(browser(page).getByRole("button", { name: "Release", exact: true })).toBeVisible();
  await expect(browser(page).getByRole("status")).toContainText("You are in control");
  expect(fake.browser.asked).toContain("take_control t-docs");
  await browser(page).getByRole("button", { name: "Release", exact: true }).click();
  await expect(browser(page).getByRole("button", { name: "Take control", exact: true })).toBeVisible();
  expect(fake.browser.asked).toContain("release_control t-docs");
});

test("the person's own tab has no agent bar, and closing a tab drops it", async ({ page }) => {
  await openBrowser(page);
  const strip = browser(page).getByRole("tablist", { name: "Open pages" });
  await strip.getByRole("tab", { name: "Pairing lockout" }).click();
  await expect(browser(page).getByRole("status")).toHaveCount(0);
  await strip.getByRole("button", { name: "Close Keyboards - Corner Shop" }).click();
  await expect(strip.getByRole("tab")).toHaveCount(2);
  expect(fake.browser.asked).toContain("close t-shop");
});

test("with no tab open the browser says so", async ({ page }) => {
  await openBrowser(page);
  fake.setTabs(SID, []);
  await expect(browser(page).getByText("No page is open.")).toBeVisible();
});
