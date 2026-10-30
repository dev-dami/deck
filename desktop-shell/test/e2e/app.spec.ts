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
  // A fresh install: the Welcome's Pair goes to the pairing form.
  await expect(page.getByRole("heading", { name: "One deck for all your agents" })).toBeVisible();
  await page.getByRole("button", { name: "Pair with a Deck" }).click();
  await expect(page.getByLabel("Pairing link or invite line")).toBeFocused();
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
  await expect(nav.locator('[data-session="ses_docs"] [data-status="needs-you"]')).toHaveText("Needs you");
  await expect(nav.locator('[data-session="ses_docs"] .status-label')).toHaveText("Approve command");
  await openMain(page);
  // Markdown renders as elements; a finished reply folds its work behind
  // "Worked for …", its tool calls fold into one actions row, and each
  // call starts folded too.
  await expect(page.locator(".md strong").first()).toHaveText("every");
  await expect(page.locator("pre.codeblock")).toContainText("l.fail(addr)");
  await expect(page.locator("details.tool")).toHaveCount(0);
  await page.locator(".worked").first().click();
  await expect(page.locator(".actions-head").first()).toContainText("1 action");
  await expect(page.locator("details.tool")).toHaveCount(0);
  await page.locator(".actions-head").first().click();
  const tool = page.locator("details.tool").first();
  await expect(tool).not.toHaveAttribute("open", "");
  await tool.locator("summary").click();
  await expect(tool.locator(".tool-output")).toContainText("ok");
  // The composer names the agent the person talks to: the thread's lead.
  await expect(page.getByRole("button", { name: /^Agent: claude/ })).toBeVisible();
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
  await expect(page.locator('.lane[data-status="working"]')).toHaveCount(2);
  await expect(claude).toContainText("compare the fixes");
  await expect(codex).toContainText("compare the fixes");
  await expect(page.locator(".streaming")).toHaveCount(0);
  await expect(page.locator('.lane[data-status="working"]')).toHaveCount(0);
});

test("@ and $ open no agent picker: the thread's agent gets the text as typed", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await composer(page).fill("");
  await composer(page).pressSequentially("$codex review the limiter, @codex too", { delay: 20 });
  await expect(page.getByRole("listbox")).toHaveCount(0);
  await composer(page).press("Enter");
  expect(fake.sent.map((s) => s.text)).toEqual(["$codex review the limiter, @codex too"]);
  // No chip per agent, no "Add agent": one agent menu.
  await expect(page.getByRole("checkbox", { name: /^Send to / })).toHaveCount(0);
  await expect(page.getByRole("button", { name: "Add agent" })).toHaveCount(0);
  await expect(page.getByRole("button", { name: /^Agent: / })).toHaveCount(1);
});

test("/race on a paired Deck starts mixed entrants through race.start", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await composer(page).fill("/race make the limiter per address");
  await composer(page).press("Enter");
  const builder = page.getByRole("region", { name: "Race builder" });
  await builder.getByRole("button", { name: "Add agent" }).click();
  const add = () => page.getByRole("menu", { name: "Add an entrant" });
  await add().getByRole("group", { name: "Agent" }).getByRole("menuitemradio", { name: "codex" }).click();
  await add().getByRole("menuitem", { name: "Add codex" }).click();
  await add().getByRole("group", { name: "Agent" }).getByRole("menuitemradio", { name: "claude" }).click();
  await add().getByRole("menuitem", { name: "Add claude" }).click();
  await page.keyboard.press("Escape");
  await builder.getByRole("button", { name: /Start race \(2\)/ }).click();
  await expect(builder).toHaveCount(0);
  expect(fake.races).toEqual([{ session_id: "ses_main", entrants: [{ agent: "codex" }, { agent: "claude" }], brief: "make the limiter per address" }]);
  await expect(page.locator('.lane[data-lane="agent:codex-2"]')).toBeVisible();
  await expect(page.locator('.lane[data-lane="agent:claude-2"]')).toBeVisible();
  // Nothing went as a message.
  expect(fake.sent).toEqual([]);
});

