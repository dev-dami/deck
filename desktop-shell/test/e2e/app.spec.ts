import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";
import { attachBridge } from "./harness";

let fake: FakeDeck;

test.beforeEach(async ({ page }) => {
  fake = await FakeDeck.start({ tickMs: 250 });
  await attachBridge(page);
});

test.afterEach(async () => {
  await fake.close();
});

async function pair(page: Page): Promise<void> {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "What should we work on?" })).toBeVisible();
  await page.getByLabel("Pairing link or invite line").fill(fake.pairLink);
  await page.getByRole("button", { name: "Pair", exact: true }).click();
  await expect(page.locator(".project")).toHaveText("deck");
}

function sidebar(page: Page) {
  return page.getByRole("navigation", { name: "Threads" });
}

async function openMain(page: Page): Promise<void> {
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator(".title")).toHaveText("Fix the pairing lockout");
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
}

const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

test("connects, lists sessions and opens one", async ({ page }) => {
  await pair(page);
  const nav = sidebar(page);
  await expect(nav.locator(".session")).toHaveCount(3);
  await expect(nav.locator('[data-session="ses_docs"] [data-status="approval"]')).toHaveText("Approval");
  await openMain(page);
  // Markdown renders as elements; a finished reply folds its work behind
  // "Worked for …", and its tool calls start folded too.
  await expect(page.locator(".md strong").first()).toHaveText("every");
  await expect(page.locator("pre.codeblock")).toContainText("l.fail(addr)");
  await expect(page.locator("details.tool")).toHaveCount(0);
  await page.locator(".worked").first().click();
  const tool = page.locator("details.tool").first();
  await expect(tool).not.toHaveAttribute("open", "");
  await tool.locator("summary").click();
  await expect(tool.locator(".tool-output")).toContainText("ok");
  // The roster: two agents with their kind and model.
  await expect(page.locator(".agent")).toHaveCount(2);
});

test("sends a message and the reply streams in", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await composer(page).fill("Please add a test");
  await composer(page).press("Enter");
  await expect(page.locator(".msg-user").last()).toContainText("Please add a test");
  await expect(composer(page)).toHaveValue("");
  const reply = page.locator('[data-agent="claude-1"].msg').last();
  await expect(reply.locator(".streaming")).toBeVisible();
  await expect(reply).toContainText("Please add a test");
  await expect(reply.locator(".streaming")).toHaveCount(0);
  expect(fake.sent.map((s) => s.text)).toEqual(["Please add a test"]);
});

test("two agents stream in one thread at once", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await composer(page).fill("$claude $codex compare the fixes");
  await composer(page).press("Enter");
  const claude = page.locator('[data-agent="claude-1"].msg').last();
  const codex = page.locator('[data-agent="codex-1"].msg').last();
  await expect(claude.locator(".streaming")).toBeVisible();
  await expect(codex.locator(".streaming")).toBeVisible();
  await expect(page.locator(".agent-busy")).toHaveCount(2);
  await expect(claude).toContainText("compare the fixes");
  await expect(codex).toContainText("compare the fixes");
  await expect(page.locator(".streaming")).toHaveCount(0);
  await expect(page.locator(".agent-busy")).toHaveCount(0);
});

test("adds an agent with $codex+", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await composer(page).fill("");
  await composer(page).pressSequentially("$codex+");
  const pop = page.getByRole("listbox", { name: "Agents" });
  await expect(pop.getByRole("option")).toHaveText([/\$codex\+.*start another codex/]);
  await composer(page).press("Enter");
  await expect(composer(page)).toHaveValue("$codex+ ");
  await composer(page).pressSequentially("review the limiter");
  await composer(page).press("Enter");
  await expect(page.locator('.agent[data-agent="codex-2"]')).toBeVisible();
  await expect(page.locator('[data-agent="codex-2"].msg')).toContainText("review the limiter");
  expect(fake.sent.map((s) => s.text)).toEqual(["$codex+ review the limiter"]);

  // The agent picker writes the same mention.
  await page.getByRole("button", { name: "Pick an agent" }).click();
  await page.getByRole("menu", { name: "Agents" }).getByRole("menuitem", { name: "Start another opencode" }).click();
  await expect(composer(page)).toHaveValue("$opencode+ ");
});

test("approves an approval from another session", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await page.locator(".panel-toggles").getByRole("button", { name: /Approvals/ }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  await expect(panel.locator(".group-head")).toHaveText("Docs pass");
  await expect(panel.locator(".approval-what")).toHaveText("go test ./...");
  await panel.getByRole("button", { name: "Always allow this session" }).click();
  await expect(panel.getByText("No approvals wait for you.")).toBeVisible();
  await expect(sidebar(page).locator('[data-session="ses_docs"] [data-status="approval"]')).toHaveCount(0);
  expect(fake.answered).toEqual([{ id: "apr_docs", decision: "allow_session" }]);
});

test("a live approval makes its agent wait", async ({ page }) => {
  await pair(page);
  await openMain(page);
  fake.requestApproval("ses_main", "codex-1", "rm -rf build");
  await expect(page.locator('.agent[data-agent="codex-1"] .agent-status')).toHaveText("waiting");
  await expect(page.locator(".panel-toggles").getByRole("button", { name: /Approvals/ })).toHaveText("Approvals2");
});

test("pages older messages, switches with the palette and makes a session", async ({ page }) => {
  await pair(page);
  await sidebar(page).getByRole("button", { name: "Long history", exact: true }).click();
  await expect(page.locator(".msg")).toHaveCount(100);
  await page.getByRole("button", { name: "Load older messages" }).click();
  await expect(page.locator(".msg")).toHaveCount(130);
  await expect(page.getByRole("button", { name: "Load older messages" })).toHaveCount(0);

  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog", { name: "Commands" });
  await palette.getByRole("textbox").fill("docs");
  await palette.getByRole("textbox").press("Enter");
  await expect(page.locator(".title")).toHaveText("Docs pass");

  // A new thread starts on the empty screen; the first message makes it,
  // titled after the message.
  await page.keyboard.press("Control+n");
  await expect(page.locator(".title")).toHaveText("New thread");
  await composer(page).fill("$claude write the release notes");
  await composer(page).press("Enter");
  await expect(sidebar(page).locator(".session")).toHaveCount(4);
  await expect(page.locator(".title")).toHaveText("write the release notes");
  await expect(page.locator('[data-agent="claude-1"].msg').last()).toContainText("write the release notes");
  expect(fake.sent.map((s) => s.text)).toEqual(["$claude write the release notes"]);
});

test("changes panel and screenshot", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  // Every file shows its diff, with line numbers.
  await expect(panel.locator(".diff-row.diff-add").first()).toContainText("l.fail(addr)");
  await expect(panel.locator(".diff-row.diff-add .diff-gutter").first()).toHaveText(/\d+/);
  await composer(page).fill("$claude $codex+ split the work");
  await composer(page).press("Enter");
  await expect(page.locator('.agent[data-agent="codex-2"]')).toBeVisible();
  await page.locator(".msg").last().waitFor();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(__dirname, "..", "..", "docs", "renderer.png") });
});
