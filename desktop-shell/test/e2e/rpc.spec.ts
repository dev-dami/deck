import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { NotifyInput, ThreadChoice, ThreadMenuInput } from "../../src/shared/bridge";
import type { TestHost } from "../bridge";
import { FakeRpcDeck, protoOf } from "../fake-grpc";
import { attachBridge } from "./harness";

// The renderer over the rpc transport: "Open a folder" starts the fake deck
// binary, which hands over a fake gRPC server's handshake.
let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;
// Folders the picker hands back, in turn, before dir.
let picks: string[] = [];
// Whether the window has the focus, and the notifications main showed.
let focused = true;
let notes: NotifyInput[] = [];
let badges: number[] = [];
// The thread menus main showed, and what the person picks in the next one.
let menus: ThreadMenuInput[] = [];
let menuPick: ThreadChoice = null;

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  picks = [];
  focused = true;
  notes = [];
  badges = [];
  menus = [];
  menuPick = null;
  host = await attachBridge(page, async () => picks.shift() ?? dir, {
    focused: () => focused,
    notify: (n) => notes.push(n),
    badge: (c) => badges.push(c),
    threadMenu: async (t) => {
      menus.push(t);
      return menuPick;
    },
  });
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });
// Where the race builder's screenshots go: DECK_SHOTS, else the temp folder.
const shot = (name: string) => join(process.env["DECK_SHOTS"] ?? join(tmpdir(), "deck-shots"), name);

// Opens a tab of the room's side panel.
async function roomTab(page: Page, name: string): Promise<void> {
  const panel = page.getByRole("complementary", { name: "Side panel" });
  if ((await panel.count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel.getByRole("tab", { name }).click();
}

async function openFolder(page: Page): Promise<void> {
  await page.goto("/");
  // A fresh install has no recent folder and no pairing: the Welcome.
  await page.getByRole("button", { name: "Open a folder" }).click();
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
  await composer(page).fill("$claude $codex split the work");
  await composer(page).press("Enter");
  const claude = page.locator('[data-agent="claude-1"].msg').last();
  const codex = page.locator('[data-agent="codex-1"].msg').last();
  await expect(claude.locator(".streaming")).toBeVisible();
  await expect(codex.locator(".streaming")).toBeVisible();
  await expect(claude).toContainText("split the work");
  await expect(codex).toContainText("split the work");
  await expect(page.locator(".streaming")).toHaveCount(0);
  // Yolo is offered: the rpc server allows it for the person at this machine.
  await page.locator(".mode-picker").click();
  await expect(page.getByRole("menuitemradio", { name: /Yolo/ })).toBeVisible();
});

test("the agent pickers list every agent once, keep it, and refresh by hand", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  // One pass over the kinds, apart from any thread.
  await expect.poll(() => [...fake.deck.modelLists].sort()).toEqual(["claude", "codex", "opencode", "pi"]);

  // A lane's picker: the same sections as any other, open at once.
  await page.locator('.lane[data-lane="agent:claude-1"]').click();
  const lane = page.getByRole("menu", { name: "claude-1" });
  await expect(lane.getByRole("menuitemradio", { name: /Default/ })).toBeVisible();
  await expect(lane.getByRole("menuitem", { name: "Apply" })).toBeDisabled();
  await lane.getByRole("menuitemradio", { name: /Deep/ }).click();
  await lane.getByRole("group", { name: "Reasoning effort" }).getByRole("menuitemradio", { name: "high" }).click();
  await lane.getByRole("menuitem", { name: "Apply" }).click();
  const roster = () => fake.deck.invoke("roster.get", { session_id: "ses_main" }) as Array<{ id: string; model?: string }>;
  await expect.poll(() => roster().find((l) => l.id === "claude-1")?.model).toBe("deep");

  // Opening pickers again and switching threads asks nothing more.
  await page.locator('.lane[data-lane="agent:codex-1"]').click();
  const codex = page.getByRole("menu", { name: "codex-1" });
  await expect(codex.getByRole("group", { name: "Model" }).getByRole("menuitemradio", { name: /Fast/ })).toBeVisible();
  // Wide and short: the models sit side by side, and the popover is wider than tall.
  const box = (await codex.boundingBox())!;
  expect(box.width).toBeGreaterThan(box.height);
  // The arrows still walk the radios in order.
  await codex.getByRole("menuitemradio", { name: /Default/ }).focus();
  await page.keyboard.press("ArrowDown");
  await expect(codex.getByRole("menuitemradio", { name: /Fast/ })).toBeFocused();
  await page.keyboard.press("Escape");
  const others = sidebar(page).locator(".session");
  await others.nth(1).click();
  await others.nth(0).click();
  expect(fake.deck.modelLists).toHaveLength(4);

  // Refresh lists them all again, asking the Deck to forget its lists once.
  await page.locator('.lane[data-lane="agent:claude-1"]').click();
  await page.getByRole("menu", { name: "claude-1" }).getByRole("menuitem", { name: "Refresh agents" }).click();
  await expect.poll(() => fake.deck.modelLists.length).toBe(8);
  expect(fake.modelRefreshes).toBe(1);
});

test("$pi starts a pi lane, and its picker offers the models pi has a key for", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await composer(page).fill("$pi look at the limiter");
  await composer(page).press("Enter");
  const lane = page.locator('.lane[data-lane="agent:pi-1"]');
  await expect(lane).toBeVisible();
  await expect(lane).toContainText("π");
  await expect(page.locator('[data-agent="pi-1"].msg').last()).toContainText("look at the limiter");

  await lane.click();
  const menu = page.getByRole("menu", { name: "pi-1" });
  await expect(menu.getByRole("menuitemradio", { name: /Coder/ })).toBeVisible();
  await menu.getByRole("menuitemradio", { name: /Reasoner/ }).click();
  const efforts = menu.getByRole("group", { name: "Reasoning effort" });
  await expect(efforts.getByRole("menuitemradio")).toHaveText(["Default", "none", "low", "medium", "high"]);
  await efforts.getByRole("menuitemradio", { name: "medium" }).click();
  await menu.getByRole("menuitem", { name: "Apply" }).click();
  const roster = () => fake.deck.invoke("roster.get", { session_id: "ses_main" }) as Array<{ id: string; model?: string }>;
  await expect.poll(() => roster().find((l) => l.id === "pi-1")?.model).toBe("openrouter/reasoner");

  // Settings lists pi with the kinds this Deck runs, and a kind it did not
  // find as not installed.
  await sidebar(page).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Agents" }).click();
  await expect(page.locator("h3", { hasText: /^pi$/ })).toBeVisible();
  await expect(page.locator('[data-kind="antigravity"][data-available="false"]')).toContainText("not installed");
});

