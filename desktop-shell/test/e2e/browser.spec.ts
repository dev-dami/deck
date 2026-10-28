import { mkdtempSync } from "node:fs";
import { createServer, type Server } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { expect, type Page, test } from "@playwright/test";
import type { Browser } from "../../src/main/browser";
import type { TestHost } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { attachBridge, type PwPage, stubBrowser } from "./harness";

// The Preview tab as a small browser, and the agents that use it. The
// renderer, main's browser (tabs, refs, limits, policy) and the desktop's end
// of the engine's channel are real; the pages are Chromium pages driven over
// the same protocol as the app's (harness.ts), and the engine is the fake,
// which sends an agent's op down the channel and reads the answer back.
let fake: FakeRpcDeck;
let host: TestHost;
let browser: Browser;
let pages: PwPage[];
let server: Server;
let port: number;
let origin: string;

const SHOP = `<!doctype html><title>Shop</title><h1>Welcome</h1><p>Sign in below.</p>
<nav><a href="/two">Second page</a></nav>
<form onsubmit="event.preventDefault();document.getElementById('out').textContent='submitted '+document.getElementById('email').value">
<label>Email <input id="email" type="email"></label>
<label>Password <input id="pw" type="password"></label>
<label><input type="checkbox" id="cb"> Remember me</label>
<button id="go">Sign in</button></form><div id="out" role="status"></div>`;

