import { mkdtempSync } from "node:fs";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge, type PreviewCall, stubPreview } from "./harness";

// Ports and the Preview tab in the renderer. The view itself needs Electron
// (electron.spec.ts); here main's preview records what the tab asks.
let fake: FakeRpcDeck;
let host: TestHost;
let calls: PreviewCall[];
let server: Server;
let port: number;

test.beforeEach(async ({ page }) => {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  const stub = stubPreview();
  calls = stub.calls;
  host = await attachBridge(page, async () => dir, { preview: stub.preview });
  server = createServer((_req, res) => res.end("<title>Fixture</title><h1>Preview fixture</h1>"));
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  port = (server.address() as AddressInfo).port;
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
});

test.afterEach(async () => {
  host.decks.closeAll();
  server.close();
  await fake.close();
});

// Types a line in the drawer's first terminal; xterm keeps the keys while it has the focus.
async function announce(page: Page, line: string): Promise<void> {
  await page.keyboard.press("Control+j");
  const drawer = page.getByRole("region", { name: "Terminal" });
  await expect(drawer.locator(".xterm-rows")).toContainText("fake shell");
  await drawer.locator(".xterm").click();
  await page.keyboard.type(line);
  await page.keyboard.press("Enter");
}

test("a port a terminal prints shows as a chip, opens in the Preview tab and goes when the server stops", async ({ page }) => {
  await announce(page, `echo Local: http://localhost:${port}/app`);
  const header = page.getByRole("group", { name: "Dev servers" });
  await expect(header.getByRole("button", { name: `Preview :${port}` })).toBeVisible();
  // The terminal tab that printed it has the chip and a Stop button.
  const drawer = page.getByRole("region", { name: "Terminal" });
  await expect(drawer.getByRole("button", { name: `Preview :${port}` })).toBeVisible();
  await expect(drawer.getByRole("button", { name: "Stop the dev server in terminal 1" })).toBeVisible();

  // The Preview tab shows only while a port is open.
  await page.getByRole("button", { name: "Show the side panel" }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  await expect(panel.getByRole("tab", { name: "Preview" })).toBeVisible();
  await panel.getByRole("tab", { name: "Changes" }).click();

  await header.getByRole("button", { name: `Preview :${port}` }).click();
  await expect(panel.getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");
  const preview = panel.getByRole("region", { name: "Preview" });
  await expect(preview.locator(".preview-host")).toHaveText(`localhost:${port}`);
  await expect(preview.getByRole("textbox", { name: "Path" })).toHaveValue("/app");
  await expect.poll(() => calls.find((c) => c[0] === "open")).toEqual(["open", `http://localhost:${port}/app`]);
  await expect
    .poll(() => calls.findLast((c) => c[0] === "bounds" || c[0] === "hide"))
    .toEqual(["bounds", expect.objectContaining({ zoom: 1, w: expect.any(Number) })]);

  // A device wider than the panel zooms the page out.
  await preview.getByRole("button", { name: "1280" }).click();
  await expect
    .poll(() => {
      const b = calls.findLast((c) => c[0] === "bounds");
      return b?.[0] === "bounds" ? b[1].zoom : null;
    })
    .toBeLessThan(1);

  // A new path goes to main as a path.
  await preview.getByRole("textbox", { name: "Path" }).fill("/docs");
  await preview.getByRole("textbox", { name: "Path" }).press("Enter");
  await expect.poll(() => calls.some((c) => c[0] === "nav" && c[1].action === "path" && c[1].path === "/docs")).toBe(true);

  // The palette covers the view: main hides it. It lists the port.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press("Control+k");
  await expect.poll(() => calls.at(-1)).toEqual(["hide"]);
  await page.getByRole("dialog", { name: "Commands" }).locator("input").fill("preview");
  await expect(page.getByRole("option", { name: new RegExp(`Preview :${port}`) })).toBeVisible();
  await page.keyboard.press("Escape");
  await expect.poll(() => calls.at(-1)?.[0]).toBe("bounds");

  // Stop sends Ctrl+C to the terminal.
  const before = fake.calls.filter((m) => m === "TerminalWrite").length;
  await drawer.getByRole("button", { name: "Stop the dev server in terminal 1" }).click();
  await expect.poll(() => fake.calls.filter((m) => m === "TerminalWrite").length).toBe(before + 1);

  // The server stops: the chip and the tab go at the next probe.
  server.close();
  server.closeAllConnections();
  await expect(header).toBeHidden({ timeout: 10_000 });
  await expect(page.getByRole("button", { name: `Preview :${port}` })).toHaveCount(0);
});

test("a port in an agent's tool output shows too, and a port nothing listens on does not", async ({ page }) => {
  await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
  await announce(page, "echo listening on port 1");
  fake.deck.toolRun("ses_main", "codex-1", "npm run dev", `\x1b[32mVITE\x1b[0m ready\n  ➜  Local:   http://localhost:${port}/\n`);
  const chips = page.getByRole("group", { name: "Dev servers" }).locator(".port-chip");
  await expect(chips).toHaveCount(1);
  await expect(chips.first()).toHaveAttribute("data-port", String(port));
  await expect(chips.first().getByRole("button", { name: `Preview :${port}` })).toHaveAttribute("title", `http://localhost:${port}/, from codex-1`);
  // The palette opens it in the Preview tab.
  await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
  await page.keyboard.press("Control+k");
  await page.getByRole("dialog", { name: "Commands" }).locator("input").fill(`Preview :${port}`);
  await page.keyboard.press("Enter");
  await expect(page.getByRole("complementary", { name: "Side panel" }).getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");
  await expect.poll(() => calls.find((c) => c[0] === "open")).toEqual(["open", `http://localhost:${port}/`]);
});