test("first run: the Welcome lists the agent CLIs, and the new-thread cards fill the composer", async ({ page }) => {
  await page.goto("/");
  await expect(page.getByRole("heading", { name: "One deck for all your agents" })).toBeVisible();
  const clis = page.getByRole("list", { name: "Agent CLIs" });
  await expect(clis.locator('[data-kind="claude"]')).toContainText("found");
  await expect(clis.locator('[data-kind="claude"]')).toContainText("/usr/local/bin/claude");
  await expect(clis.locator('[data-kind="codex"]')).toContainText("/opt/homebrew/bin/codex");
  await expect(clis.locator('[data-kind="opencode"]')).toContainText("not found");
  await expect(clis.locator('[data-kind="opencode"]').getByRole("link", { name: "Install guide" })).toHaveAttribute("href", "https://opencode.ai");
  // Pi: not found, with how to get pi and its ACP server.
  await expect(clis.locator('[data-kind="pi"]')).toContainText("pi-acp");
  // Antigravity: its name only.
  await expect(clis.locator('[data-kind="antigravity"]').getByRole("link")).toHaveCount(0);
  await page.getByRole("button", { name: "Check again" }).click();
  await expect(clis.locator('[data-found="true"]')).toHaveCount(2);

  // A new project opens on the new-thread screen.
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await expect(page.getByRole("heading", { name: `What should we build in ${basename(dir)}?` })).toBeVisible();
  const starts = page.getByRole("list", { name: "Ways to start" });
  await starts.getByRole("button", { name: /Split the work/ }).click();
  await expect(composer(page)).toHaveValue("$claude $codex ");
  await expect(composer(page)).toBeFocused();
  await composer(page).pressSequentially("fix the limiter");
  await starts.getByRole("button", { name: /Race agents/ }).click();
  await expect(composer(page)).toHaveValue("/race fix the limiter");
  await starts.getByRole("button", { name: /One agent/ }).click();
  await expect(composer(page)).toHaveValue("$claude fix the limiter");

  // The palette opens the Welcome again, with a way back. Enter waits for
  // the Welcome to be the picked line.
  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog", { name: "Commands" });
  await palette.getByRole("textbox").fill("Welcome");
  await expect(palette.getByRole("option", { selected: true })).toHaveText(/Welcome/);
  await palette.getByRole("textbox").press("Enter");
  await expect(page.getByRole("heading", { name: "One deck for all your agents" })).toBeVisible();
  await page.getByRole("button", { name: "Back to threads" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
});

test("first run: an engine that finds no agent CLI shows the agents, any other error its last lines", async ({ page }) => {
  process.env["DECK_FAKE_NO_AGENTS"] = "1";
  try {
    await page.goto("/");
    await page.getByRole("button", { name: "Open a folder" }).click();
    const agents = page.getByRole("region", { name: "Your agents" });
    await expect(agents).toContainText("Deck found no agent CLI");
    await expect(page.getByRole("alert")).toHaveCount(0);
  } finally {
    delete process.env["DECK_FAKE_NO_AGENTS"];
  }
  delete process.env["DECK_FAKE_HANDSHAKE"];
  await page.getByRole("region", { name: "Your agents" }).getByRole("button", { name: "Try again" }).click();
  const failed = page.getByRole("alert", { name: "Deck did not start" });
  await expect(failed.locator("pre")).toHaveText("fake deck: DECK_FAKE_HANDSHAKE is not set");
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  await failed.getByRole("button", { name: "Try again" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
});

test("a session in the background updates live", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Docs pass", exact: true }).click();
  await expect(page.locator(".title")).toHaveText("Docs pass");
  // Another UI (the terminal) writes to the main session meanwhile.
  fake.deck.invoke("chat.send", { session_id: "ses_main", text: "$codex check the limiter tests" });
  const row = sidebar(page).locator('[data-session="ses_main"]');
  // Working while the agent writes; once it finished in the background, its
  // changed files wait for review.
  await expect(row.locator('[data-status="working"]')).toBeVisible();
  await expect(row.locator('[data-status="review"]')).toBeVisible();
  await expect(row.locator('[data-status="working"]')).toHaveCount(0);
  const lists = fake.calls.filter((m) => m === "ListMessages").length;
  await row.getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  // The reply streamed in while the session was in the background.
  await expect(page.locator('[data-agent="codex-1"].msg').last()).toContainText("check the limiter tests");
  await expect(row.locator('[data-status="review"]')).toHaveCount(0);
  expect(lists).toBe(1);
});

test("answers an approval and closes the folder", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Docs pass", exact: true }).click();
  await page.getByRole("button", { name: "Approve", exact: true }).click();
  await expect(page.locator('[aria-label="Approvals for this thread"]')).toHaveCount(0);
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
  await expect(usage).toContainText(/\d+% used/);
  await usage.getByRole("radio", { name: "7 days" }).click();
  await usage.getByRole("button", { name: "Table" }).click();
  await expect(usage.locator("table").first().locator("tbody tr")).toHaveCount(7);
  await usage.getByRole("button", { name: /Fix the pairing lockout/ }).click();
  await expect(page.locator(".title")).toHaveText("Fix the pairing lockout");
});

test("usage analytics: quota with a projection where reported, usage alone where not", async ({ page }) => {
  await openFolder(page);
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Usage" }).click();
  const usage = page.locator("[data-usage]");
  await usage.getByRole("radio", { name: "Analytics" }).click();
  const claude = usage.locator('[data-kind-section="claude"]');
  await expect(claude.locator('[data-headline="quota"]')).toContainText("%");
  await expect(claude.locator("[data-projected]")).toHaveCount(1);
  await expect(claude.getByRole("radiogroup", { name: "Claude window" }).getByRole("radio")).toHaveCount(2);
  // OpenCode reports no plan limits: its tokens, no quota and no projection.
  const opencode = usage.locator('[data-kind-section="opencode"]');
  await expect(opencode.locator('[data-headline="usage"]')).toContainText("reports no plan limits");
  await expect(opencode.locator("[data-projected]")).toHaveCount(0);
  // One agent's tab splits it by agent; earlier spans have no projection.
  await usage.getByRole("tab", { name: "Claude" }).click();
  await expect(usage.locator("[data-kind-section]")).toHaveCount(1);
  await expect(claude).toContainText("reviewer");
  await usage.getByRole("button", { name: "Earlier" }).click();
  await expect(claude.locator("[data-projected]")).toHaveCount(0);
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
  // Threads sit under their project folders, as in T3 Code.
  await expect(sidebar(page).locator(".project-group")).toHaveCount(2);
  const first = sidebar(page).locator(`[data-project="${basename(dir)}"] .session`).filter({ hasText: "Docs pass" });
  await first.getByRole("button", { name: "Docs pass", exact: true }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await expect(page.locator(".title")).toHaveText("Docs pass");
  // The filter shows one project; closing one leaves the other.
  await sidebar(page).getByRole("button", { name: "Projects" }).click();
  await page.getByRole("menu", { name: "Projects" }).getByRole("button", { name: `Close ${basename(second)}` }).click();
  await expect(sidebar(page).locator(".session")).toHaveCount(3);
});

test("projects: a switch and back keeps the terminal, the draft and the unread thread", async ({ page }) => {
  const second = mkdtempSync(join(tmpdir(), "deck-second-"));
  await openFolder(page);
  const mine = sidebar(page).locator(`[data-project="${basename(dir)}"]`);
  // A shell that runs something.
  await page.keyboard.press("Control+j");
  const drawer = page.getByRole("region", { name: "Terminal" });
  await drawer.locator(".xterm").click();
  await page.keyboard.type("ls");
  await page.keyboard.press("Enter");
  await expect(drawer.locator(".xterm-rows")).toContainText("ran: ls");
  // A draft on the new-thread screen.
  await composer(page).fill("a draft for later");
  // A thread with news the person has not read.
  fake.deck.invoke("chat.send", { session_id: "ses_main", text: "$codex check the limiter tests" });
  await expect(mine.locator('[data-session="ses_main"] [data-status="review"]')).toBeVisible();

  picks = [second];
  await sidebar(page).getByRole("button", { name: "Add project" }).click();
  await expect(page.locator(".project")).toHaveText(basename(second));
  await expect(composer(page)).toHaveValue("");
  await expect(drawer).toBeHidden();

  await mine.getByRole("button", { name: `New thread in ${basename(dir)}` }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await expect(composer(page)).toHaveValue("a draft for later");
  await expect(mine.locator('[data-session="ses_main"] [data-status="review"]')).toBeVisible();
  // The same shell, still running, with what it printed.
  await expect(drawer).toBeVisible();
  await expect(drawer.getByRole("tab")).toHaveCount(1);
  await expect(drawer.locator(".xterm-rows")).toContainText("ran: ls");
  await drawer.locator(".xterm").click();
  await page.keyboard.type("pwd");
  await page.keyboard.press("Enter");
  await expect(drawer.locator(".xterm-rows")).toContainText("ran: pwd");
  expect(fake.calls.filter((m) => m === "TerminalClose")).toHaveLength(0);
});

test("side chat: asks another agent about the thread without touching it", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await roomTab(page, "Side chat");
  const side = page.getByRole("region", { name: "Side chat" });
  await side.getByRole("radio", { name: /codex/ }).click();
  await side.getByLabel("Side question").fill("what is claude-1 doing?");
  await side.getByLabel("Side question").press("Enter");
  // The panel shows the question alone; the agent got it with a snapshot.
  await expect(side.locator(".side-question")).toHaveText("what is claude-1 doing?");
  await expect(side.locator('[data-agent="codex-1"]').last()).toContainText("You asked");
  const sent = fake.deck.sent.at(-1)?.text ?? "";
  expect(sent.startsWith("$codex what is claude-1 doing?")).toBe(true);
  expect(sent).toContain("Agents: claude-1");
  // The side thread stays out of the thread list, and the main thread got nothing.
  await expect(sidebar(page).locator(".session")).toHaveCount(3);
  expect(fake.deck.sent.filter((s) => s.session_id === "ses_main")).toHaveLength(0);
});

test("orchestration: the composer's level picker, and the level Settings gives new threads", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  const dock = page.locator(".composer-dock");
  // The thread's level, from its board, with what each level does.
  await dock.getByRole("button", { name: "Orchestration: Auto" }).click();
  const menu = page.getByRole("menu", { name: "Orchestration" });
  await expect(menu.getByRole("menuitemradio")).toHaveCount(4);
  await expect(menu.getByRole("menuitemradio", { name: /^Auto/ })).toHaveAttribute("aria-checked", "true");
  await expect(menu.getByRole("menuitemradio", { name: /^Full/ })).toContainText("races between agents");
  await menu.getByRole("menuitemradio", { name: /^Full/ }).click();
  await expect.poll(() => fake.board.level).toBe("full");
  await expect(dock.getByRole("button", { name: "Orchestration: Full" })).toBeVisible();
  await expect(menu).toHaveCount(0);

  // Settings › Agents: the level new threads start at, Off until changed.
  await sidebar(page).getByRole("button", { name: "Settings" }).click();
  await page.getByRole("button", { name: "Agents" }).click();
  const levels = page.getByRole("radiogroup", { name: "Orchestration for new threads" });
  await expect(levels.getByRole("radio", { name: "Off" })).toHaveAttribute("aria-checked", "true");
  await levels.getByRole("radio", { name: "Assist" }).click();
  await expect(levels.getByRole("radio", { name: "Assist" })).toHaveAttribute("aria-checked", "true");
  await expect(page.locator(".settings")).toContainText("a couple of helpers");
  expect(await page.evaluate(() => localStorage.getItem("deck.desktop.prefs"))).toContain('"orchestration":"assist"');
  // A new thread asks the Deck for it.
  await page.getByRole("button", { name: "New thread" }).first().click();
  await composer(page).fill("split the limiter work");
  await composer(page).press("Enter");
  await expect.poll(() => fake.createdLevels.at(-1)).toBe("assist");
});

test("room tabs: the agents tree, the task board, keeping a race entry after its files, and the race form", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await roomTab(page, "Agents");
  const agents = page.getByRole("region", { name: "Agents" });
  // The tree: the subagent under the member that started it, with what runs now.
  await expect(agents.locator('.crew-agent[data-agent="codex-sub"]')).toContainText("deck/t3");
  await expect(agents.locator('.crew-agent[data-agent="claude-1"]')).toContainText("go test ./pkg/remote/...");
  await expect(agents.locator('.crew-agent[data-agent="claude-1"]')).toContainText("30% context");
  // Approvals are no tab of the room; Tasks counts the queue.
  await expect(page.getByRole("complementary", { name: "Side panel" }).getByRole("tab")).toHaveText(["Changes", "Graph", "Tasks1", "Agents", "Side chat"]);

  await roomTab(page, "Tasks");
  const tasks = page.getByRole("region", { name: "Tasks" });
  // The queue is the first section of Tasks.
  await expect(tasks.locator(".queued")).toContainText("Also run go vet when you are done.");
  // The board: a race with its entries. Keeping one shows its files first.
  const race = tasks.locator('.board-task[data-task="t2"]');
  await expect(race.locator(".race-entry")).toHaveCount(2);
  await race.locator('.race-entry[data-task="t3"]').getByRole("button", { name: "Keep" }).click();
  const ask = page.getByRole("dialog", { name: "Apply claude-sub's entry?" });
  await expect(ask.getByRole("list", { name: "Files" })).toContainText("a.go");
  await expect(ask.getByRole("list", { name: "Files" })).toContainText("b.go");
  expect(fake.board.actions).toEqual([]);
  await ask.getByRole("button", { name: "Apply", exact: true }).click();
  await expect(page.locator(".toast")).toContainText("Applied 1 file from t3");
  await expect(race.locator(".task-status").first()).toHaveText("done");
  // A failed task retries without a question.
  await tasks.locator('.board-task[data-task="t5"]').getByRole("button", { name: "Retry" }).click();
  // The board's orchestration level is the composer's picker too.
  await tasks.getByRole("button", { name: "Orchestration: Auto" }).click();
  await page.getByRole("menuitemradio", { name: /^Off/ }).click();
  await expect.poll(() => fake.board.level).toBe("off");
  await expect(tasks.getByRole("button", { name: "Orchestration: Off" })).toBeVisible();
  await expect(page.locator(".composer-dock").getByRole("button", { name: "Orchestration: Off" })).toBeVisible();
  expect(fake.board.actions).toEqual(["apply t3", "retry t5"]);
  // The Tasks tab races with the same builder as /race, through StartRace,
  // with a judge by default.
  await tasks.getByRole("button", { name: "New race" }).click();
  const builder = tasks.getByRole("region", { name: "Race builder" });
  await builder.getByLabel("Race task").fill("make the limiter per address");
  await expect(builder.getByRole("button", { name: /Start race/ })).toBeDisabled();
  await builder.getByRole("button", { name: "Add agent" }).click();
  const add = page.getByRole("menu", { name: "Add an entrant" });
  await add.getByRole("group", { name: "Agent" }).getByRole("menuitemradio", { name: "codex" }).click();
  await add.getByRole("menuitem", { name: "Add codex" }).click();
  await page.getByRole("menu", { name: "Add an entrant" }).getByRole("menuitem", { name: "Add codex" }).click();
  await page.keyboard.press("Escape");
  await expect(builder.getByRole("list", { name: "Entrants" }).locator(".race-entrant")).toHaveCount(2);
  await expect(builder.getByRole("switch", { name: "Judge" })).toHaveAttribute("aria-checked", "true");
  await builder.getByRole("button", { name: /Start race \(2\)/ }).click();
  await expect.poll(() => fake.room.asked).toContain("race codex,codex judged: make the limiter per address");
  await expect(builder.getByLabel("Race task")).toHaveValue("");
  // Without the judge.
  await builder.getByLabel("Race task").fill("try again");
  await builder.getByRole("switch", { name: "Judge" }).click();
  await builder.getByRole("button", { name: /Start race \(2\)/ }).click();
  await expect.poll(() => fake.room.asked).toContain("race codex,codex: try again");
});

test("/race: a builder of mixed entrants, codex, claude and claude again on its model, three race lanes", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  const lanes = page.getByRole("toolbar", { name: "Lanes" });
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  const before = await page.locator(".lane[data-lane]").count();

  // The menu offers /race; picking it opens the builder at once.
  await composer(page).fill("");
  await composer(page).pressSequentially("/ra");
  await page.getByRole("listbox", { name: "Commands" }).getByRole("option", { name: /\/race/ }).click();
  const builder = page.getByRole("region", { name: "Race builder" });
  await expect(builder).toBeVisible();
  await expect(composer(page)).toHaveValue("");
  await builder.getByRole("button", { name: "Close the race builder" }).click();
  await expect(builder).toHaveCount(0);

  // The rest of the message is the task.
  await composer(page).fill("/race fix the flaky limiter test");
  await composer(page).press("Enter");
  await expect(builder.getByLabel("Race task")).toHaveValue("fix the flaky limiter test");
  await expect(composer(page)).toHaveValue("");
  await expect(builder.getByRole("button", { name: /Start race/ })).toBeDisabled();

  // codex, then claude on a model, then the same claude again: the add
  // picker starts on the last entrant.
  await builder.getByRole("button", { name: "Add agent" }).click();
  const add = () => page.getByRole("menu", { name: "Add an entrant" });
  await add().getByRole("group", { name: "Agent" }).getByRole("menuitemradio", { name: "codex" }).click();
  await add().getByRole("menuitem", { name: "Add codex" }).click();
  await add().getByRole("group", { name: "Agent" }).getByRole("menuitemradio", { name: "claude" }).click();
  await add().getByRole("group", { name: "Model" }).getByRole("menuitemradio").nth(1).click();
  await add().getByRole("menuitem", { name: "Add claude" }).click();
  await expect(add().getByRole("group", { name: "Model" }).getByRole("menuitemradio").nth(1)).toHaveAttribute("aria-checked", "true");
  await add().getByRole("menuitem", { name: "Add claude" }).click();
  await page.screenshot({ path: shot("race-add-picker.png") });
  await page.keyboard.press("Escape");
  const chips = builder.getByRole("list", { name: "Entrants" }).locator(".race-entrant");
  await expect(chips).toHaveCount(3);
  await expect(chips.nth(0)).toHaveAttribute("data-agent", "codex");
  await expect(chips.nth(1)).toHaveAttribute("data-agent", "claude");
  await expect(chips.nth(2)).toHaveAttribute("data-agent", "claude");
  const model = (await chips.nth(1).locator(".race-entrant-detail").textContent()) ?? "";
  expect(model).not.toBe("Default");
  await expect(chips.nth(2).locator(".race-entrant-detail")).toHaveText(model);
  await expect(builder.locator(".race-summary")).toHaveText("3 agents: codex, claude ×2");
  await page.screenshot({ path: shot("race-builder.png") });

  // A chip edits its entrant; × takes one out.
  await chips.nth(0).getByRole("button", { name: /Change it/ }).click();
  await expect(page.getByRole("menu", { name: "Entrant 1" }).getByRole("menuitemradio", { name: "codex" })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("Escape");
  await builder.getByRole("button", { name: "Add agent" }).click();
  await add().getByRole("menuitem", { name: "Add claude" }).click();
  await page.keyboard.press("Escape");
  await expect(chips).toHaveCount(4);
  await builder.getByRole("button", { name: "Remove entrant 4 (claude)" }).click();
  await expect(chips).toHaveCount(3);

  await builder.getByRole("button", { name: /Start race \(3\)/ }).click();
  await expect(builder).toHaveCount(0);
  const asked = fake.room.asked.find((a) => a.startsWith("race ")) ?? "";
  expect(asked).toMatch(/^race codex,claude\/[^,]+,claude\/[^,]+ judged: fix the flaky limiter test$/);
  const [, first, second] = asked.split(" ")[1]?.split(",") ?? [];
  expect(first).toBe(second);
  // Three lanes join the room, one per entrant.
  await expect(page.locator(".lane[data-lane]")).toHaveCount(before + 3);
  await expect(lanes.locator('[data-lane="agent:codex-2"]')).toBeVisible();
  await expect(lanes.locator('[data-lane="agent:claude-2"]')).toBeVisible();
  await expect(lanes.locator('[data-lane="agent:claude-3"]')).toBeVisible();
  await page.screenshot({ path: shot("race-lanes.png") });
});

test("branches: a thread starts on its own branch, applies it, discards it and moves again", async ({ page }) => {
  await openFolder(page);
  const own = page.getByRole("switch", { name: "On its own branch" });
  await expect(own).toHaveAttribute("aria-checked", "true");
  await expect(page.locator(".own-branch")).toContainText("deck/<id>");
  await composer(page).fill("$claude make the limiter per address");
  await composer(page).press("Enter");
  // The fake names the branch after the session.
  const branch = page.locator(".room-branch");
  await expect(branch).toContainText("deck/ses_");
  const name = (await branch.textContent()) ?? "";
  await expect(sidebar(page).locator(".session.open .row-branch")).toHaveText(name);
  await expect(page.locator(".streaming")).toHaveCount(0);

  await branch.click();
  await page.getByRole("menuitem", { name: "Apply to my tree" }).click();
  await expect(page.locator(".toast").last()).toContainText("Applied 1 file to your tree: pkg/remote/auth/limit.go");

  await branch.click();
  await page.getByRole("menuitem", { name: "Discard branch" }).click();
  const ask = page.getByRole("dialog", { name: `Discard ${name}?` });
  await expect(ask).toContainText(`Deck removes the branch ${name}`);
  await expect(ask).toContainText("changed file");
  await ask.getByRole("button", { name: "Cancel" }).click();
  expect(fake.room.asked.filter((a) => a.startsWith("UnisolateSession"))).toEqual([]);
  await branch.click();
  await page.getByRole("menuitem", { name: "Discard branch" }).click();
  await page.getByRole("dialog", { name: `Discard ${name}?` }).getByRole("button", { name: "Discard branch" }).click();
  await expect(page.locator(".room-branch")).not.toContainText("deck/");
  await expect(sidebar(page).locator(".session.open .row-branch")).toHaveCount(0);

  // A thread in the shared tree can move to a branch of its own.
  await page.getByRole("button", { name: "Branch", exact: true }).click();
  await page.getByRole("menuitem", { name: "Move to its own branch" }).click();
  await expect(page.locator(".room-branch")).toContainText("deck/ses_");
  expect(fake.room.asked.filter((a) => !a.startsWith("since")).map((a) => a.split(" ")[0])).toEqual([
    "ApplyIsolated",
    "UnisolateSession",
    "IsolateSession",
  ]);

  // Off is remembered for the next thread.
  await page.getByRole("button", { name: "New thread" }).first().click();
  await page.getByRole("switch", { name: "On its own branch" }).click();
  await expect(page.getByRole("switch", { name: "On its own branch" })).toHaveAttribute("aria-checked", "false");
  expect(await page.evaluate(() => localStorage.getItem("deck.desktop.prefs"))).toContain('"isolate":false');
});

test("branches: Undo takes an apply back, from the toast or the branch menu", async ({ page }) => {
  await openFolder(page);
  await composer(page).fill("$claude make the limiter per address");
  await composer(page).press("Enter");
  const branch = page.locator(".room-branch");
  await expect(branch).toContainText("deck/ses_");
  await expect(page.locator(".streaming")).toHaveCount(0);
  const asked = () => fake.room.asked.map((a) => a.split(" ")[0]);

  await branch.click();
  await expect(page.getByRole("menuitem", { name: "Undo last apply" })).toHaveCount(0);
  await page.getByRole("menuitem", { name: "Apply to my tree" }).click();
  const applied = page.locator(".toast").filter({ hasText: "Applied 1 file to your tree" });
  await applied.getByRole("button", { name: "Undo" }).click();
  await expect.poll(asked).toEqual(["ApplyIsolated", "UndoApply"]);
  await expect(page.locator(".toast").filter({ hasText: "Undid the apply: 1 file as before." })).toBeVisible();

  // While an apply is recorded, the branch menu offers to undo it.
  await branch.click();
  await expect(page.getByRole("menuitem", { name: "Undo last apply" })).toHaveCount(0);
  await page.getByRole("menuitem", { name: "Apply to my tree" }).click();
  await branch.click();
  await page.getByRole("menuitem", { name: "Undo last apply" }).click();
  await expect.poll(asked).toEqual(["ApplyIsolated", "UndoApply", "ApplyIsolated", "UndoApply"]);
  await branch.click();
  await expect(page.getByRole("menuitem", { name: "Undo last apply" })).toHaveCount(0);
});

// A 1×1 PNG.
const PNG = Buffer.from("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNkYAAAAAYAAjCB0C8AAAAASUVORK5CYII=", "base64");

test("an image in a message shows as a thumbnail and opens larger", async ({ page }) => {
  await openFolder(page);
  fake.deck.sendImage("ses_docs", "Here is the screen.", "shot.png", "image/png", PNG);
  await sidebar(page).getByRole("button", { name: "Docs pass", exact: true }).click();
  const thumb = page.getByRole("button", { name: "Open shot.png" });
  await expect(thumb.locator("img")).toHaveAttribute("src", /^blob:/);
  await expect.poll(() => thumb.locator("img").evaluate((img: HTMLImageElement) => img.naturalWidth)).toBe(1);
  await thumb.click();
  const lightbox = page.getByRole("dialog", { name: "shot.png" });
  await expect(lightbox.locator("img")).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(lightbox).toHaveCount(0);
  expect(fake.calls.filter((c) => c === "ReadImage")).toHaveLength(1);
});

test("palette: finds a message in a room whose messages are not loaded and opens the room at it", async ({ page }) => {
  // A room long enough to page and to render only the rows in view.
  await fake.close();
  fake = await FakeRpcDeck.start({ tickMs: 250, bigRoom: 300 });
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const needle = fake.deck.allMessages().find((m) => m.session_id === "ses_big" && m.parts[0]?.text === "Big 7");
  if (needle === undefined) throw new Error("no Big 7");
  needle.parts = [{ type: "text", text: "Big 7 holds the needle" }];
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();

  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog", { name: "Commands" });
  await palette.getByRole("textbox").fill("needle");
  const hit = palette.locator('[data-kind="hit"]');
  await expect(hit).toHaveCount(1);
  await expect(hit).toContainText("Big room");
  await expect(hit.locator("mark")).toHaveText("needle");
  await hit.click();
  await expect(page.locator(".topbar .title")).toHaveText("Big room");
  await expect(page.locator(".user-text", { hasText: "Big 7 holds the needle" })).toBeInViewport();
  expect(fake.calls.filter((c) => c === "SearchMessages").length).toBeGreaterThanOrEqual(1);
});

test("revert: a file or all of them, after a question, with Undo and a note in the room", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await roomTab(page, "Changes");
  const changes = page.getByRole("region", { name: "Changes" });
  await changes.getByRole("button", { name: "Revert pkg/remote/auth/limit.go" }).click();
  const ask = page.getByRole("dialog", { name: "Revert pkg/remote/auth/limit.go?" });
  await ask.getByRole("button", { name: "Cancel" }).click();
  expect(fake.room.asked).toEqual([]);
  await changes.getByRole("button", { name: "Revert pkg/remote/auth/limit.go" }).click();
  await ask.getByRole("button", { name: "Revert", exact: true }).click();
  await expect.poll(() => fake.room.asked).toEqual(["RevertFiles ses_main", "since 0"]);
  const toast = page.locator(".toast").filter({ hasText: "Reverted 1 file." });
  await expect(toast).toBeVisible();
  // The timeline says what the person did; the composer stays empty.
  await expect(page.locator(".room-note")).toHaveText("You reverted pkg/remote/auth/limit.go");
  await expect(composer(page)).toHaveValue("");
  await toast.getByRole("button", { name: "Undo" }).click();
  await expect.poll(() => fake.room.asked).toContain("UndoRevert ses_main");
  await expect(toast).toHaveCount(0);

  await changes.getByRole("button", { name: "Revert all" }).click();
  const all = page.getByRole("dialog", { name: "Revert all 2 files?" });
  await expect(all).toContainText("pkg/remote/auth/limit_test.go");
  await all.getByRole("button", { name: "Revert all" }).click();
  await expect(page.locator(".toast").filter({ hasText: "Reverted 2 files." })).toBeVisible();
  await expect(page.locator(".room-note").last()).toHaveText("You reverted 2 files: pkg/remote/auth/limit.go, pkg/remote/auth/limit_test.go");
  // The line comment and its count stay in the diff.
  await expect(changes.locator(".file").first()).toBeVisible();
});

