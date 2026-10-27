import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// A claude told to "spin up sub agents" starts four reviewers with its CLI's
// own Task tool (test/fake-server.ts): Deck shows each as a lane under it, as
// a block of its own work in the conversation, and as a node under it in the
// Work Graph. DECK_SHOTS names a folder for the screenshots.
let fake: FakeRpcDeck;
let host: TestHost;
const OUT = process.env["DECK_SHOTS"] ?? join(tmpdir(), "deck-shots");
const shot = (name: string) => {
  mkdirSync(OUT, { recursive: true });
  return join(OUT, name);
};

// Wide enough for the room's lanes on one line, ended reviewers too.
test.use({ viewport: { width: 1680, height: 900 } });

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-subagents-"));
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const lanes = (page: Page) => page.getByRole("toolbar", { name: "Lanes" });
const SUBS = [293, 294, 295, 296].map((n) => `claude-1/review-pr-${n}`);

async function startReviews(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  const box = page.getByRole("textbox", { name: "Message" });
  await box.fill("$claude spin up sub agents to review each of them");
  await box.press("Enter");
}

test("a claude's own subagents: lanes under it, nodes in the graph, blocks of their work", async ({ page }) => {
  await startReviews(page);

  // Four lanes right after claude's.
  const strip = lanes(page);
  for (const name of SUBS) await expect(strip.locator(`[data-lane="agent:${name}"]`)).toBeVisible({ timeout: 15_000 });
  const ids = await strip.locator("[data-lane]").evaluateAll((els) => els.map((e) => (e as HTMLElement).dataset["lane"]));
  const at = ids.indexOf("agent:claude-1");
  expect(ids.slice(at + 1, at + 5)).toEqual(SUBS.map((n) => `agent:${n}`));
  await expect(strip.locator(`[data-lane="agent:${SUBS[2]}"] .lane-name`)).toHaveText("review-pr-295");

  // They end their way: two done, #296 failed, #295 still at work.
  await expect(strip.locator(`[data-lane="agent:${SUBS[0]}"]`)).toHaveAttribute("data-sub", "done", { timeout: 15_000 });
  await expect(strip.locator(`[data-lane="agent:${SUBS[1]}"]`)).toHaveAttribute("data-sub", "done", { timeout: 15_000 });
  await expect(strip.locator(`[data-lane="agent:${SUBS[3]}"]`)).toHaveAttribute("data-sub", "failed", { timeout: 15_000 });
  await expect(strip.locator(`[data-lane="agent:${SUBS[2]}"]`)).toHaveAttribute("data-sub", "running");
  await page.locator(".lanes").screenshot({ path: shot("subagents-lanes.png") });

  // The Work Graph's Now: each reviewer a node under claude, the legend
  // counts them apart, and the running one names its call at work.
  await page.getByRole("tablist", { name: "View" }).getByRole("tab", { name: "Graph" }).click();
  const g = page.getByRole("group", { name: "Work graph" });
  for (const name of SUBS) await expect(g.locator(`[data-node="agent:${name}"]`)).toBeVisible();
  await expect(g.locator(".graph-legend")).toContainText("3 agents · 4 sub-agents");
  await expect(g.locator(`[data-node="agent:${SUBS[3]}"]`)).toContainText("failed");
  await expect(g.locator(`[data-node="agent:${SUBS[0]}"]`)).toContainText("Review PR #293 · done");
  await expect(g.locator(`[data-node="agent:${SUBS[2]}"]`)).toContainText("Grep");
  await page.waitForTimeout(600); // the layout settles
  await g.screenshot({ path: shot("subagents-graph-now.png") });

  // History: each reviewer is a lane of its own under claude's.
  await page.getByRole("tablist", { name: "Graph scope" }).getByRole("tab", { name: "History" }).click();
  await expect(page.locator(".hist-text").filter({ hasText: "↳ review-pr-296" })).toHaveCount(1);
  await page.waitForTimeout(400);
  await page.screenshot({ path: shot("subagents-graph-history.png") });
  await page.getByRole("tablist", { name: "Graph scope" }).getByRole("tab", { name: "Now" }).click();
  await page.getByRole("tablist", { name: "View" }).getByRole("tab", { name: "Conversation" }).click();

  // The conversation: claude's words stay its own, and each reviewer's work
  // is one block under them, with its actions and how it stands.
  const claude = page.locator('.msg-agent[data-agent="claude-1"]').filter({ hasText: "The #296 reviewer stopped" });
  await expect(claude).toBeVisible({ timeout: 15_000 });
  await expect(page.locator(".msg-sub")).toHaveCount(4);
  // The blocks follow claude's reply, in the order the reviewers started.
  const order = await page.locator(".msg-agent, .msg-sub").evaluateAll((els) => els.slice(-5).map((e) => (e as HTMLElement).dataset["agent"]));
  expect(order).toEqual(["claude-1", ...SUBS]);
  const failed = page.locator(`.msg-sub[data-agent="${SUBS[3]}"]`);
  await expect(failed).toHaveAttribute("data-state", "failed");
  await expect(failed.locator(".sub-count")).toHaveText("3 actions");
  await expect(failed.locator(".sub-task")).toHaveText("Review PR #296");
  await expect(page.locator(`.msg-sub[data-agent="${SUBS[0]}"]`)).toHaveAttribute("data-state", "done");
  // The one at work names its call.
  await expect(page.locator(`.msg-sub[data-agent="${SUBS[2]}"] .sub-now`)).toContainText("TODO");
  // A block opens to its words and calls.
  await failed.locator(".sub-head").click();
  await expect(failed.locator(".sub-error")).toHaveText("Agent stopped: API Error: 529 overloaded");
  await expect(failed.locator(".actions-group")).toHaveCount(1);
  await failed.scrollIntoViewIfNeeded();
  await page.screenshot({ path: shot("subagents-conversation.png") });

  // A sub-lane shows that reviewer's work alone, open; its popover says it
  // answers to claude, and Stop is claude's.
  await strip.locator(`[data-lane="agent:${SUBS[2]}"]`).click();
  await expect(page.locator(".msg-sub")).toHaveCount(1);
  await expect(page.locator(`.msg-sub[data-agent="${SUBS[2]}"] .sub-log`)).toBeVisible();
  await expect(page.locator('.msg-agent[data-agent="claude-1"]')).toHaveCount(0);
  const pop = page.getByRole("menu", { name: "review-pr-295" });
  await expect(pop.locator(".sub-task")).toHaveText("Review PR #295");
  await page.screenshot({ path: shot("subagents-lane-alone.png") });
  await pop.getByRole("menuitem", { name: "Stop @claude-1 and its subagents" }).click();
  await expect.poll(() => fake.deck.interrupted).toContain("claude-1");
  // Its subagents stopped with it.
  await expect(strip.locator(`[data-lane="agent:${SUBS[2]}"]`)).toHaveAttribute("data-sub", "stopped", { timeout: 15_000 });
  await expect(page.locator(`.msg-sub[data-agent="${SUBS[2]}"]`)).toHaveAttribute("data-state", "stopped");
  await strip.locator(`[data-lane="agent:${SUBS[2]}"]`).click();
  await page.getByRole("menu", { name: "review-pr-295" }).getByRole("menuitem", { name: "Started by @claude-1" }).click();
  await expect(strip.locator('[data-lane="agent:claude-1"]')).toHaveAttribute("aria-pressed", "true");
});
