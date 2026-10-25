import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The room's verbs over the fake rpc Deck: a plan card and a line comment.
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

const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

async function openMain(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
}

const PLAN = `I read the limiter. The count is global.

## Plan

1. Key the failure count by address in \`limit.go\`.
2. Prune quiet addresses after an hour.
3. Add \`TestLimiterIsPerAddress\`.

I will not touch pairing.`;

test("a plan-mode reply shows a plan card, and Approve switches the mode and sends", async ({ page }) => {
  await openMain(page);
  const mode = page.locator(".mode-picker");
  // The room was yolo before plan, so Approve goes back to yolo.
  fake.deck.invoke("mode.set", { session_id: "ses_main", mode: "yolo" });
  await expect(mode).toContainText("Yolo");
  fake.deck.invoke("mode.set", { session_id: "ses_main", mode: "plan" });
  await expect(mode).toContainText("Plan");
  fake.deck.reply("ses_main", "claude-1", PLAN);

  const card = page.locator(".plan-card");
  await expect(card).toBeVisible();
  await expect(card.locator(".plan-heading")).toHaveText("Plan");
  await expect(card.locator(".plan-steps li")).toHaveCount(3);
  // The prose around the plan stays prose.
  await expect(page.locator('[data-agent="claude-1"].msg').last()).toContainText("I will not touch pairing.");

  await card.getByRole("button", { name: "Approve" }).click();
  await expect(card.locator(".plan-state")).toHaveText("Approved. The agent goes ahead.");
  await expect(mode).toContainText("Yolo");
  expect(fake.deck.invoke("mode.get", { session_id: "ses_main" })).toEqual({ mode: "yolo" });
  expect(fake.deck.sent.at(-1)).toEqual({ session_id: "ses_main", text: "$claude-1 Go ahead with the plan." });
});

test("a numbered list in ask mode stays prose", async ({ page }) => {
  await openMain(page);
  fake.deck.reply("ses_main", "claude-1", PLAN);
  await expect(page.locator('[data-agent="claude-1"].msg').last()).toContainText("Prune quiet addresses");
  await expect(page.locator(".plan-card")).toHaveCount(0);
});

test("a line comment in the diff lands in the composer, addressed to the agent", async ({ page }) => {
  await openMain(page);
  await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  const row = panel.locator(".diff-row.diff-del", { hasText: "global.fail()" });
  await row.hover();
  await row.getByRole("button", { name: "Comment on line 13" }).click();
  const input = panel.getByRole("textbox", { name: "Comment on pkg/remote/auth/limit.go:13" });
  await input.fill("Why was this global?");
  await input.press("Enter");
  await expect(composer(page)).toHaveValue("$claude-1 In `pkg/remote/auth/limit.go:13`:\n> global.fail()\nWhy was this global?");
  await expect(composer(page)).toBeFocused();

  const added = panel.locator(".diff-row.diff-add", { hasText: "l.prune()" });
  await added.hover();
  await added.getByRole("button", { name: "Comment on line 15" }).click();
  await panel.getByRole("textbox", { name: "Comment on pkg/remote/auth/limit.go:15" }).fill("Prune on a timer instead.");
  await panel.getByRole("textbox", { name: "Comment on pkg/remote/auth/limit.go:15" }).press("Enter");
  const badge = panel.locator(".line-comments");
  await expect(badge).toContainText("2 comments");
  await badge.getByRole("button", { name: "Send" }).click();
  await expect(composer(page)).toHaveValue("");
  expect(fake.deck.sent.at(-1)?.text).toBe(
    "$claude-1 In `pkg/remote/auth/limit.go:13`:\n> global.fail()\nWhy was this global?\n\n$claude-1 In `pkg/remote/auth/limit.go:15`:\n> l.prune()\nPrune on a timer instead.",
  );
});

test("after a stop, the files the person changed go back to the agent", async ({ page }) => {
  await openMain(page);
  await composer(page).fill("$claude look again");
  await composer(page).press("Enter");
  await page.getByRole("button", { name: "Stop all agents" }).click();
  // The fake does not stop the stream; the take-over waits until it ends.
  await expect(page.locator(".streaming")).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Send", exact: true })).toBeVisible();
  // The changed files once the agents stand still are the base.
  await page.waitForTimeout(500);
  fake.deck.setChanges("ses_main", [
    { path: "pkg/remote/auth/limit.go", added: 5, removed: 1 },
    { path: "pkg/remote/auth/limit_test.go", added: 12, removed: 0 },
    { path: "README.md", added: 2, removed: 0 },
  ]);
  await page.evaluate(() => window.dispatchEvent(new Event("focus")));
  await expect(page.locator(".takeover")).toHaveText("You changed 2 files since the stop");
  await page.getByRole("button", { name: "Continue with my changes" }).click();
  await expect(page.locator(".takeover")).toHaveCount(0);
  expect(fake.deck.sent.at(-1)?.text).toBe("$claude-1 Continue. I changed: README.md, pkg/remote/auth/limit.go");
});
