import { mkdirSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { crewAtWork, FakeRpcDeck, workAtWork } from "../fake-grpc";
import { attachBridge } from "./harness";

// Worktrees and capsules in the Work tab and the Work Graph, over the
// crew of "Fix the pairing lockout": an agent's worktree, one landed, a
// long refactor behind main that conflicts with it, and that refactor as a
// capsule of 640 files. With SHOTS set it also screenshots the list, a
// capsule's diff, a merge with a conflict and the graph, dark and light.
const OUT = process.env["SHOTS"] ?? "";
let fake: FakeRpcDeck;
let host: TestHost;

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  crewAtWork(fake);
  workAtWork(fake);
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-work-"));
  host = await attachBridge(page, async () => dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const panel = (page: Page) => page.getByRole("complementary", { name: "Side panel" });
const work = (page: Page) => panel(page).getByRole("region", { name: "Work" });
const row = (page: Page, name: string) => work(page).locator(`[data-tree="${name}"]`);

async function openWork(page: Page, mode: "dark" | "light" = "dark"): Promise<void> {
  await page.emulateMedia({ colorScheme: mode });
  await page.addInitScript((m) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m })), mode);
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).not.toHaveText("");
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  if ((await panel(page).count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel(page).getByRole("tab", { name: "Work" }).click();
  await expect(row(page, "store-refactor")).toBeVisible();
}

const shot = async (page: Page, name: string) => {
  if (OUT === "") return;
  mkdirSync(OUT, { recursive: true });
  await page.mouse.move(2, 600);
  await page.screenshot({ path: join(OUT, `${name}.png`) });
};

test("lists worktrees and capsules with where each stands", async ({ page }) => {
  await openWork(page);
  await expect(work(page).locator("[data-tree]")).toHaveCount(3);
  await expect(row(page, "t2")).toHaveAttribute("data-state", "working");
  await expect(row(page, "t3")).toHaveAttribute("data-state", "landed");
  await expect(row(page, "store-refactor")).toHaveAttribute("data-state", "behind");
  await expect(row(page, "store-refactor").locator(".tree-divergence")).toHaveText("↑3 ↓4");
  await expect(row(page, "t2")).toContainText("claude-sub · t2 · from main");
  await expect(work(page).locator("[data-capsule]")).toHaveCount(3);
  await expect(work(page).locator('[data-capsule="store-refactor"]')).toContainText("v2");
  await expect(work(page).locator('[data-capsule="store-refactor"] .capsule-tests')).toHaveText("tests fail");
  // The lane of the agent on its own worktree wears its branch.
  await expect(page.locator('[data-lane="agent:claude-sub"] .lane-branch')).toHaveText("t2");
  await shot(page, "work-list-dark");
});

test("a sync that conflicts says which files and changes nothing", async ({ page }) => {
  await openWork(page);
  await row(page, "store-refactor").getByRole("button", { expanded: false }).click();
  await row(page, "store-refactor").getByRole("button", { name: "Sync" }).click();
  const alert = row(page, "store-refactor").locator(".tree-conflict");
  await expect(alert).toContainText("Sync stopped: 1 file conflict with main");
  await expect(alert).toContainText("internal/store/store.go");
  expect(fake.lines.asked).toContain("sync store-refactor");
});

test("merging checks every file first and waits on a conflict", async ({ page }) => {
  await openWork(page);
  await row(page, "store-refactor").getByRole("button", { expanded: false }).click();
  await row(page, "store-refactor").getByRole("button", { name: "Merge into main" }).click();
  const dialog = page.getByRole("dialog", { name: "Merge store-refactor into the shared tree" });
  await expect(dialog.locator(".merge-conflicts")).toContainText("1 file changed on both sides");
  await expect(dialog.locator('[data-land="conflict"]')).toContainText("both sides changed the same lines");
  await expect(dialog.locator('[data-land-count="fits"]')).toHaveText("2 applies");
  await expect(dialog.locator('[data-land-count="merge"]')).toHaveText("1 merges");
  await expect(dialog.getByRole("button", { name: "Merge" })).toBeDisabled();
  expect(fake.lines.asked).toContain("check store-refactor");
  await shot(page, "work-merge-conflict-dark");
  await dialog.getByRole("radio", { name: "Keep the shared tree's" }).click();
  await dialog.getByRole("button", { name: "Merge" }).click();
  await expect(dialog).toBeHidden();
  expect(fake.lines.asked).toContain("merge store-refactor ours");
  await expect(work(page).locator('[data-capsule="store-refactor"]')).toHaveAttribute("data-state", "landed");
});

test("a capsule pages through its files, opens a diff and goes to an agent", async ({ page }) => {
  await openWork(page);
  await work(page).locator('[data-capsule="store-refactor"] button').click();
  const detail = panel(page).getByRole("region", { name: "Capsule store-refactor" });
  await expect(detail).toContainText("100 of 640 files");
  await expect(detail.locator(".capsule-file")).toHaveCount(100);
  await expect(detail.locator(".capsule-commits li")).toHaveCount(3);
  await expect(detail.locator(".capsule-comment")).toContainText("holds the lock");
  await detail.getByRole("button", { name: "Show 100 more files" }).click();
  await expect(detail).toContainText("200 of 640 files");
  const file = detail.locator(".capsule-file").first();
  await file.getByRole("button").click();
  await expect(file.locator(".diff-row, [role=row]").first()).toBeVisible();
  await expect(file).toContainText("func Open(ctx context.Context) {}");
  await shot(page, "work-capsule-diff-dark");
  await detail.getByRole("combobox", { name: "Hand to agent" }).selectOption("codex-1");
  await detail.getByRole("button", { name: "Hand it over" }).click();
  await expect.poll(() => fake.lines.asked).toContain("hand store-refactor codex-1");
  await detail.getByRole("button", { name: "Tests pass" }).click();
  await expect.poll(() => fake.lines.asked).toContain("review store-refactor pass");
  await detail.getByRole("button", { name: "Back to the work" }).click();
  await expect(row(page, "t2")).toBeVisible();
});

test("the graph links agents to their worktrees, capsules and merges", async ({ page }) => {
  await openWork(page);
  await panel(page).getByRole("tab", { name: "Graph" }).click();
  const g = page.getByRole("group", { name: "Work graph" });
  await expect(g.locator('[data-kind="branch"]')).toHaveCount(3);
  await expect(g.locator('[data-kind="capsule"]')).toHaveCount(3);
  await expect(g.locator('[data-kind="main"]')).toHaveCount(1);
  await expect(g.locator('[data-edge="works"]')).toHaveCount(2);
  await expect(g.locator('[data-edge="bundles"]')).toHaveCount(2);
  await expect(g.locator('[data-edge="lands"]')).toHaveCount(1);
  await shot(page, "work-graph-dark");
  await g.locator('[data-node="branch:store-refactor"]').click();
  await expect(panel(page).getByRole("tab", { name: "Work" })).toHaveAttribute("aria-selected", "true");
});

test("screenshots in light", async ({ page }) => {
  test.skip(OUT === "", "SHOTS is not set");
  await openWork(page, "light");
  await shot(page, "work-list-light");
  await row(page, "store-refactor").getByRole("button", { expanded: false }).click();
  await row(page, "store-refactor").getByRole("button", { name: "Merge into main" }).click();
  const dialog = page.getByRole("dialog", { name: "Merge store-refactor into the shared tree" });
  await expect(dialog.locator(".merge-conflicts")).toBeVisible();
  await shot(page, "work-merge-conflict-light");
  await dialog.getByRole("button", { name: "Cancel" }).click();
  await work(page).locator('[data-capsule="store-refactor"] button').click();
  const detail = panel(page).getByRole("region", { name: "Capsule store-refactor" });
  await detail.locator(".capsule-file").first().getByRole("button").click();
  await expect(detail.locator(".capsule-file").first()).toContainText("func Open(ctx context.Context) {}");
  await shot(page, "work-capsule-diff-light");
  await detail.getByRole("button", { name: "Back to the work" }).click();
  await panel(page).getByRole("tab", { name: "Graph" }).click();
  await expect(page.getByRole("group", { name: "Work graph" }).locator('[data-kind="branch"]')).toHaveCount(3);
  await shot(page, "work-graph-light");
});
