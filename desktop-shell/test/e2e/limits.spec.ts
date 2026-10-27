import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { NotifyInput } from "../../src/shared/bridge";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { analyticsRecord } from "../fake-usage";
import { attachBridge } from "./harness";

// Plan limits in the window: checking them, starting a rolling window, an
// agent waiting for its reset, a message scheduled for it, the quota chips
// and the limit notifications. SHOTS=<dir> also saves screenshots.
const OUT = process.env["SHOTS"] ?? "";
const H = 3_600_000;

let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;
let notes: NotifyInput[] = [];
// The notifications about plan limits (the rooms tell of other things).
const limitNotes = () => notes.filter((n) => n.title.includes("Plan limits") || /limit|window/.test(n.body));

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  notes = [];
  host = await attachBridge(page, async () => dir, { focused: () => false, notify: (n) => notes.push(n) });
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });
const shot = async (page: Page, name: string) => {
  if (OUT !== "") await page.screenshot({ path: join(OUT, `${name}.png`) });
};

// The windows a Deck would report: Claude's 5 hours at used% (reset in
// resetIn ms, or none running when resetIn is negative) and its week, and
// Codex's two.
function windows(used: number, resetIn: number): Record<string, unknown>[] {
  const now = Date.now();
  const row = (kind: string, id: string, wkind: string, label: string, pct: number, resetsAt: number, mins: number) => ({
    agent_kind: kind,
    window_id: id,
    kind: wkind,
    label,
    used_pct: pct,
    resets_at: resetsAt,
    duration_mins: mins,
    plan: kind === "claude" ? "max" : "pro",
    at: now - 4 * 60_000,
  });
  return [
    row("claude", "five_hour", "session", "5h", used, resetIn < 0 ? now - H : now + resetIn, 300),
    row("claude", "seven_day", "weekly", "Weekly", 41, now + 60 * H, 10_080),
    row("codex", "primary", "session", "5h", 12, now + 3 * H, 300),
    row("codex", "secondary", "weekly", "Weekly", 30, now + 90 * H, 10_080),
  ];
}

async function openFolder(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  await expect(page.locator(".link")).toHaveText("Connected");
}

async function analytics(page: Page) {
  await sidebar(page).getByRole("button", { name: "Usage" }).click();
  const usage = page.locator("[data-usage]");
  await usage.getByRole("radio", { name: "Analytics" }).click();
  return usage;
}

test("check limits, say plainly who can't be checked, and start a window that is not running", async ({ page }) => {
  fake.serveLimits(windows(0, -1));
  await openFolder(page);
  const usage = await analytics(page);
  const claude = usage.locator('[data-kind-section="claude"]');
  // No 5-hour window runs: it can be started; Codex's runs, so it can't.
  const start = claude.locator('[data-start-window="claude"]');
  await expect(start).toBeVisible();
  await expect(start).toHaveAttribute("title", /uses a little of your plan/);
  await expect(usage.locator('[data-start-window="codex"]')).toHaveCount(0);

  await usage.locator("[data-check-limits]").click();
  await expect(usage.locator("[data-limits-note]")).toHaveText("Opencode can't be checked: it does not report plan limits.");
  await expect(claude.locator('[data-checked="claude"]')).toHaveText("checked just now");
  await expect(usage.locator('[data-checked="opencode"]')).toContainText("can't be checked");
  expect(fake.lim.asked).toContain("check ");
  await shot(page, "analytics-checked");

  // One kind's own check.
  await usage.locator('[data-check-kind="codex"]').click();
  await expect.poll(() => fake.lim.asked).toContain("check codex");

  await start.click();
  await expect(usage.locator("[data-limits-note]")).toHaveText("Started Claude's window with haiku.");
  await expect.poll(() => fake.lim.asked).toContain("start claude");
  await expect(start).toHaveCount(0);
});

