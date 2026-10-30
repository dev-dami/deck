import { mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import { type TestHost } from "../bridge";
import { FakeRpcDeck, subThreadAtWork } from "../fake-grpc";
import { attachBridge } from "./harness";

// Screenshots of the single-agent default and the workflow menu, for design
// review: the new-thread screen, the menu open, a workflow picked, a room of
// one agent, and a crew's room with its hand-off cards.
// SHOTS=<dir> npx playwright test workflow-shots; without SHOTS the e2e run
// skips them.
const OUT = process.env["SHOTS"] ?? "";
test.skip(OUT === "", "SHOTS names the folder for the screenshots");

let fake: FakeRpcDeck;
let host: TestHost;
test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-"));
  host = await attachBridge(page, async () => dir);
});
test.afterEach(async () => {
  host.decks.closeAll();
  await fake.close();
});

const shot = (page: Page, name: string) => page.screenshot({ path: join(OUT, `${name}.png`) });
const composer = (page: Page) => page.getByRole("textbox", { name: "Message" });

for (const mode of ["dark", "light"] as const) {
  test(`workflow shots ${mode}`, async ({ page }) => {
    await page.emulateMedia({ colorScheme: mode });
    await page.addInitScript((m) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m })), mode);
    await page.goto("/");
    await page.getByRole("button", { name: "Open a folder" }).click();
    await expect(page.getByRole("heading", { name: /What should we build/ })).toBeVisible();
    await page.mouse.move(2, 600);
    await page.waitForTimeout(300);
    await shot(page, `${mode}-1-new-thread-single-agent`);

    await page.getByRole("button", { name: "Start a multi-agent workflow" }).click();
    await page.waitForTimeout(400);
    await shot(page, `${mode}-2-workflow-menu`);

    await page.getByRole("list", { name: "Workflows" }).getByRole("button", { name: /Plan, build, review/ }).click();
    await composer(page).fill("Fix the login timeout and add a regression test");
    await page.waitForTimeout(300);
    await shot(page, `${mode}-3-workflow-picked`);
    await page.getByRole("button", { name: "Back to one agent" }).click();

    // The agent menu: the one agent the thread talks to, its model and effort.
    await page.getByRole("button", { name: /^Agent: / }).click();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-3b-agent-menu`);
    await page.keyboard.press("Escape");

    // A room of one agent: no lanes strip, like the agent's own CLI.
    fake.crew = { agents: [{ name: "claude-1", kind: "claude", model: "opus", status: "idle", depth: 0 }], running: [] };
    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Docs pass", exact: true }).click();
    await expect(page.locator('[data-agent="claude-1"].msg').first()).toBeVisible();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-4-solo-room`);

    // A crew's room: lanes, and the agents' messages to each other as cards.
    fake.deck.reply("ses_main", "claude-1", "→ $codex-1\n\nReview the limiter change in `pkg/remote/auth/limit.go`; check that a valid token is never blocked.");
    fake.deck.reply("ses_main", "codex-1", '→ $claude-1\n\ntask t2 "Key failures by address" done: keyed by addr, with TestLimiterIsPerAddress; go test ./pkg/remote/auth passes.');
    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await expect(page.locator(".handoff").first()).toBeVisible();
    await page.locator(".handoff").last().scrollIntoViewIfNeeded();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-5-crew-room-handoffs`);

    // A sub-thread: nested under its parent, its report a card that opens it.
    const sub = subThreadAtWork(fake);
    await page.reload();
    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await expect(page.locator('.handoff[data-handoff="subthread"]')).toBeVisible();
    await page.locator('.handoff[data-handoff="subthread"]').scrollIntoViewIfNeeded();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-6-subthread-report`);
    await page.locator('.handoff[data-handoff="subthread"]').getByRole("button", { name: /^Open / }).click();
    await expect(page.locator(".room-parent")).toContainText("Sub-thread of Fix the pairing lockout");
    await expect(page.locator(`[data-session="${sub}"][data-sub]`)).toBeVisible();
    await page.waitForTimeout(300);
    await shot(page, `${mode}-7-subthread-room`);
  });
}