test("archive: Deck archives at once, Undo unarchives, and the fold brings a room back", async ({ page }) => {
  await openFolder(page);
  const row = sidebar(page).locator('[data-session="ses_long"]');
  await row.hover();
  await row.getByRole("button", { name: "Archive Long history" }).click();
  await expect(row).toHaveCount(0);
  await expect.poll(() => fake.room.asked).toEqual(["ArchiveSession ses_long"]);
  const toast = page.locator(".toast").filter({ hasText: 'Archived "Long history".' });
  await toast.getByRole("button", { name: "Undo" }).click();
  await expect.poll(() => fake.room.asked).toEqual(["ArchiveSession ses_long", "UnarchiveSession ses_long"]);
  await expect(row).toBeVisible();

  // Archived again, the room waits in the fold, and its row brings it back.
  await row.hover();
  await row.getByRole("button", { name: "Archive Long history" }).click();
  const fold = sidebar(page).getByRole("button", { name: "Archived (1)" });
  await fold.click();
  await row.hover();
  await expect(row.getByRole("button", { name: "Archive Long history", exact: true })).toHaveCount(0);
  await row.getByRole("button", { name: "Unarchive Long history" }).click();
  await expect.poll(() => fake.room.asked.at(-1)).toBe("UnarchiveSession ses_long");
  await expect(fold).toHaveCount(0);
  await expect(row).toBeVisible();
});

