import { existsSync, mkdtempSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { _electron as electron, expect, test } from "@playwright/test";
import { FakeDeck } from "../fake-server";

const root = join(__dirname, "..", "..");
const built = existsSync(join(root, "dist", "app", "main", "main.js"));
const display = process.platform !== "linux" || process.env["DISPLAY"] !== undefined;

// The whole app: main, preload, IPC, the app:// protocol and the CSP. Run it
// with `npm run e2e` under a display (xvfb-run on a headless Linux).
test.skip(!built || !display, "needs `npm run build` and a display");

test("the Electron app pairs, connects and runs two agents", async () => {
  const fake = await FakeDeck.start({ tickMs: 300 });
  // Root in a container cannot use Chromium's sandbox; a desktop user can.
  const args = [root, `--user-data-dir=${mkdtempSync(join(tmpdir(), "deck-desktop-"))}`];
  if (process.getuid?.() === 0) args.push("--no-sandbox");
  const app = await electron.launch({ args, cwd: root });
  try {
    const win = await app.firstWindow();
    await win.setViewportSize({ width: 1360, height: 860 });
    await expect(win.getByRole("heading", { name: "Connect to Deck" })).toBeVisible();

    // No Node in the page, and no network of its own.
    expect(await win.evaluate(() => typeof (globalThis as { require?: unknown }).require)).toBe("undefined");
    const fetched = await win.evaluate(async (url) => {
      try {
        await fetch(url);
        return "reached";
      } catch {
        return "blocked";
      }
    }, `http://127.0.0.1:${fake.port}/api/v1/health`);
    expect(fetched).toBe("blocked");

    await win.getByLabel("Pairing link or invite line").fill(fake.pairLink);
    await win.getByRole("button", { name: "Pair", exact: true }).click();
    await expect(win.locator(".project")).toHaveText("deck");
    const servers = await win.evaluate(() =>
      (window as unknown as { deck: { listServers: () => Promise<unknown> } }).deck.listServers(),
    );
    expect(JSON.stringify(servers)).not.toMatch(/token/i);

    await win.getByRole("navigation", { name: "Sessions" }).getByRole("button", { name: /Fix the pairing lockout/ }).click();
    const box = win.getByRole("textbox", { name: "Message" });
    await box.fill("$claude $codex+ split the work and compare");
    await box.press("Enter");
    await expect(win.locator('.agent[data-agent="codex-2"]')).toBeVisible();
    await expect(win.locator('[data-agent="claude-1"].msg').last().locator(".streaming")).toBeVisible();
    await win.locator(".panel-toggles").getByRole("button", { name: /Approvals/ }).click();
    await expect(win.locator(".approval")).toHaveCount(1);
    await win.waitForTimeout(700);
    await win.screenshot({ path: join(root, "docs", "screenshot.png") });
  } finally {
    await app.close();
    await fake.close();
  }
});
