import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { Question } from "../../../pkg/remote/web/src/protocol";
import type { NotifyInput } from "../../src/shared/bridge";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge } from "./harness";

// An agent's question (Claude's AskUserQuestion) over the rpc transport: the
// room needs you, the inbox and the bell name it, the card over the
// composer takes the answer, and the agent hears it.
let fake: FakeRpcDeck;
let host: TestHost;
let dir: string;
let notes: NotifyInput[] = [];

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  notes = [];
  host = await attachBridge(page, async () => dir, { focused: () => true, notify: (n) => notes.push(n) });
});

test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const sidebar = (page: Page) => page.getByRole("navigation", { name: "Threads" });
// Where the screenshots go: DECK_SHOTS, else the temp folder.
const shot = (name: string) => join(process.env["DECK_SHOTS"] ?? join(tmpdir(), "deck-shots"), name);

const DELIVERY: Question = {
  id: "The reviews are done but only reported here. How should I deliver them?",
  header: "Delivery",
  text: "The reviews are done but only reported here. How should I deliver them?",
  options: [
    { label: "Post as comments", description: "One review comment per finding on the PR" },
    { label: "Summary only", description: "A single PR comment with the list" },
  ],
  other: true,
};
const CHECKS: Question = {
  id: "Which checks should I re-run first?",
  header: "Checks",
  text: "Which checks should I re-run first?",
  options: [
    { label: "Lint", description: "npm run lint" },
    { label: "Unit", description: "npm test" },
    { label: "E2E", description: "Playwright" },
  ],
  multi_select: true,
  other: true,
};

async function openFolder(page: Page): Promise<void> {
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".link")).toHaveText("Connected");
}

test("a question: needs you, the inbox and the bell, the card, and the agent hears the answer", async ({ page }) => {
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Docs pass", exact: true }).click();
  const asked = fake.deck.requestQuestion("ses_main", "claude-1", [DELIVERY, CHECKS]);

  // The room needs you: "Answer claude-1", and the bell says so.
  const row = sidebar(page).locator('[data-session="ses_main"]');
  await expect(row.locator('[data-status="needs-you"]')).toBeVisible();
  await expect(row.locator(".status-label")).toHaveText("Answer claude-1");
  const bell = page.getByRole("button", { name: /^Notifications, \d+ unread$/ });
  await bell.click();
  const panel = page.getByRole("dialog", { name: "Notifications" });
  await expect(panel.locator('[data-notification="approval"]').filter({ hasText: "Fix the pairing lockout" })).toContainText("Answer claude-1");
  await page.keyboard.press("Escape");

  // The inbox shows the question with its options.
  await sidebar(page).getByRole("button", { name: /^Inbox/ }).click();
  const inbox = page.getByRole("listbox", { name: "Waiting for you" });
  const item = inbox.getByRole("option").filter({ hasText: "Fix the pairing lockout" });
  await expect(item).toContainText("Answer claude-1");
  await expect(item.getByRole("radio", { name: /Post as comments/ })).toBeVisible();
  await expect(item.getByRole("button", { name: "Submit" })).toBeDisabled();

  // In the room, the card rides on the composer and has the keys.
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  const card = page.locator("[data-question]").filter({ hasText: "Delivery" });
  await expect(card).toBeVisible();
  await expect(card.getByText(DELIVERY.text)).toBeVisible();
  await expect(card.getByText("One review comment per finding on the PR")).toBeVisible();
  // The conversation says it waits.
  await expect(page.locator('.msg-question[data-question="pending"]')).toContainText(DELIVERY.text);
  await settle(page);
  await page.screenshot({ path: shot("question-card-dark.png") });

  // 1 picks the first option and moves the keys to the next question.
  await expect(card).toBeFocused();
  await page.keyboard.press("1");
  await expect(card.getByRole("radio", { name: /Post as comments/ })).toHaveAttribute("aria-checked", "true");
  await page.keyboard.press("2");
  await expect(card.getByRole("checkbox", { name: /Unit/ })).toHaveAttribute("aria-checked", "true");
  await card.getByRole("checkbox", { name: /Lint/ }).click();
  await card.getByRole("checkbox", { name: /Lint/ }).click();
  await card.getByRole("textbox", { name: `Other answer: ${CHECKS.text}` }).fill("go vet");
  await page.screenshot({ path: shot("question-card-picked-dark.png") });
  await page.keyboard.press("Enter");

  // The Deck got the answers by question...
  await expect.poll(() => fake.deck.answered).toEqual([
    {
      id: asked.id,
      decision: "answer",
      answers: [
        { id: DELIVERY.id, selected: ["Post as comments"] },
        { id: CHECKS.id, selected: ["Unit"], other: "go vet" },
      ],
    },
  ]);
  // ...the card leaves one line, the conversation records it and the agent
  // goes on with what it heard.
  await expect(card).toHaveCount(0);
  await expect(page.locator("[data-answered]")).toContainText("You answered: Post as comments; Unit, go vet");
  await expect(page.locator('.msg-question[data-question="settled"]')).toContainText("You answered: Post as comments; Unit, go vet");
  await expect(page.locator('[data-agent="claude-1"].msg').last()).toContainText("Got it: Post as comments; Unit, go vet.");
  await expect(row.locator('[data-status="needs-you"]')).toHaveCount(0);
  await settle(page);
  await page.screenshot({ path: shot("question-answered-dark.png") });

  // Skip declines the next one.
  const again = fake.deck.requestQuestion("ses_main", "claude-1", [DELIVERY]);
  const next = page.locator("[data-question]");
  await expect(next).toBeVisible();
  await expect(page.locator("[data-answered]")).toHaveCount(0);
  await next.getByRole("button", { name: "Skip" }).click();
  await expect.poll(() => fake.deck.answered.at(-1)).toEqual({ id: again.id, decision: "deny" });
  await expect(page.locator("[data-answered]")).toContainText("You skipped it");
  await expect(page.locator('.msg-question[data-question="settled"]').last()).toContainText("You skipped it");
  await page.locator("[data-answered]").getByRole("button", { name: "Dismiss" }).click();
  await expect(page.locator("[data-answered]")).toHaveCount(0);
});

// Lets the composer's cards finish coming in before a screenshot.
async function settle(page: Page): Promise<void> {
  await page.evaluate(() =>
    Promise.all(
      document
        .getAnimations()
        .filter((a) => a.effect?.getComputedTiming().iterations !== Infinity)
        .map((a) => a.finished.catch(() => undefined)),
    ),
  );
  await page.mouse.move(2, 600);
}

test("the question card in the light theme, and its record", async ({ page }) => {
  await page.emulateMedia({ colorScheme: "light" });
  await page.addInitScript(() => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: "light" })));
  await openFolder(page);
  await sidebar(page).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  fake.deck.requestQuestion("ses_main", "claude-1", [DELIVERY, CHECKS]);
  const card = page.locator("[data-question]");
  await expect(card).toBeVisible();
  await settle(page);
  await page.screenshot({ path: shot("question-card-light.png") });
  await card.getByRole("radio", { name: /Summary only/ }).click();
  await card.getByRole("checkbox", { name: /E2E/ }).click();
  await card.getByRole("button", { name: "Submit" }).click();
  await expect(page.locator("[data-answered]")).toContainText("You answered: Summary only; E2E");
  await expect(page.locator('[data-agent="claude-1"].msg').last()).toContainText("Got it: Summary only; E2E.");
  await settle(page);
  await page.screenshot({ path: shot("question-answered-light.png") });
});
