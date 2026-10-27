import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// Agents editing the same files (the engine's edit gate): the thread
// "Shared files" holds two agents' edits of one checkout and the asks
// between them, as one line each in the conversation; the Work Graph shows
// who owns which file now, who waits on whom, and each file's timeline in
// the History. With SHOTS set it also screenshots them.
const OUT = process.env["SHOTS"] ?? "";
let fake: FakeRpcDeck;
let host: TestHost;
const LIMIT = "pkg/remote/auth/limit.go";
const PRUNE = "pkg/remote/auth/prune.go";

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  fake.crew = {
    agents: [
      { name: "ada", kind: "claude", model: "opus", status: "tool", context_tokens: 61_000, context_window: 200_000 },
      { name: "bob", kind: "codex", model: "gpt-5-codex", status: "working", context_tokens: 30_000, context_window: 272_000 },
    ],
    running: [{ agent: "bob", kind: "codex", name: "Bash", summary: "go test ./pkg/remote/auth", started: Date.now() - 9_000, command: true }],
  };
  fake.board.tasks = [];
  fake.deck.addSharedThread();
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-coord-"));
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

async function openShared(page: Page): Promise<void> {
  await page.addInitScript(() => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: "dark" })));
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).not.toHaveText("");
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Shared files", exact: true }).click();
  await expect(page.locator(".msg-ask").first()).toBeVisible();
}

const shot = async (page: Page, name: string) => {
  if (OUT === "") return;
  mkdirSync(OUT, { recursive: true });
  await page.mouse.move(2, 600);
  await page.screenshot({ path: join(OUT, `${name}.png`) });
};

test("asks between agents read as one line each in the conversation", async ({ page }) => {
  await openShared(page);
  const asks = page.locator(".msg-ask");
  await expect(asks).toHaveCount(2);
  await expect(asks.nth(0)).toHaveAttribute("data-ask", "granted");
  await expect(asks.nth(0)).toHaveText(/@bob\s*asked\s*.*@ada\s*about\s*limit\.go\s*→ ada: go ahead, I'm done with Fail/);
  await expect(asks.nth(1)).toHaveAttribute("data-ask", "pending");
  await expect(asks.nth(1)).toContainText("waiting for an answer");
  // Not an agent's reply: no turn of its own, no raw backticks.
  await expect(page.locator('[data-agent="deck"]')).toHaveCount(0);
  await expect(asks.nth(0)).not.toContainText("`");
  await asks.nth(0).scrollIntoViewIfNeeded();
  await shot(page, "coord-conversation");
});

test("now: who owns each file, who waits, and each file's timeline", async ({ page }) => {
  await openShared(page);
  await page.locator('[data-view="graph"]').click();
  const g = page.getByRole("group", { name: "Work graph" });
  const file = (p: string) => g.locator(`[data-node="file:${p}"]`);
  await expect(file(LIMIT)).toBeVisible();
  // limit.go was handed to bob after ada; ada waits on bob for prune.go.
  await expect(file(LIMIT).locator("[data-file-owner]")).toHaveAttribute("data-file-owner", "bob");
  await expect(file(LIMIT)).toContainText("bob");
  await expect(file(LIMIT)).toContainText("after ada");
  await expect(file(PRUNE).locator("[data-asking]")).toHaveAttribute("data-asking", "ada");
  await expect(file(PRUNE)).toContainText("asks bob");
  await expect(g.locator('[data-edge="asks"]')).toHaveCount(1);
  await expect(g.locator('[data-edge="asks"].graph-flow')).toHaveCount(1);
  // ada's and bob's edits of limit.go (bob's in his hue, as he has it), and
  // bob's of prune.go; ada's ask stands for her edge to it.
  await expect(g.locator('[data-edge="edited"]')).toHaveCount(3);
  await expect(g.locator(".graph-legend")).toContainText("asks for a file");
  await shot(page, "coord-graph-now");

  // A file opens its timeline: the edits, oldest first, and the ask.
  await file(LIMIT).click();
  const card = page.getByRole("region", { name: `${LIMIT} timeline` });
  await expect(card).toBeVisible();
  await expect(card.locator("[data-owner]")).toHaveAttribute("data-owner", "bob");
  const items = card.getByRole("list", { name: `Edits and asks of ${LIMIT}` }).locator("li");
  await expect(items).toHaveCount(3);
  await expect(items.nth(0)).toContainText("ada");
  await expect(items.nth(1)).toContainText("go ahead, I'm done with Fail");
  await expect(items.nth(2)).toContainText("bob");
  await shot(page, "coord-graph-file");
  // An edit opens its diff.
  await items.nth(0).getByRole("button").click();
  const diff = page.getByRole("dialog", { name: "ada edited limit.go" });
  await expect(diff).toBeVisible();
  await expect(diff.locator(".diff")).toContainText("l.byAddr(addr).fail()");
  await shot(page, "coord-graph-diff");
  await page.keyboard.press("Escape");
  await expect(diff).toHaveCount(0);
  await expect(card).toBeVisible();
  // An ask jumps to its line in the conversation.
  await items.nth(1).getByRole("button").click();
  await expect(page.locator(".timeline")).toBeVisible();
  await expect(page.locator('.msg-ask[data-ask="granted"]')).toBeInViewport();
});

test("history: each contested file's edits and asks along a row", async ({ page }) => {
  await openShared(page);
  await page.locator('[data-view="graph"]').click();
  await page.getByRole("tab", { name: "History" }).click();
  const g = page.getByRole("group", { name: "Work history" });
  await expect(g).toBeVisible();
  // Only this thread, so the row is easy to read.
  const filters = page.getByRole("toolbar", { name: "History filters" });
  await filters.getByRole("button", { name: "Threads" }).click();
  const menu = page.getByRole("menu", { name: "Threads" });
  await menu.getByRole("menuitem", { name: "All threads" }).click();
  for (const t of ["Docs pass", "Fix the pairing lockout", "Long history"]) await menu.getByRole("menuitem", { name: t }).click();
  await page.keyboard.press("Escape");
  await expect(g.locator(".history-band")).toHaveCount(2);

  await expect(g.locator('[data-kind="ask"]')).toHaveCount(2);
  await expect(g.locator('[data-kind="ask"][data-state="pending"]')).toHaveCount(1);
  await expect(g.locator('[data-kind="edit"]')).toHaveCount(1);
  await expect(g.locator('[data-edge="requests"]')).toHaveCount(2);
  await expect(g.locator('[data-edge="answers"]')).toHaveCount(1);
  await page.waitForTimeout(400);
  await shot(page, "coord-graph-history");

  // An edit opens its diff; an ask jumps to its line.
  await g.locator('[data-kind="edit"]').getByRole("button").click();
  const diff = page.getByRole("dialog", { name: "bob edited limit.go" });
  await expect(diff.locator(".diff")).toContainText("l.prune(time.Now())");
  await page.keyboard.press("Escape");
  await g.getByRole("button", { name: /^ada asked bob about/ }).click();
  await expect(page.locator(".timeline")).toBeVisible();
  await expect(page.locator('.msg-ask[data-ask="pending"]')).toBeInViewport();
});