test("approves the approval of another thread over its composer", async ({ page }) => {
  await pair(page);
  await openMain(page);
  await sidebar(page).getByRole("button", { name: "Docs pass", exact: true }).click();
  const card = page.locator('[aria-label="Approvals for this thread"]');
  await expect(page.locator(".approval-what")).toHaveText("go test ./...");
  await page.getByRole("button", { name: "Always allow this session" }).click();
  await expect(card).toHaveCount(0);
  await expect(sidebar(page).locator('[data-session="ses_docs"] [data-status="needs-you"]')).toHaveCount(0);
  expect(fake.answered).toEqual([{ id: "apr_docs", decision: "allow_session" }]);
});

test("a live approval makes its agent wait", async ({ page }) => {
  await pair(page);
  await openMain(page);
  fake.requestApproval("ses_main", "codex-1", "rm -rf build");
  // Its lane says so.
  await expect(page.locator('.lane[data-lane="agent:codex-1"]')).toHaveAttribute("data-status", "waiting");
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
  await composer(page).fill("$claude $codex split the work");
  await composer(page).press("Enter");
  await expect(page.locator('[data-agent="codex-1"].msg').last()).toContainText("split the work");
  await page.locator(".msg").last().waitFor();
  await page.waitForTimeout(500);
  await page.screenshot({ path: join(__dirname, "..", "..", "docs", "renderer.png") });
});

test("diffs: syntax colour, changed words and a split view", async ({ page }) => {
  await pair(page);
  await openMain(page);
  // The reply's fenced Go block is coloured as tokens.
  await expect(page.locator("pre.codeblock .syn-function").first()).toHaveText("fail");
  await page.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  const file = panel.locator(".file").first();
  await expect(file.locator(".file-lang")).toHaveText("Go");
  // The hunk header and the function it is in.
  await expect(file.locator(".diff-hunk-row").first()).toContainText("@@ -12,7 +12,9 @@");
  await expect(file.locator(".diff-hunk-ctx").first()).toHaveText("func (l *Limiter) Fail(addr string) {");
  // The fake .go diff shows highlighted tokens.
  await expect(file.locator(".diff-row.diff-add .syn-comment")).toHaveText("// Per address, never global (R1).");
  await expect(file.locator(".diff-row.diff-add .syn-function").first()).toHaveText("fail");
  const fn = await file.locator(".diff-row.diff-add .syn-function").first().evaluate((e) => getComputedStyle(e).color);
  const plainColour = await file.locator(".diff-row.diff-add").first().evaluate((e) => getComputedStyle(e).color);
  expect(fn).not.toBe(plainColour);
  // The removed and the added line mark the words that changed.
  await expect(file.locator(".diff-row.diff-del .diff-word")).toHaveText(["global"]);
  await expect(file.locator(".diff-row.diff-add .diff-word")).toHaveText(["l", "addr"]);

  // Split is offered once the panel is expanded.
  await expect(panel.getByRole("button", { name: "Split view" })).toHaveCount(0);
  await panel.getByRole("button", { name: "Widen the panel" }).click();
  await panel.getByRole("button", { name: "Split view" }).click();
  const split = file.locator(".diff-split");
  await expect(split).toBeVisible();
  const old = split.locator('.diff-code[data-side="old"]', { hasText: "global.fail()" });
  const next = split.locator('.diff-code[data-side="new"]', { hasText: "l.fail(addr)" });
  await expect(old).toBeVisible();
  await expect(next).toBeVisible();
  // Two columns: the old line sits left of the new one, on the same row.
  const a = await old.boundingBox();
  const b = await next.boundingBox();
  expect(a !== null && b !== null && a.x + a.width <= b.x && Math.abs(a.y - b.y) < 2).toBe(true);
  // A removed line takes a comment on the old side.
  await old.hover();
  await split.locator('.diff-gutter[data-side="old"]').getByRole("button", { name: "Comment on line 13" }).click();
  await expect(panel.getByRole("textbox", { name: "Comment on pkg/remote/auth/limit.go:13" })).toBeFocused();
  await page.keyboard.press("Escape");
  // The choice is kept; unified comes back with one click.
  const kept = await page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}"));
  expect(kept).toMatchObject({ diffView: "split" });
  await panel.getByRole("button", { name: "Unified view" }).click();
  await expect(file.locator(".diff-split")).toHaveCount(0);
});

