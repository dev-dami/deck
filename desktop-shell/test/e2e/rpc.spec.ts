import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// The renderer over the rpc transport: "Open a folder" starts the fake deck
// binary, which hands over a fake gRPC server's handshake.
let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;
// Folders the picker hands back, in turn, before dir.
let picks: string[] = [];

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  picks = [];
  host = await attachBridge(page, async () => picks.shift() ?? dir);
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

async function openFolder(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Add project" }).click();
  // A folder is named after itself.
  await expect(page.locator(".project")).toHaveText(basename(dir));
  // The sidebar names the project, never an address.
  await expect(page.locator(".link")).toHaveText("Connected");
}

test("opens a folder and runs two agents in one thread", async ({ page }) => {
  await openFolder(page);
  await expect(sidebar(page).locator(".session")).toHaveCount(3);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
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
  await page.locator(".mode-picker").click();
  await expect(page.getByRole("radio", { name: /Yolo/ })).toBeVisible();
});

test("a session in the background updates live", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Docs pass", exact: true }).click();
  await expect(page.locator(".title")).toHaveText("Docs pass");
  // Another UI (the terminal) writes to the main session meanwhile.
  fake.deck.invoke("chat.send", { session_id: "ses_main", text: "$codex check the limiter tests" });
  const row = sidebar(page).locator('[data-session="ses_main"]');
  // T3's thread statuses: Working while the agent writes, Done once it
  // finished in the background.
  await expect(row.locator('[data-status="working"]')).toBeVisible();
  await expect(row.locator('[data-status="done"]')).toBeVisible();
  await expect(row.locator('[data-status="working"]')).toHaveCount(0);
  const lists = fake.calls.filter((m) => m === "ListMessages").length;
  await row.getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  // The reply streamed in while the session was in the background.
  await expect(page.locator('[data-agent="codex-1"].msg').last()).toContainText("check the limiter tests");
  await expect(row.locator('[data-status="done"]')).toHaveCount(0);
  expect(lists).toBe(1);
});

test("answers an approval and closes the folder", async ({ page }) => {
  await openFolder(page);
  await page.locator(".panel-toggles").getByRole("button", { name: /Approvals/ }).click();
  await page.getByRole("button", { name: "Approve", exact: true }).click();
  await expect(page.getByText("No approvals wait for you.")).toBeVisible();
  expect(fake.deck.answered).toEqual([{ id: "apr_docs", decision: "allow" }]);
  await page.getByRole("button", { name: "Close project" }).click();
  await expect(page.getByRole("heading", { name: "What should we work on?" })).toBeVisible();
  await expect(page.getByRole("list", { name: "Recent folders" })).toContainText(dir);
});

test("git: switches and makes a branch, commits, pushes and opens a pull request", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  // The branch sits in the composer's strip; the next git step in the header.
  const branch = page.locator(".branch-picker");
  await expect(branch).toContainText("main");
  await branch.click();
  await page.getByRole("dialog", { name: "Branches" }).getByRole("menuitem", { name: /feature\/limiter/ }).click();
  await expect(branch).toContainText("feature/limiter");
  await branch.click();
  await page.getByLabel("Find or create a branch").fill("fix/lockout");
  await page.getByLabel("Find or create a branch").press("Enter");
  await expect(branch).toContainText("fix/lockout");

  const git = page.locator(".git-actions");
  await git.getByRole("button", { name: "Commit" }).click();
  const commit = page.getByRole("dialog", { name: "Commit changes" });
  await commit.getByLabel("Commit message").fill("Key the limiter by address");
  await commit.getByRole("button", { name: "Commit and push" }).click();
  await expect(git.getByRole("button", { name: "Create PR" })).toBeVisible();
  expect(fake.git.commits).toEqual(["Key the limiter by address"]);
  expect(fake.git.pushed).toBe(1);

  await git.getByRole("button", { name: "Create PR" }).click();
  const pr = page.getByRole("dialog", { name: "Create pull request" });
  await expect(pr.getByLabel("Title")).toHaveValue("Fix the pairing lockout");
  await pr.getByRole("button", { name: "Create pull request" }).click();
  await expect(git.getByRole("link", { name: "#7723" })).toBeVisible();
});

test("git: a refused checkout shows git's words", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await page.locator(".branch-picker").click();
  fake.git.branches = ["main"];
  await page.getByRole("dialog", { name: "Branches" }).getByRole("menuitem", { name: /feature\/limiter/ }).click();
  await expect(page.locator(".toast")).toContainText("did not match any file");
});

test("terminal: opens in the drawer and runs what you type", async ({ page }) => {
  await openFolder(page);
  await page.keyboard.press("Control+j");
  const drawer = page.getByRole("region", { name: "Terminal" });
  await expect(drawer.locator(".xterm-rows")).toContainText("fake shell");
  await drawer.locator(".xterm").click();
  await page.keyboard.type("ls");
  await page.keyboard.press("Enter");
  await expect(drawer.locator(".xterm-rows")).toContainText("ran: ls");
  // A second tab is a second shell.
  await drawer.getByRole("button", { name: "New terminal" }).click();
  await expect(drawer.getByRole("tab")).toHaveCount(2);
  await drawer.getByRole("button", { name: "Hide the terminal" }).click();
  await expect(drawer).toBeHidden();
});

test("usage: the page shows totals, agents, models and limits", async ({ page }) => {
  await openFolder(page);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Usage" }).click();
  const usage = page.locator("[data-usage]");
  await expect(usage.locator('[data-tile="Tokens"]')).toContainText("1.6M");
  await expect(usage.locator('[data-tile="Cost"]')).toContainText("$18.42");
  await expect(usage).toContainText("opus");
  await expect(usage).toContainText("64% used");
  await usage.getByRole("radio", { name: "7 days" }).click();
  await usage.getByRole("button", { name: "Table" }).click();
  await expect(usage.locator("table").first().locator("tbody tr")).toHaveCount(7);
  await usage.getByRole("button", { name: /Fix the pairing lockout/ }).click();
  await expect(page.locator(".title")).toHaveText("Fix the pairing lockout");
});

test("projects: two folders open at once, their threads in one sidebar", async ({ page }) => {
  const second = mkdtempSync(join(tmpdir(), "deck-second-"));
  await openFolder(page);
  picks = [second];
  await expect(sidebar(page).locator(".session")).toHaveCount(3);
  await sidebar(page).getByRole("button", { name: "Add project" }).click();
  // The new project opens on its new-thread screen; both projects' threads list.
  await expect(page.locator(".title")).toHaveText("New thread");
  await expect(page.locator(".project")).toHaveText(basename(second));
  await expect(sidebar(page).locator(".session")).toHaveCount(6);
  // A thread of the first project makes it current again.
  const first = sidebar(page).locator(".session").filter({ hasText: basename(dir) }).filter({ hasText: "Docs pass" });
  await first.getByRole("button", { name: "Docs pass", exact: true }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await expect(page.locator(".title")).toHaveText("Docs pass");
  // The filter shows one project; closing one leaves the other.
  await sidebar(page).getByRole("button", { name: "Projects" }).click();
  await page.getByRole("menu", { name: "Projects" }).getByRole("button", { name: `Close ${basename(second)}` }).click();
  await expect(sidebar(page).locator(".session")).toHaveCount(3);
});
