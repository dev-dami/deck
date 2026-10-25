import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The composer over the fake rpc Deck: slash commands, the @ file picker,
// queue or steer, the prompt history and the palette.
let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;

test.beforeEach(async ({ page }) => {
  // Slow enough that an agent is still busy for the second message.
  fake = await FakeRpcDeck.start({ tickMs: 600 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

async function openMain(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
}

test("/plan switches the mode", async ({ page }) => {
  await openMain(page);
  await expect(page.locator(".mode-picker")).toContainText("Ask");
  await composer(page).fill("/pl");
  const picker = page.getByRole("listbox", { name: "Commands" });
  await expect(picker.getByRole("option")).toHaveText([/\/plan/]);
  await composer(page).press("Enter");
  await expect(page.locator(".mode-picker")).toContainText("Plan");
  await expect(composer(page)).toHaveValue("");
  expect(fake.deck.invoke("mode.get", { session_id: "ses_main" })).toEqual({ mode: "plan" });
  // Typed whole, a command runs on Enter too; unknown slash text is a message.
  await composer(page).fill("/ask");
  await page.keyboard.press("Escape");
  await composer(page).press("Enter");
  await expect(page.locator(".mode-picker")).toContainText("Ask");
  await composer(page).fill("/usr/bin is missing");
  await composer(page).press("Enter");
  await expect.poll(() => fake.deck.sent.at(-1)?.text).toBe("/usr/bin is missing");
});

test("@ inserts a changed path", async ({ page }) => {
  await openMain(page);
  await composer(page).fill("look at @lim");
  const picker = page.getByRole("listbox", { name: "Files" });
  await expect(picker.getByRole("option")).toHaveText([/pkg\/remote\/auth\/limit\.go/, /limit_test\.go/]);
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("look at `pkg/remote/auth/limit.go` ");
});

test("Ctrl+Enter steers a busy agent; Enter queues", async ({ page }) => {
  await openMain(page);
  await composer(page).fill("$claude look again");
  await composer(page).press("Enter");
  await expect(page.locator(".send-hint")).toHaveText("Enter queues · Ctrl+Enter steers");
  await composer(page).fill("$claude-1 key it by address");
  await expect(page.getByRole("button", { name: "Send", exact: true })).toHaveAttribute("title", /Queue for the busy agent/);
  await composer(page).press("Control+Enter");
  await expect.poll(() => fake.deck.steered.length).toBe(1);
  expect(fake.calls).toContain("Steer");
  expect(fake.deck.steered[0]).toMatch(/:claude-1$/);

  await composer(page).fill("$claude-1 then prune");
  await composer(page).press("Enter");
  const panel = page.getByRole("complementary", { name: "Side panel" });
  if ((await panel.count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel.getByRole("tab", { name: "Tasks" }).click();
  const queued = page.getByRole("list", { name: "Queue" }).locator(".queued");
  await expect(queued.last()).toContainText("then prune");
  expect(fake.deck.steered).toHaveLength(1);
  // Edit puts the text back in the composer and takes the item out.
  await queued.last().getByRole("button", { name: "Edit" }).click();
  await expect(composer(page)).toHaveValue("$claude-1 then prune");
  await expect(queued).toHaveCount(1);
});

test("↑ in an empty composer recalls the last prompt", async ({ page }) => {
  await openMain(page);
  await composer(page).fill("first prompt");
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("");
  await expect(page.locator(".streaming")).toHaveCount(0);
  await composer(page).fill("second prompt");
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("");
  await composer(page).press("ArrowUp");
  await expect(composer(page)).toHaveValue("second prompt");
  await composer(page).press("ArrowUp");
  await expect(composer(page)).toHaveValue("first prompt");
  await composer(page).press("ArrowDown");
  await expect(composer(page)).toHaveValue("second prompt");
  await composer(page).press("ArrowDown");
  await expect(composer(page)).toHaveValue("");
});

test("the palette finds a room by its messages and lists actions after >", async ({ page }) => {
  await openMain(page);
  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog", { name: "Commands" });
  await palette.getByRole("textbox").fill("valid token");
  const hit = palette.getByRole("option").first();
  await expect(hit).toContainText("Fix the pairing lockout");
  await expect(hit.locator(".palette-line")).toHaveText("I agree. I also checked that a valid token is never blocked.");
  await palette.getByRole("textbox").fill("> open folder");
  await expect(palette.getByRole("option")).toHaveCount(0);
  await palette.getByRole("textbox").fill("> add project");
  await expect(palette.getByRole("option")).toHaveText([/Add project…\s*Ctrl O/]);
});