test("archive: with a Deck that cannot unarchive, the row goes, Undo brings it back, and after five seconds Deck archives it", async ({ page }) => {
  fake.room.caps = fake.room.caps.filter((m) => m !== "UnarchiveSession");
  await openFolder(page);
  const row = sidebar(page).locator('[data-session="ses_long"]');
  await row.hover();
  await row.getByRole("button", { name: "Archive Long history" }).click();
  await expect(row).toHaveCount(0);
  const toast = page.locator(".toast").filter({ hasText: 'Archived "Long history".' });
  await toast.getByRole("button", { name: "Undo" }).click();
  await expect(row).toBeVisible();
  await page.waitForTimeout(5500);
  expect(fake.room.asked).toEqual([]);

  // The row's menu archives too; Deck hears of it after the Undo time.
  menuPick = "archive";
  await row.click({ button: "right" });
  await expect.poll(() => menus.at(-1)).toEqual({ id: "ses_long", title: "Long history", archive: "on" });
  await expect(row).toHaveCount(0);
  await expect.poll(() => fake.room.asked, { timeout: 8000 }).toEqual(["ArchiveSession ses_long"]);
  // The archived room waits behind a fold, closed at first.
  const fold = sidebar(page).getByRole("button", { name: "Archived (1)" });
  await expect(fold).toHaveAttribute("aria-expanded", "false");
  await fold.click();
  await expect(row).toBeVisible();
  await row.hover();
  await expect(row.getByRole("button", { name: "Unarchive Long history" })).toHaveCount(0);

  // Never for a room whose agents work.
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await composer(page).fill("$claude go on");
  await composer(page).press("Enter");
  const busy = sidebar(page).locator('[data-session="ses_main"]');
  await busy.hover();
  await expect(busy.getByRole("button", { name: "Archive Fix the pairing lockout" })).toHaveAttribute("aria-disabled", "true");
  menuPick = null;
  await busy.click({ button: "right" });
  await expect.poll(() => menus.at(-1)?.archive).toBe("busy");
});