test("the palette checks limits and starts a window", async ({ page }) => {
  fake.serveLimits(windows(0, -1));
  await openFolder(page);
  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog", { name: "Commands" });
  await palette.getByRole("textbox").fill("> start claude");
  await palette.getByRole("option", { name: /Start Claude's window now/ }).click();
  await expect.poll(() => fake.lim.asked).toContain("start claude");
  await page.keyboard.press("Control+k");
  await palette.getByRole("textbox").fill("> check plan");
  await palette.getByRole("option", { name: /Check plan limits/ }).click();
  await expect(page.locator(".toast").filter({ hasText: "Opencode can't be checked" })).toBeVisible();
});

test("an agent waiting for its reset: the banner counts down, Resume now resumes it", async ({ page }) => {
  const until = Date.now() + 2 * H + 13 * 60_000 + 20_000;
  fake.serveLimits(windows(100, until - Date.now()), [
    { session_id: "ses_main", agent: "claude-1", kind: "claude", since: Date.now() - 60_000, until, window: "five_hour" },
  ]);
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  const banner = page.locator("[data-wait-banner]");
  await expect(banner).toContainText(/claude-1 · Waiting for reset · \d{1,2}:\d{2}\s?[AP]M \(in 2h 13m\)/);
  // The lane chip shows the wait; Codex's quota is low.
  await expect(page.locator('[data-lane] [data-quota="waiting"]')).toHaveCount(1);
  await expect(page.locator('[data-lane="agent:codex-1"] [data-quota="ok"]')).toBeVisible();
  // The composer says the room's fullest plan is out.
  await expect(page.locator('.room-quota[data-quota="out"]')).toHaveText("Claude 100%");
  await shot(page, "room-waiting");

  // A draft can wait for the reset too.
  await composer(page).fill("then run the race detector");
  await page.getByRole("button", { name: "More", exact: true }).click();
  await page.locator("[data-send-at-reset]").click();
  await expect.poll(() => fake.lim.asked).toContain("schedule ses_main then run the race detector");
  await expect(composer(page)).toHaveValue("");
  await expect(page.locator(".toast").filter({ hasText: "Scheduled for" })).toBeVisible();
  await expect(banner).toContainText("1 scheduled message");

  await banner.getByRole("button", { name: "Resume now" }).click();
  await expect.poll(() => fake.lim.asked).toContain("resume ses_main claude-1");
  await expect(banner).toHaveCount(0);
});

test("a scheduled message shows in the Tasks tab and can be sent now", async ({ page }) => {
  fake.serveLimits(windows(80, 2 * H));
  fake.lim.scheduled.push({ id: "at-9", session_id: "ses_main", text: "carry on with the docs", at: Date.now() + 2 * H, created: Date.now() });
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  if ((await panel.count()) === 0) await page.getByRole("button", { name: "Show the side panel" }).click();
  await panel.getByRole("tab", { name: "Tasks" }).click();
  const item = panel.locator('[data-scheduled="at-9"]');
  await expect(item).toContainText("carry on with the docs");
  await expect(item).toContainText("(in 2h)");
  await shot(page, "tasks-scheduled");
  await item.getByRole("button", { name: "Send now" }).click();
  await expect.poll(() => fake.lim.asked).toContain("send at-9");
  await expect(item).toHaveCount(0);
});

test("crossing a threshold, and the limit, tells the person once each", async ({ page }) => {
  fake.serveLimits(windows(50, 3 * H));
  await openFolder(page);
  const usage = await analytics(page);
  // The first look is the starting point: nothing to tell.
  await expect(usage.locator('[data-kind-section="claude"] [data-limit-controls="claude"]')).toBeVisible();
  await page.waitForTimeout(500);
  expect(limitNotes()).toEqual([]);

  // Claude climbs past 90%: a check brings it in.
  fake.lim.limits.splice(0, Infinity, ...windows(93, 3 * H));
  await usage.locator("[data-check-limits]").click();
  await expect.poll(() => limitNotes().map((n) => n.body)).toContainEqual(expect.stringMatching(/Claude has used 93% of its 5h window · resets /));
  // Again at the same level: nothing new.
  await usage.locator("[data-check-limits]").click();
  await page.waitForTimeout(1500);
  expect(limitNotes()).toHaveLength(1);

  // The limit itself.
  fake.lim.limits.splice(0, Infinity, ...windows(100, 3 * H));
  await usage.locator("[data-check-limits]").click();
  await expect.poll(() => limitNotes().map((n) => n.body)).toContainEqual(expect.stringMatching(/Claude hit its 5h limit · resets /));
  const bell = page.getByRole("button", { name: /^Notifications, \d+ unread$/ });
  await bell.click();
  const panel = page.getByRole("dialog", { name: "Notifications" });
  await expect(panel.locator('[data-notification="limit"]').first()).toContainText("Plan limits");
  await shot(page, "notifications-limit");
  // A limit entry opens the Usage page.
  await panel.locator('[data-notification="limit"]').first().getByRole("button").first().click();
  await expect(page.locator("[data-usage]")).toBeVisible();
});

test("the quota chip turns amber, then red, near the limit", async ({ page }) => {
  // Seed the record so the analytics have history, then a warm window.
  const rec = analyticsRecord(Date.now(), 1);
  fake.serveLimits(windows(78, 3 * H));
  fake.lim.history.push(...rec.limit_history.filter((l) => l["agent_kind"] === "codex"));
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await expect(page.locator('[data-lane="agent:claude-1"] [data-quota="warn"]')).toBeVisible();
  await expect(page.locator('.room-quota[data-quota="warn"]')).toHaveText("Claude 78%");
  fake.lim.limits.splice(0, Infinity, ...windows(94, 3 * H));
  // Settings' thresholds move it: at 75/90, 94 is red.
  await page.keyboard.press("Control+k");
  const palette = page.getByRole("dialog", { name: "Commands" });
  await palette.getByRole("textbox").fill("> check claude");
  await palette.getByRole("option", { name: /Check Claude's limits/ }).click();
  await expect(page.locator('[data-lane="agent:claude-1"] [data-quota="crit"]')).toBeVisible();
  await shot(page, "room-quota-crit");
});
