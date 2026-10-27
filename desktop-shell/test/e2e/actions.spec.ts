import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { crewAtWork, FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// Tool calls fold into one actions row per run (actions.ts), and the Work
// Graph's History maps every thread's work. The thread "Busy crew" is
// stored as Deck stores turns: results in messages of their own, two agents
// interleaved, a failed call and a turn still at work. With SHOTS set it
// also screenshots both.
const OUT = process.env["SHOTS"] ?? "";
let fake: FakeRpcDeck;
let host: TestHost;

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  crewAtWork(fake);
  fake.deck.addBusyThread();
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-actions-"));
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

async function openBusy(page: Page): Promise<void> {
  await page.addInitScript(() => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: "dark" })));
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).not.toHaveText("");
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Busy crew", exact: true }).click();
  await expect(page.locator('[data-agent="bob"].msg').first()).toBeVisible();
}

const shot = async (page: Page, name: string) => {
  if (OUT !== "") await page.screenshot({ path: join(OUT, `${name}.png`) });
};

test("tool calls fold into one actions row per run", async ({ page }) => {
  await openBusy(page);
  // No result message shows as a reply of its own, and no call shows until
  // its row opens.
  await expect(page.locator('[data-agent="agent"]')).toHaveCount(0);
  await expect(page.locator("details.tool")).toHaveCount(0);
  const heads = page.locator(".actions-head");
  // ada's turn still at work: a spinner and the call that runs.
  await expect(page.locator('.actions-group[data-state="running"]')).toHaveCount(1);
  await expect(page.locator('.actions-group[data-state="running"] .actions-head')).toContainText("@ada");
  await expect(page.locator('.actions-group[data-state="running"] .actions-now')).toContainText("go test ./pkg/remote/auth");
  // The finished turns fold behind "Worked for …"; open, each run is one row:
  // ada's first step, bob's read between, ada's fix,
  // bob's review and ada's running prune.
  const worked = page.locator(".worked");
  await expect(worked).toHaveCount(4);
  for (let i = 0; i < 4; i++) await worked.nth(i).click();
  await expect(heads).toHaveCount(5);
  await expect(heads.nth(0)).toContainText("5 actions");
  await expect(heads.nth(0)).toContainText("read 3 files, ran 1 command, searched 1 time");
  await expect(heads.nth(0).locator(".actions-failed")).toHaveText("1 failed");
  await expect(heads.nth(1)).toContainText("@bob");
  await expect(heads.nth(2)).toContainText("ran 2 commands, edited 2 files");
  await shot(page, "timeline-collapsed");

  // Open: every call, each of which opens to its output; the state holds.
  await heads.first().click();
  const group = page.locator(".actions-group").first();
  await expect(group.locator("details.tool")).toHaveCount(5);
  await group.locator("details.tool.failed summary").click();
  await expect(group.locator("details.tool.failed .tool-output")).toContainText("FAIL");
  await group.evaluate((el) => {
    el.scrollIntoView({ block: "start" });
    el.closest(".timeline")?.scrollBy(0, -48);
  });
  await page.mouse.move(2, 600);
  await shot(page, "timeline-expanded");
  // To the graph and back: the fold and the group are still open.
  await page.locator('[data-view="graph"]').click();
  await page.locator('[data-view="conversation"]').click();
  await expect(page.locator(".actions-group").first().locator("details.tool")).toHaveCount(5);
});

test("the graph's History maps every thread's work and jumps to it", async ({ page }) => {
  await openBusy(page);
  await page.locator('[data-view="graph"]').click();
  await page.getByRole("tab", { name: "History" }).click();
  const g = page.getByRole("group", { name: "Work history" });
  await expect(g).toBeVisible();
  // Every thread is a band, with the files under them.
  await expect(g.locator(".history-band")).toHaveCount(5);
  // Busy crew: ada's and bob's runs, the handoff for review and back, and
  // the files they read and edited; ada's prune still runs.
  await expect(g.locator('[data-kind="actions"]')).toHaveCount(6);
  await expect(g.locator('[data-kind="actions"][data-state="running"]')).toHaveCount(1);
  await expect(g.locator('[data-edge="reviews"]')).toHaveCount(1);
  await expect(g.locator('[data-edge="handoff"]')).toHaveCount(1);
  await expect(g.locator('[data-node="f:pkg/remote/auth/limit.go"]')).toBeVisible();
  await page.mouse.move(2, 600);
  await shot(page, "graph-history-all");

  // Filters: one thread, then no files.
  const filters = page.getByRole("toolbar", { name: "History filters" });
  await filters.getByRole("button", { name: "Threads" }).click();
  await page.getByRole("menu", { name: "Threads" }).getByRole("menuitem", { name: "All threads" }).click();
  for (const t of ["Docs pass", "Fix the pairing lockout", "Long history"]) {
    await page.getByRole("menu", { name: "Threads" }).getByRole("menuitem", { name: t }).click();
  }
  await page.keyboard.press("Escape");
  await expect(g.locator(".history-band")).toHaveCount(2);
  // A run opens in place to its calls.
  const run = g.locator('[data-kind="actions"]').first();
  await run.getByRole("button", { name: "Show the actions" }).click();
  await expect(run.getByRole("list", { name: "Actions" }).locator("li")).toHaveCount(5);
  await page.waitForTimeout(400);
  await page.mouse.move(2, 600);
  await shot(page, "graph-history-thread");
  await filters.getByRole("button", { name: "Files" }).click();
  await expect(g.locator('[data-kind="file"]')).toHaveCount(0);

  // A click jumps to that point of the conversation.
  await g.getByRole("button", { name: /^bob: Looks right/ }).click();
  await expect(page.locator(".timeline")).toBeVisible();
  await expect(page.locator('[data-agent="bob"].msg', { hasText: "Looks right" })).toBeInViewport();
});