test("@ lists the tree's files after the changed ones, and leases show in the lane strip", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await composer(page).pressSequentially("look at @limit");
  const files = page.getByRole("listbox", { name: "Files" });
  await expect(files.getByRole("option").first()).toContainText("pkg/remote/auth/limit.go");
  await expect(files.getByRole("option").first()).toContainText("changed");
  await composer(page).fill("");
  await composer(page).pressSequentially("look at @READ");
  await expect(files.getByRole("option")).toHaveText(["README.md"]);
  await page.keyboard.press("Enter");
  await expect(composer(page)).toHaveValue("look at `README.md` ");
  expect(fake.calls).toContain("ListFiles");

  // A lease the engine reports lands on the agent's chip and in its popover.
  fake.emit("lease_changed", { session_id: "ses_main", agent: "claude-1", paths: ["pkg/rpc/**", "docs/a.md"], exclusive: true, released: false });
  const lanes = page.getByRole("toolbar", { name: "Lanes" });
  await expect(lanes.locator('[data-lane="agent:claude-1"] .lane-leases')).toHaveText("2");
  await lanes.locator('[data-lane="agent:claude-1"]').click();
  await expect(page.getByRole("list", { name: "Paths claude-1 holds" }).getByRole("listitem")).toHaveText(["pkg/rpc/**", "docs/a.md"]);
});

