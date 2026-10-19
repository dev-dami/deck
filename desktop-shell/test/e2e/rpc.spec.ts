import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The renderer over the rpc transport: "Open a folder" starts the fake deck
// binary, which hands over a fake gRPC server's handshake.
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

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Sessions" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

async function openFolder(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder…" }).click();
  await expect(page.locator(".project")).toHaveText("deck");
  await expect(page.locator(".where")).toContainText(dir);
}

test("opens a folder and runs two agents in one thread", async ({ page }) => {
  await openFolder(page);
  await expect(sidebar(page).locator(".session")).toHaveCount(3);
  await sidebar(page).getByRole("button", { name: /Fix the pairing lockout/ }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  await composer(page).fill("$claude $codex+ split the work");
  await composer(page).press("Enter");
  const claude = page.locator('[data-agent="claude-1"].msg').last();
  const codex = page.locator('[data-agent="codex-2"].msg').last();
  await expect(claude.locator(".streaming")).toBeVisible();
  await expect(codex.locator(".streaming")).toBeVisible();
  await expect(page.locator('.agent[data-agent="codex-2"]')).toBeVisible();
  await expect(claude).toContainText("split the work");
  await expect(codex).toContainText("split the work");
  await expect(page.locator(".streaming")).toHaveCount(0);
  // Yolo is offered: the rpc server allows it for the person at this machine.
  await expect(page.getByRole("radio", { name: "Yolo" })).toBeVisible();
});

test("a session in the background updates live", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: /Docs pass/ }).click();
  await expect(page.locator(".title")).toHaveText("Docs pass");
  // Another UI (the terminal) writes to the main session meanwhile.
  fake.deck.invoke("chat.send", { session_id: "ses_main", text: "$codex check the limiter tests" });
  const row = sidebar(page).locator('[data-session="ses_main"]');
  await expect(row.locator(".dot.busy")).toBeVisible();
  await expect(row.locator(".dot.unread")).toBeVisible();
  await expect(row.locator(".dot.busy")).toHaveCount(0);
  const lists = fake.calls.filter((m) => m === "ListMessages").length;
  await row.getByRole("button", { name: /Fix the pairing lockout/ }).click();
  // The reply streamed in while the session was in the background.
  await expect(page.locator('[data-agent="codex-1"].msg').last()).toContainText("check the limiter tests");
  await expect(row.locator(".dot.unread")).toHaveCount(0);
  expect(lists).toBe(1);
});

test("answers an approval and closes the folder", async ({ page }) => {
  await openFolder(page);
  await page.locator(".panel-toggles").getByRole("button", { name: /Approvals/ }).click();
  await page.getByRole("button", { name: "Allow", exact: true }).click();
  await expect(page.getByText("No approvals wait for you.")).toBeVisible();
  expect(fake.deck.answered).toEqual([{ id: "apr_docs", decision: "allow" }]);
  await page.getByRole("button", { name: "Switch Deck" }).click();
  await expect(page.getByRole("heading", { name: "Connect to Deck" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Recent folders" })).toContainText(dir);
});