async function start(page: Page, limits: Parameters<typeof stubBrowser>[1] = {}) {
  fake = await FakeRpcDeck.start({ tickMs: 250 });
  fake.advertiseBrowser = true;
  process.env["DECK_FAKE_HANDSHAKE"] = fake.handshake;
  const dir = mkdtempSync(join(tmpdir(), "deck-folder-"));
  const stub = stubBrowser(page, limits);
  browser = stub.browser;
  pages = stub.pages;
  host = await attachBridge(page, async () => dir, { browser });
  server = createServer((req, res) => {
    res.setHeader("content-type", "text/html");
    res.end(req.url?.startsWith("/two") ? "<title>Two</title><h1>Page two</h1><a href='/'>Home</a>" : SHOP);
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  port = (server.address() as AddressInfo).port;
  origin = `http://127.0.0.1:${port}`;
  await page.goto("/");
  await page.getByRole("button", { name: "Open a folder" }).click();
  await expect(page.locator(".project")).toHaveText(basename(dir));
  // The window has told the engine it hosts the browser.
  await expect.poll(() => fake.browserHosted).toBe(true);
}

test.afterEach(async () => {
  browser.stop();
  host.decks.closeAll();
  server.close();
  server.closeAllConnections();
  await fake.close();
});

// What the engine hears back for an op: the text, and whether it was an error.
async function ask(agent: string, op: string, args: Record<string, unknown> = {}, extra: Record<string, unknown> = {}) {
  const r = await fake.browserOp(agent, op, args, extra);
  return { text: String(r["text"] ?? ""), isError: r["is_error"] === true, image: r["image"] as Uint8Array | undefined };
}

const open = async (page: Page) => {
  await page.getByRole("button", { name: "Show the side panel" }).click();
  const panel = page.getByRole("complementary", { name: "Side panel" });
  return panel;
};

// Types a line in the drawer's first terminal; xterm keeps the keys while it has the focus.
async function announce(page: Page, line: string): Promise<void> {
  await page.keyboard.press("Control+j");
  const drawer = page.getByRole("region", { name: "Terminal" });
  await expect(drawer.locator(".xterm-rows")).toContainText("fake shell");
  await drawer.locator(".xterm").click();
  await page.keyboard.type(line);
  await page.keyboard.press("Enter");
}

test.describe("the person's browser", () => {
  test.beforeEach(async ({ page }) => start(page));

  test("a port a terminal prints opens in the Preview tab, where any address can be typed", async ({ page }) => {
    await announce(page, `echo Local: http://localhost:${port}/app`);
    const header = page.getByRole("group", { name: "Dev servers" });
    await expect(header.getByRole("button", { name: `Preview :${port}` })).toBeVisible();
    const drawer = page.getByRole("region", { name: "Terminal" });
    await expect(drawer.getByRole("button", { name: "Stop the dev server in terminal 1" })).toBeVisible();

    // The tab shows while a port is open.
    const panel = await open(page);
    await expect(panel.getByRole("tab", { name: "Preview" })).toBeVisible();
    await panel.getByRole("tab", { name: "Changes" }).click();
    await header.getByRole("button", { name: `Preview :${port}` }).click();
    await expect(panel.getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");
    const browserPane = panel.getByRole("region", { name: "Browser" });
    await expect(browserPane.getByRole("textbox", { name: "Address" })).toHaveValue(`http://localhost:${port}/app`);
    await expect(browserPane.getByRole("tab")).toHaveCount(1);
    await expect.poll(() => pages[0]?.url()).toBe(`http://localhost:${port}/app`);
    await expect.poll(() => browser.state().shown).toBe("t1");
    await expect.poll(() => pages[0]?.placed.at(-1)).toEqual(expect.objectContaining({ w: expect.any(Number) }));
    // Nothing else in the chrome: the tab strip, buttons, one address, no device bar.
    await expect(browserPane.getByRole("button", { name: "1280" })).toHaveCount(0);

    // Any address, not only the dev server's.
    const address = browserPane.getByRole("textbox", { name: "Address" });
    await address.fill(`${origin}/two`);
    await address.press("Enter");
    await expect.poll(() => pages[0]?.url()).toBe(`${origin}/two`);
    await expect(browserPane.getByRole("tab", { name: /Two/ })).toBeVisible();
    // What is not a web page is refused, and says so.
    await address.fill("javascript:alert(1)");
    await address.press("Enter");
    await expect(browserPane.getByText("web pages (http and https) only")).toBeVisible();
    await address.press("Escape");
    expect(pages[0]?.url()).toBe(`${origin}/two`);

    // The palette covers the view: main hides it. It lists the port.
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.keyboard.press("Control+k");
    await expect.poll(() => browser.state().shown).toBeNull();
    await page.getByRole("dialog", { name: "Commands" }).locator("input").fill("preview");
    await expect(page.getByRole("option", { name: new RegExp(`Preview :${port}`) })).toBeVisible();
    await page.keyboard.press("Escape");
    await expect.poll(() => browser.state().shown).toBe("t1");

    // A second tab, from the plus; closing it.
    await browserPane.getByRole("button", { name: "New tab" }).click();
    await expect(address).toBeFocused();
    await address.fill(origin);
    await address.press("Enter");
    await expect(browserPane.getByRole("tab")).toHaveCount(2);
    await browserPane.getByRole("tab").nth(1).hover();
    await browserPane.getByRole("tab").nth(1).getByRole("button", { name: /^Close/ }).click();
    await expect(browserPane.getByRole("tab")).toHaveCount(1);

    // Stop sends Ctrl+C to the terminal.
    const before = fake.calls.filter((m) => m === "TerminalWrite").length;
    await drawer.getByRole("button", { name: "Stop the dev server in terminal 1" }).click();
    await expect.poll(() => fake.calls.filter((m) => m === "TerminalWrite").length).toBe(before + 1);

    // The server stops: the chip goes at the next probe; the browser stays.
    server.close();
    server.closeAllConnections();
    await expect(header).toBeHidden({ timeout: 10_000 });
    await expect(panel.getByRole("tab", { name: "Preview" })).toBeVisible();
  });

  test("with no dev server and no page, the tab is empty and says how a port gets here", async ({ page }) => {
    const panel = await open(page);
    // The tab is not offered without a port or a page; the panel's own tabs are.
    await expect(panel.getByRole("tab", { name: "Preview" })).toHaveCount(0);
    // An agent opens a page: the tab appears.
    await ask("claude1", "open", { url: `${origin}/` }, { free: true });
    await expect(panel.getByRole("tab", { name: "Preview" })).toBeVisible();
  });

  test("a port in an agent's tool output shows too, and the palette opens it", async ({ page }) => {
    await page.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    await announce(page, "echo listening on port 1");
    fake.deck.toolRun("ses_main", "codex-1", "npm run dev", `\x1b[32mVITE\x1b[0m ready\n  ➜  Local:   http://localhost:${port}/\n`);
    const chips = page.getByRole("group", { name: "Dev servers" }).locator(".port-chip");
    await expect(chips).toHaveCount(1);
    await expect(chips.first()).toHaveAttribute("data-port", String(port));
    await expect(chips.first().getByRole("button", { name: `Preview :${port}` })).toHaveAttribute("title", `http://localhost:${port}/, from codex-1`);
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await page.keyboard.press("Control+k");
    await page.getByRole("dialog", { name: "Commands" }).locator("input").fill(`Preview :${port}`);
    await page.keyboard.press("Enter");
    await expect(page.getByRole("complementary", { name: "Side panel" }).getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");
    await expect.poll(() => pages[0]?.url()).toBe(`http://localhost:${port}/`);
  });

  test("Ctrl or Cmd+L puts the cursor in the address field, opening the panel if need be", async ({ page }) => {
    await ask("claude1", "open", { url: `${origin}/` }, { free: true });
    await page.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    // The menu sends it as main would.
    await page.evaluate(() => (window as unknown as Record<string, (a: unknown, b: unknown) => void>)["__deckMenuEmit"]?.("browser-address", undefined));
    await expect(page.getByRole("region", { name: "Browser" }).getByRole("textbox", { name: "Address" })).toBeFocused();
    // It is in the palette with its key.
    await page.keyboard.press("Escape");
    await page.keyboard.press("Control+k");
    await page.getByRole("dialog", { name: "Commands" }).locator("input").fill("address");
    await expect(page.getByRole("option", { name: /Browser: go to address/ })).toContainText("Ctrl L");
  });
});

test.describe("agents in the browser", () => {
  test.beforeEach(async ({ page }) => start(page));

  test("an agent opens, reads, types and clicks through the engine's channel, in a tab the person watches", async ({ page }) => {
    const panel = await open(page);
    const opened = await ask("claude1", "open", { url: `${origin}/` }, { allowed: [origin] });
    expect(opened).toMatchObject({ isError: false });
    expect(opened.text).toContain("opened t1: Shop");

    await panel.getByRole("tab", { name: "Preview" }).click();
    const strip = panel.getByRole("region", { name: "Browser" });
    const chip = strip.locator('[data-tab="t1"]');
    await expect(chip).toHaveAttribute("data-owner", "claude1");
    await expect(chip).toContainText(/Shop|@claude/);
    // The agent's mark: the kind's glyph, in its hue.
    await expect(chip.locator("span[aria-hidden]").first()).not.toHaveText("");
    // What it is doing shows on the tab and in the bar under the address, for a moment.
    const bar = strip.locator('[data-agent-bar="claude1"]');
    await expect(bar).toContainText(/@claude (is using this tab|· opening)/);
    await expect(bar.getByRole("button", { name: "Take over" })).toBeVisible();

    const snap = await ask("claude1", "snapshot");
    expect(snap.isError).toBe(false);
    expect(snap.text.split("\n").map((l) => l.trim())).toEqual(
      expect.arrayContaining([
        `[t1] Shop  ${origin}/`,
        'h1 "Welcome"',
        'link [e1] "Second page" -> /two',
        'textbox [e2] "Email" = ""',
        'textbox [e3] "Password" = ""',
        'checkbox [e4] "Remember me" unchecked',
        'button [e5] "Sign in"',
      ]),
    );
    const tok = (name: string) => /\[(e\d+)\]/.exec(snap.text.split("\n").find((l) => l.includes(`"${name}"`)) ?? "")?.[1] ?? "";
    expect((await ask("claude1", "type", { ref: tok("Email"), text: "ada@example.com" })).text).toBe("typed 15 characters into e2.");
    expect((await ask("claude1", "type", { ref: tok("Password"), text: "s3cr3t-pass" })).text).not.toContain("s3cr3t");
    expect((await ask("claude1", "click", { ref: tok("Remember me") })).isError).toBe(false);
    expect((await ask("claude1", "click", { ref: tok("Sign in") })).isError).toBe(false);
    expect(await pages[0]!.evaluate("document.getElementById('out').textContent")).toBe("submitted ada@example.com");
    expect(await pages[0]!.evaluate("document.getElementById('cb').checked")).toBe(true);

    // The password is never shown back.
    const again = await ask("claude1", "snapshot", { interactive: true });
    expect(again.text).toContain('textbox [e2] "Email" = "ada@example.com"');
    expect(again.text).toContain('textbox [e3] "Password" = (hidden)');
    expect(again.text).not.toContain("s3cr3t");

    // A link goes to a new document: the refs go with it, and say so.
    const nav = await ask("claude1", "click", { ref: tok("Second page") });
    expect(nav.text).toContain(`page: Two  ${origin}/two`);
    const stale = await ask("claude1", "click", { ref: tok("Sign in") });
    expect(stale.isError).toBe(true);
    expect(stale.text).toContain("browser.snapshot");
    const back = await ask("claude1", "back");
    expect(back.text).toContain("Shop");
    // The tab shows the page the agent is on.
    await expect(strip.getByRole("textbox", { name: "Address" })).toHaveValue(`${origin}/`);
  });

  test("a second agent gets a tab of its own and cannot act on the first's", async ({ page }) => {
    const panel = await open(page);
    const ok = { allowed: [origin] };
    await ask("claude1", "open", { url: `${origin}/` }, ok);
    await ask("codex1", "open", { url: `${origin}/two` }, ok);
    await panel.getByRole("tab", { name: "Preview" }).click();
    const strip = panel.getByRole("region", { name: "Browser" });
    await expect(strip.getByRole("tab")).toHaveCount(2);
    await expect(strip.locator('[data-tab="t1"]')).toHaveAttribute("data-owner", "claude1");
    await expect(strip.locator('[data-tab="t2"]')).toHaveAttribute("data-owner", "codex1");
    expect(pages).toHaveLength(2);

    const stolen = await ask("codex1", "snapshot", { tab: "t1" });
    expect(stolen.isError).toBe(true);
    expect(stolen.text).toContain("you can act only on your own");
    const listed = await ask("codex1", "tabs");
    expect(listed.text).toContain("t1 (@claude)");
    expect(listed.text).toContain("t2 (you)");
    // Clicking a tab shows it.
    await strip.locator('[data-tab="t2"]').click();
    await expect.poll(() => browser.state().shown).toBe("t2");
    await strip.locator('[data-tab="t1"]').click();
    await expect.poll(() => browser.state().shown).toBe("t1");
  });

  test("the person can take a tab over, and hand it back", async ({ page }) => {
    const panel = await open(page);
    await ask("claude1", "open", { url: `${origin}/` }, { allowed: [origin] });
    await panel.getByRole("tab", { name: "Preview" }).click();
    const strip = panel.getByRole("region", { name: "Browser" });
    await strip.getByRole("button", { name: "Take over" }).click();
    await expect(strip.getByText("You have this tab. @claude waits.")).toBeVisible();
    const refused = await ask("claude1", "snapshot");
    expect(refused.isError).toBe(true);
    expect(refused.text).toContain("took over t1");
    await strip.getByRole("button", { name: "Hand back" }).click();
    await expect.poll(async () => (await ask("claude1", "snapshot")).isError).toBe(false);
  });

  test("an agent is kept to http(s) and to the sites the person allowed", async () => {
    const files = await ask("claude1", "open", { url: "file:///etc/passwd" }, { free: true });
    expect(files).toMatchObject({ isError: true });
    const chrome = await ask("claude1", "open", { url: "chrome://settings" }, { free: true });
    expect(chrome.isError).toBe(true);
    const unasked = await ask("claude1", "open", { url: "https://example.org/" });
    expect(unasked.isError).toBe(true);
    expect(unasked.text).toContain("not a site the user has allowed");
    expect(browser.state().tabs).toHaveLength(0);
    // The engine passes the sites the person approved.
    const ok = await ask("claude1", "open", { url: `${origin}/two` }, { allowed: [origin] });
    expect(ok.isError).toBe(false);
  });

  test("a screenshot comes back downscaled, as an image", async () => {
    await ask("claude1", "open", { url: `${origin}/` }, { allowed: [origin] });
    const shot = await ask("claude1", "screenshot");
    expect(shot.isError).toBe(false);
    expect(shot.text).toMatch(/^screenshot of t1 \(1024x640, \d+ KB\)$/);
    const bytes = Buffer.from(shot.image ?? []);
    expect(bytes.subarray(0, 3).toString("hex")).toBe("ffd8ff");
  });
});

test.describe("the limits", () => {
  // This browser holds two live pages; the default holds eight.
  test.beforeEach(async ({ page }) => start(page, { live: 2 }));

  test("with more agents than live pages the oldest idle tab is discarded and comes back when its agent returns", async ({ page }) => {
    const panel = await open(page);
    const ok = { allowed: [origin] };
    for (const a of ["claude1", "codex1", "pi1"]) {
      await ask(a, "open", { url: `${origin}/two` }, ok);
      await page.waitForTimeout(20);
    }
    await panel.getByRole("tab", { name: "Preview" }).click();
    const strip = panel.getByRole("region", { name: "Browser" });
    await expect(strip.getByRole("tab")).toHaveCount(3);
    await expect(strip.locator("[data-tab][data-live]")).toHaveCount(2);
    await expect(strip.locator('[data-tab="t1"]')).not.toHaveAttribute("data-live", "");
    expect(browser.state().tabs.filter((t) => t.live).map((t) => t.owner?.agent)).toEqual(["codex1", "pi1"]);
    // Its agent comes back: the page is made again at the same address, and the
    // oldest of the others is let go in turn.
    const back = await ask("claude1", "snapshot");
    expect(back.isError).toBe(false);
    expect(back.text).toContain("h1 \"Page two\"");
    await expect(strip.locator('[data-tab="t1"]')).toHaveAttribute("data-live", "");
    await expect(strip.locator("[data-tab][data-live]")).toHaveCount(2);
    expect(pages.filter((p) => !p.destroyed)).toHaveLength(2);
  });
});