test("attention from the engine: a room that waits for an answer needs you", async ({ page }) => {
  await openFolder(page);
  fake.room.meta["ses_long"] = { attention: "question" };
  const s = (fake.deck.invoke("sessions.list", {}) as Record<string, unknown>[]).find((x) => x["id"] === "ses_long");
  fake.emit("session_updated", protoOf(s) as Record<string, unknown>);
  const row = sidebar(page).locator('[data-session="ses_long"]');
  await expect(row.locator('[data-status="needs-you"]')).toBeVisible();
  await expect(row.locator(".status-label")).toHaveText("Answer a question");
});

test("an older Deck: no branch, no Revert, no Archive, no races and @ lists the changed files", async ({ page }) => {
  fake.room.caps = [];
  await openFolder(page);
  await expect(page.getByRole("heading", { name: `What should we build in ${basename(dir)}?` })).toBeVisible();
  await expect(page.getByRole("switch", { name: "On its own branch" })).toHaveCount(0);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator(".room-branch")).toHaveCount(0);
  const row = sidebar(page).locator('[data-session="ses_long"]');
  await row.hover();
  await expect(row.getByRole("button", { name: "Archive Long history" })).toHaveCount(0);
  await row.click({ button: "right" });
  await expect.poll(() => menus.at(-1)?.archive).toBe("none");

  await roomTab(page, "Changes");
  await expect(page.getByRole("region", { name: "Changes" }).locator(".file")).toHaveCount(2);
  await expect(page.getByRole("button", { name: /^Revert/ })).toHaveCount(0);

  await composer(page).pressSequentially("look at @READ");
  await expect(page.getByRole("listbox", { name: "Files" })).toHaveCount(0);
  await composer(page).fill("");
  await composer(page).pressSequentially("look at @limit");
  await expect(page.getByRole("listbox", { name: "Files" }).getByRole("option")).toHaveCount(2);
  await composer(page).fill("");

  await roomTab(page, "Tasks");
  const tasks = page.getByRole("region", { name: "Tasks" });
  // An older Deck cannot race: the builder says so and sends nothing.
  await tasks.getByRole("button", { name: "New race" }).click();
  const builder = tasks.getByRole("region", { name: "Race builder" });
  await expect(builder.getByRole("switch", { name: "Judge" })).toHaveCount(0);
  await builder.getByLabel("Race task").fill("make the limiter per address");
  await builder.getByRole("button", { name: "Add agent" }).click();
  await page.getByRole("menu", { name: "Add an entrant" }).getByRole("menuitem", { name: /^Add / }).click();
  await page.getByRole("menu", { name: "Add an entrant" }).getByRole("menuitem", { name: /^Add / }).click();
  await page.keyboard.press("Escape");
  const sent = fake.deck.sent.length;
  await builder.getByRole("button", { name: /Start race \(2\)/ }).click();
  await expect(page.locator(".toast").last()).toContainText("cannot start races");
  expect(fake.deck.sent.length).toBe(sent);
  expect(fake.room.asked).toEqual([]);
  expect(fake.calls).not.toContain("ListFiles");
  expect(fake.calls).not.toContain("StartRace");
});

