import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { NotifyInput } from "../../src/shared/bridge";
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
// Whether the window has the focus, and the notifications main showed.
let focused = true;
let notes: NotifyInput[] = [];
let badges: number[] = [];

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  picks = [];
  focused = true;
  notes = [];
  badges = [];
  host = await attachBridge(page, async () => picks.shift() ?? dir, {
    focused: () => focused,
    notify: (n) => notes.push(n),
    badge: (c) => badges.push(c),
  });
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

// Opens a tab of the room's side panel.
async function roomTab(page: Page, name: string): Promise<void> {
  const panel = page.getByRole("complementary", { name: "Side panel" });
  if ((await panel.count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel.getByRole("tab", { name }).click();
}

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
  await expect(page.getByRole("complementary", { name: "Side panel" }).getByRole("tab")).toHaveText(["Changes", "Tasks1", "Agents", "Side chat"]);

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
  await tasks.getByRole("switch", { name: "Orchestrate" }).click();
  await expect(tasks.getByRole("switch", { name: "Orchestrate" })).toHaveAttribute("aria-checked", "true");
  expect(fake.board.actions).toEqual(["apply t3", "retry t5"]);
  // The race form sends the composer's syntax.
  await tasks.getByRole("button", { name: "New race" }).click();
  await tasks.getByLabel("Race brief").fill("make the limiter per address");
  await tasks.getByLabel("Race kind").selectOption("codex");
  await tasks.getByRole("button", { name: "More entries" }).click();
  await tasks.getByRole("button", { name: /Start race \(4\)/ }).click();
  await expect.poll(() => fake.deck.sent.at(-1)?.text).toBe("$codex×4 make the limiter per address");
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
  await expect(page.locator(".room-index")).toContainText("mai");

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
  // for the open room toasts, and the toast can be dismissed.
  fake.deck.notice("ses_long", "codex-2 and claude-1 both changed internal/auth/auth.go in the last few minutes; one may undo the other");
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