test("diffs: a diff theme recolours the tokens", async ({ page }) => {
  await pair(page);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Appearance" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-diff-theme", "match");
  const keyword = page.locator(".diff-preview .syn-keyword").first();
  await expect(keyword).toHaveText("const");
  const before = await keyword.evaluate((e) => getComputedStyle(e).color);
  await page.getByRole("radiogroup", { name: "Diff theme" }).getByRole("radio", { name: "GitHub Dark" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-diff-theme", "github-dark");
  await expect(keyword).toHaveCSS("color", "rgb(255, 123, 114)");
  expect(before).not.toBe("rgb(255, 123, 114)");
  // Monochrome drops the syntax colour.
  await page.getByRole("radiogroup", { name: "Diff theme" }).getByRole("radio", { name: "Monochrome" }).click();
  const mono = await keyword.evaluate((e) => [getComputedStyle(e).color, getComputedStyle(e.parentElement as Element).color]);
  expect(mono[0]).toBe(mono[1]);
  const kept = await page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}"));
  expect(kept).toMatchObject({ diffTheme: "monochrome" });
});

test("themes: a theme from the catalog and an accent recolor the window", async ({ page }) => {
  await pair(page);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Appearance" }).click();
  await page.getByRole("radiogroup", { name: "Colours" }).getByRole("radio", { name: /Tokyo Night/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-palette", "tokyo-night");
  // The page's sheet takes the theme's canvas; the window behind it is glass.
  await expect(page.locator("main.center")).toHaveCSS("background-color", "rgb(26, 27, 38)");
  await page.getByRole("radiogroup", { name: "Colours" }).getByRole("radio", { name: /GitHub Light/ }).click();
  await expect(page.locator("html")).toHaveAttribute("data-mode", "light");
  await page.getByRole("radiogroup", { name: "Accent" }).getByRole("radio", { name: "Green" }).click();
  await expect(page.locator("html")).toHaveAttribute("data-accent", "green");
  // The choice is kept for the next start.
  const kept = await page.evaluate(() => JSON.parse(localStorage.getItem("deck.desktop.prefs") ?? "{}"));
  expect(kept).toMatchObject({ palette: "github-light", accent: "green" });
});

test("menu commands, a downloaded update and a pair link reach the window", async ({ page }) => {
  await pair(page);
  // What main sends on the menu, update and link channels.
  const emit = (fn: string, ev: unknown) =>
    page.evaluate(([f, e]) => (window as unknown as Record<string, (e: unknown) => void>)[f]?.(e), [fn, ev] as const);
  await emit("__deckMenuEmit", "toggle-sidebar");
  await expect(sidebar(page)).toHaveCount(0);
  await emit("__deckMenuEmit", "toggle-sidebar");
  await expect(sidebar(page)).toBeVisible();
  await emit("__deckMenuEmit", "settings");
  await expect(page.locator("[data-settings]")).toHaveAttribute("data-settings", "general");

  await emit("__deckUpdateEmit", { type: "ready", version: "0.2.0" });
  await expect(sidebar(page).getByRole("button", { name: "Update ready · Restart" })).toBeVisible();

  // A pair link fills the form; the person still clicks Pair.
  await emit("__deckLinkEmit", { type: "pair", url: "deck://pair?c=ABCDE" });
  await expect(page.getByLabel("Pairing link or invite line")).toHaveValue("deck://pair?c=ABCDE");
});