test("lane strip: the people in the room, a lane filter and an agent's popover", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  const lanes = page.getByRole("toolbar", { name: "Lanes" });
  // You, the roster merged with the agents tree, and the subagent of the tree.
  await expect(lanes.locator(".lane")).toHaveCount(4);
  await expect(lanes.locator('[data-lane="you"]')).toBeVisible();
  const claude = lanes.locator('[data-lane="agent:claude-1"]');
  await expect(claude).toHaveAttribute("data-status", "working");
  await expect(claude.locator(".lane-ring")).toBeVisible();
  await expect(lanes.locator('[data-lane="agent:codex-sub"] .lane-leases')).toHaveText("1");
  // A spawned agent's lane says who spawned it.
  await expect(lanes.locator('[data-lane="agent:codex-sub"] .lane-parent')).toHaveText("by @claude-1");
  await expect(lanes.locator('[data-lane="agent:codex-1"] .lane-parent')).toHaveCount(0);
  await expect(page.locator(".room-index")).toContainText("ain");

  // A click on codex-1 shows its lane alone and opens its popover.
  await lanes.locator('[data-lane="agent:codex-1"]').click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  await expect(page.locator('[data-agent="claude-1"].msg')).toHaveCount(0);
  await expect(page.locator(".msg-user")).toHaveCount(0);
  const pop = page.getByRole("menu", { name: "codex-1" });
  await expect(pop.getByRole("menuitem", { name: "Stop codex-1" })).toBeDisabled();
  await pop.getByRole("menuitem", { name: "Steer codex-1" }).click();
  await expect(composer(page)).toHaveValue("$codex-1 ");
  await expect(composer(page)).toBeFocused();

  // The person's lane shows the person's turns; a second click shows all.
  await lanes.locator('[data-lane="you"]').click();
  await expect(page.locator(".msg-agent")).toHaveCount(0);
  await lanes.locator('[data-lane="you"]').click();
  await expect(page.locator('[data-agent="claude-1"].msg').first()).toBeVisible();
});

test("an agent told to spin up agents spawns them: lanes under it, its brief as a handoff", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-agent="codex-1"].msg')).toBeVisible();
  // No add or remove chrome: agents come by mention, or by an agent.
  await expect(page.getByRole("button", { name: "Add agent" })).toHaveCount(0);
  await composer(page).fill("$claude split this up and spin up two codex agents to write the limiter tests");
  await composer(page).press("Enter");

  // claude answers, then spawns two codex agents, each a lane under it.
  const lanes = page.getByRole("toolbar", { name: "Lanes" });
  for (const name of ["codex-2", "codex-3"]) {
    const lane = lanes.locator(`[data-lane="agent:${name}"]`);
    await expect(lane.locator(".lane-parent")).toHaveText("by @claude-1", { timeout: 15_000 });
    await expect(page.locator(`[data-agent="${name}"].msg`).last()).toContainText("write the limiter tests");
  }
  // The brief shows as claude's hand-off card to each.
  await expect(page.locator('[data-agent="claude-1"].msg .handoff[aria-label="claude-1 to codex-2"]')).toBeVisible();
  expect(fake.deck.spawned.map((s) => [s.parent, s.name])).toEqual([
    ["claude-1", "codex-2"],
    ["claude-1", "codex-3"],
  ]);

  // The spawned lane's popover links to its spawner's lane.
  await lanes.locator('[data-lane="agent:codex-2"]').click();
  const pop = page.getByRole("menu", { name: "codex-2" });
  await pop.getByRole("menuitem", { name: "Spawned by @claude-1" }).click();
  await expect(lanes.locator('[data-lane="agent:claude-1"]')).toHaveAttribute("aria-pressed", "true");
  await expect(page.locator('[data-agent="codex-2"].msg')).toHaveCount(0);
  await expect(page.locator('[data-agent="claude-1"].msg .handoff[aria-label="claude-1 to codex-3"]')).toBeVisible();
});

test("sharing: the share popover, a follower's lane and a room shared with you", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  // Nobody follows yet: a quiet Share button.
  const share = page.getByRole("button", { name: "Share" });
  await expect(share.locator(".share-avatar")).toHaveCount(0);
  await share.click();
  const pop = page.getByRole("dialog", { name: "Share" });
  await pop.getByRole("radiogroup", { name: "Share with vps" }).getByRole("radio", { name: "Editor" }).click();
  await expect.poll(() => fake.team.shares).toEqual({ vps: "editor" });
  await page.keyboard.press("Escape");
  // The follower shows as an avatar and as a lane with its role.
  await expect(page.locator('.share-avatar[data-peer="vps"]')).toHaveText("VP");
  await expect(page.locator('.lane[data-lane="mate:vps"] .lane-role')).toHaveText("editor");
  // Not shared again: the lane goes.
  await page.getByRole("button", { name: "Share" }).click();
  await pop.getByRole("radiogroup", { name: "Share with vps" }).getByRole("radio", { name: "Not shared" }).click();
  await expect.poll(() => fake.team.shares).toEqual({});
  await expect(page.locator('.lane[data-lane="mate:vps"]')).toHaveCount(0);
  await page.keyboard.press("Escape");

  // The footer counts the teammates online.
  await expect(sidebar(page).getByRole("button", { name: "1 teammate online" })).toBeVisible();
  // A room vps shares with you opens in the room view; a participant writes in it.
  const row = sidebar(page).locator('[data-shared="vps/th1"]');
  await expect(row).toContainText("vps · participant");
  await row.getByRole("button").click();
  const room = page.locator(".shared-room");
  await expect(room.locator(".title")).toHaveText("Deploy the relay");
  await expect(room.locator('.shared-row[data-who="claude2"]')).toContainText("building the image");
  await room.getByLabel("Post to the shared room").fill("ping me when it is up");
  await room.getByLabel("Post to the shared room").press("Enter");
  await expect(room.locator('.shared-row[data-who="laptop"]')).toContainText("ping me when it is up");
});

test("team: presence, chat and contracts", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await sidebar(page).getByRole("button", { name: "1 teammate online" }).click();
  const team = page.locator("[data-team]");
  await expect(team.locator('.teammate[data-peer="vps"]')).toContainText("fix the flaky test");
  // The team chat.
  await expect(team.locator(".team-chat")).toContainText("I am on the flaky test.");
  await team.getByLabel("Message the team").fill("shipping at five");
  await team.getByLabel("Message the team").press("Enter");
  await expect(team.locator(".team-chat")).toContainText("shipping at five");
  // A teammate's contract, accepted with codex; one proposed for this thread.
  await team.getByLabel("Agent for the contract").selectOption("codex");
  await team.locator('.contract[data-contract="c1"]').getByRole("button", { name: "Accept" }).click();
  await expect(team.locator('.contract[data-contract="c1"]')).toContainText("accepted · codex");
  await team.getByLabel("Contract brief").fill("review the limiter");
  await team.getByRole("button", { name: "Ask" }).click();
  await expect(page.locator(".toast")).toContainText("Asked vps (task t9)");
  // A shared room opens from the team page too.
  await team.locator('.teammate[data-peer="vps"]').getByRole("button", { name: "Open" }).click();
  await expect(page.locator(".shared-room .title")).toHaveText("Deploy the relay");
});

test("attention: a room that needs you, the inbox, notifications, and a finished room", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  const long = sidebar(page).locator('[data-session="ses_long"]');
  // The window is in the background from here on.
  focused = false;

  // A notice for a room off screen waits in its status, not in a toast; one
  // for the open room toasts, and the toast can be dismissed. The engine
  // marks its overlap notes.
  const overlap = "codex-2 and claude-1 both changed internal/auth/auth.go in the last few minutes; one may undo the other";
  fake.emit("notice", { session_id: "ses_long", text: overlap, via: "overlap" });
  await expect(long.locator('[data-status="overlap"]')).toBeVisible();
  await expect(long.locator(".status-label")).toHaveText("codex-2 and claude-1 both changed auth.go");
  fake.deck.notice("ses_main", "Saved a checkpoint");
  const toast = page.locator(".toast").filter({ hasText: "Saved a checkpoint" });
  await expect(toast).toBeVisible();
  await expect(page.locator(".toast").filter({ hasText: "both changed" })).toHaveCount(0);
  await toast.getByRole("button", { name: "Dismiss" }).click();
  await expect(toast).toHaveCount(0);

  // An approval: the room needs you, says what for, and a notification
  // names the project and the room.
  const asked = fake.deck.requestApproval("ses_long", "claude-1", "rm -rf build");
  await expect(long.locator('[data-status="needs-you"]')).toBeVisible();
  await expect(long.locator(".status-label")).toHaveText("Approve command");
  await expect.poll(() => notes.map((n) => `${n.title}: ${n.body}`)).toContain(`${basename(dir)} · Long history: Approve command`);
  expect(notes[0]?.tag).toContain("ses_long");
  await expect.poll(() => badges.at(-1) ?? 0).toBeGreaterThanOrEqual(2);
  // The bell in the sidebar's foot counts it, and its panel lists it.
  const bell = page.getByRole("button", { name: /^Notifications, \d+ unread$/ });
  await bell.click();
  const panel = page.getByRole("dialog", { name: "Notifications" });
  await expect(panel.locator('[data-notification="approval"]').filter({ hasText: "Long history" })).toContainText("Approve command");  await panel.getByRole("button", { name: "Mark all read" }).click();
  await expect(page.getByRole("button", { name: "Notifications", exact: true })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect(panel).toHaveCount(0);

  // Ctrl+Alt+L goes to the newest room that needs you; opening it does not
  // clear the state, only an answer does.
  await page.keyboard.press("Control+Alt+l");
  await expect(page.locator(".title")).toHaveText("Long history");
  await expect(long.locator('[data-status="needs-you"]')).toBeVisible();
  // While Ctrl is held the rows show their keys; Ctrl+2 opens the second.
  await page.keyboard.down("Control");
  await expect(sidebar(page).locator(".row-digit").first()).toHaveText("1");
  await page.keyboard.press("2");
  await page.keyboard.up("Control");
  await expect(page.locator(".title")).toHaveText("Docs pass");
  await expect(sidebar(page).locator(".row-digit")).toHaveCount(0);

  // The inbox lists both approvals, newest first; y approves the selected one.
  await sidebar(page).getByRole("button", { name: /^Inbox/ }).click();
  const inbox = page.getByRole("listbox", { name: "Waiting for you" });
  const first = inbox.getByRole("option").first();
  await expect(first).toContainText("Long history");
  await expect(first).toContainText("Approve command");
  await expect(first).toContainText("rm -rf build");
  await expect(inbox.getByRole("option").filter({ hasText: "Docs pass" })).toContainText("go test ./...");
  await expect(inbox).toBeFocused();
  await page.keyboard.press("y");
  await expect.poll(() => fake.deck.answered).toEqual([{ id: asked.id, decision: "allow" }]);
  await expect(long.locator('[data-status="needs-you"]')).toHaveCount(0);
  await expect(inbox.getByRole("option").filter({ hasText: "rm -rf build" })).toHaveCount(0);

  // A room that finishes out of sight is done until it is opened.
  fake.deck.invoke("chat.send", { session_id: "ses_long", text: "tidy the notes" });
  await expect(long.locator('[data-status="done"]')).toBeVisible();
  await expect.poll(() => notes.map((n) => `${n.title}: ${n.body}`)).toContain(`${basename(dir)} · Long history: Finished`);
  const done = inbox.locator('[data-state="done"]').filter({ hasText: "Long history" });
  await expect(done).toContainText("Finished");
  await done.click();
  await page.keyboard.press("Enter");
  await expect(page.locator(".title")).toHaveText("Long history");
  await expect(long.locator('[data-status="done"]')).toHaveCount(0);
});
