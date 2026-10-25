import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { basename, delimiter, join } from "node:path";
import { _electron as electron, type ElectronApplication, expect, test } from "@playwright/test";
import { FAKE_DECK } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { FakeDeck } from "../fake-server";

const root = join(__dirname, "..", "..");
const built = existsSync(join(root, "dist", "app", "main", "main.js"));
const display = process.platform !== "linux" || process.env["DISPLAY"] !== undefined;

// The whole app: main, preload, IPC, the app:// protocol and the CSP. Run it
// with `npm run e2e` under a display (xvfb-run on a headless Linux).
test.skip(!built || !display, "needs `npm run build` and a display");

function launch(userData: string, env: Record<string, string> = {}): Promise<ElectronApplication> {
  const args = [root, `--user-data-dir=${userData}`];
  // Root in a container cannot use Chromium's sandbox; a desktop user can.
  if (process.getuid?.() === 0) args.push("--no-sandbox");
  const base = Object.fromEntries(Object.entries(process.env).filter((e): e is [string, string] => e[1] !== undefined));
  return electron.launch({ args, cwd: root, env: { ...base, ...env } });
}

test("over rpc: the app reopens the last folder and runs two agents", async () => {
  const fake = await FakeRpcDeck.start({ tickMs: 300 });
  const folder = mkdtempSync(join(tmpdir(), "deck-folder-"));
  const userData = mkdtempSync(join(tmpdir(), "deck-desktop-"));
  writeFileSync(join(userData, "recent.json"), JSON.stringify([folder]));
  // A `deck` on PATH that hands over the fake server's handshake.
  const bin = join(mkdtempSync(join(tmpdir(), "deck-bin-")), "bin");
  mkdirSync(bin);
  symlinkSync(FAKE_DECK, join(bin, "deck"));
  const stopped = join(folder, "stopped");
  const app = await launch(userData, {
    // The fake stands in for the bundled engine.
    DECK_BIN: join(bin, "deck"),
    PATH: `${bin}${delimiter}${process.env["PATH"] ?? ""}`,
    DECK_FAKE_HANDSHAKE: fake.handshake,
    DECK_FAKE_STOPPED: stopped,
  });
  try {
    const win = await app.firstWindow();
    await win.setViewportSize({ width: 1360, height: 860 });
    await expect(win.locator(".project")).toHaveText(basename(folder));
    await expect(win.locator(".link")).toHaveText("Connected");
    const text = await win.evaluate(() => document.body.innerText);
    expect(text).not.toContain(fake.token);

    await win.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    const box = win.getByRole("textbox", { name: "Message" });
    await box.fill("$claude $codex+ split the work and compare");
    await box.press("Enter");
    await expect(win.locator('.agent[data-agent="codex-2"]')).toBeVisible();
    await expect(win.locator('[data-agent="claude-1"].msg').last().locator(".streaming")).toBeVisible();
    await win.locator(".panel-toggles").getByRole("button", { name: /Diff/ }).click();
    await win.waitForTimeout(700);
    await win.screenshot({ path: join(root, "docs", "screenshot.png") });
  } finally {
    await app.close();
    await fake.close();
  }
  // Quitting the app closed the Deck's stdin.
  await expect.poll(() => existsSync(stopped), { timeout: 5000 }).toBe(true);
});

test("over v2: the app pairs, connects and runs two agents", async () => {
  const fake = await FakeDeck.start({ tickMs: 300 });
  // A claude on PATH, for the Welcome to find.
  const bin = mkdtempSync(join(tmpdir(), "deck-clis-"));
  writeFileSync(join(bin, "claude"), "#!/bin/sh\n", { mode: 0o755 });
  const app = await launch(mkdtempSync(join(tmpdir(), "deck-desktop-")), {
    PATH: `${bin}${delimiter}${process.env["PATH"] ?? ""}`,
  });
  try {
    const win = await app.firstWindow();
    // A fresh user data folder: the Welcome, with the CLIs main found.
    await expect(win.getByRole("heading", { name: "One deck for all your agents" })).toBeVisible();
    await expect(win.getByRole("list", { name: "Agent CLIs" }).locator('[data-kind="claude"]')).toContainText(join(bin, "claude"));

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

    // The pairing form opens on demand when the app's engine is there.
    const pairButton = win.getByRole("button", { name: "Pair with a Deck" });
    if ((await pairButton.getAttribute("aria-expanded")) !== "true") await pairButton.click();
    await win.getByLabel("Pairing link or invite line").fill(fake.pairLink);
    await win.getByRole("button", { name: "Pair", exact: true }).click();
    await expect(win.locator(".project")).toHaveText("deck");
    const servers = await win.evaluate(() =>
      (window as unknown as { deck: { listServers: () => Promise<unknown> } }).deck.listServers(),
    );
    expect(JSON.stringify(servers)).not.toMatch(/token/i);

    await win.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    const box = win.getByRole("textbox", { name: "Message" });
    await box.fill("$claude $codex+ split the work and compare");
    await box.press("Enter");
    await expect(win.locator('.agent[data-agent="codex-2"]')).toBeVisible();
    await expect(win.locator('[data-agent="codex-2"].msg').last()).toContainText("split the work");
  } finally {
    await app.close();
    await fake.close();
  }
});

test("ports: a terminal's dev server opens in the preview view, which keeps to its port", async () => {
  const fake = await FakeRpcDeck.start({ tickMs: 300 });
  const folder = mkdtempSync(join(tmpdir(), "deck-folder-"));
  const userData = mkdtempSync(join(tmpdir(), "deck-desktop-"));
  writeFileSync(join(userData, "recent.json"), JSON.stringify([folder]));
  const bin = join(mkdtempSync(join(tmpdir(), "deck-bin-")), "bin");
  mkdirSync(bin);
  symlinkSync(FAKE_DECK, join(bin, "deck"));
  const server = createServer((_req, res) => {
    res.setHeader("content-type", "text/html");
    res.end(
      '<title>Fixture</title><h1>Preview fixture</h1><a id="out" href="http://deck-preview.invalid/">out</a>' +
        '<a id="pop" href="http://deck-preview.invalid/pop" target="_blank">pop</a><a id="file" href="file:///etc/hosts">file</a>',
    );
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  const port = (server.address() as AddressInfo).port;
  const app = await launch(userData, {
    DECK_BIN: join(bin, "deck"),
    PATH: `${bin}${delimiter}${process.env["PATH"] ?? ""}`,
    DECK_FAKE_HANDSHAKE: fake.handshake,
  });
  // The view's page, read from main through Playwright: the app has no hook for it.
  const viewUrl = () =>
    app.evaluate(({ webContents }, p) => webContents.getAllWebContents().find((w) => w.getURL().includes(`:${p}/`))?.getURL() ?? null, port);
  const inView = (code: string) =>
    app.evaluate(
      ({ webContents }, [p, js]) => webContents.getAllWebContents().find((w) => w.getURL().includes(`:${p}/`))?.executeJavaScript(js),
      [port, code] as const,
    );
  const shown = () =>
    app.evaluate(({ BrowserWindow }) => {
      const views = BrowserWindow.getAllWindows()[0]?.contentView.children ?? [];
      return views.map((v) => ({ visible: v.getVisible(), width: v.getBounds().width }));
    });
  try {
    const win = await app.firstWindow();
    await win.setViewportSize({ width: 1360, height: 860 });
    await expect(win.locator(".link")).toHaveText("Connected");
    // What the app hands the system browser, kept here instead.
    await app.evaluate(({ shell }) => {
      const g = globalThis as unknown as { opened: string[] };
      g.opened = [];
      shell.openExternal = async (url: string) => {
        g.opened.push(url);
      };
    });
    const opened = () => app.evaluate(() => (globalThis as unknown as { opened: string[] }).opened);

    await win.keyboard.press("Control+j");
    const drawer = win.getByRole("region", { name: "Terminal" });
    await expect(drawer.locator(".xterm-rows")).toContainText("fake shell");
    await drawer.locator(".xterm").click();
    await win.keyboard.type(`echo Local: http://localhost:${port}/`);
    await win.keyboard.press("Enter");
    await win.getByRole("group", { name: "Dev servers" }).getByRole("button", { name: `Preview :${port}` }).click();
    await expect(win.getByRole("complementary", { name: "Side panel" }).getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");

    await expect.poll(viewUrl, { timeout: 10_000 }).toBe(`http://localhost:${port}/`);
    await expect.poll(() => inView("document.querySelector('h1')?.textContent")).toBe("Preview fixture");
    await expect.poll(shown).toEqual([{ visible: true, width: expect.any(Number) }]);
    expect((await shown())[0]?.width).toBeGreaterThan(100);
    // The page has none of the app: no bridge, no Node, its own session.
    expect(await inView("[typeof window.deck, typeof require, typeof process].join(' ')")).toBe("undefined undefined undefined");
    const own = await app.evaluate(({ webContents, session }, p) => {
      const wc = webContents.getAllWebContents().find((w) => w.getURL().includes(`:${p}/`));
      return wc !== undefined && wc.session !== session.defaultSession && wc.session === session.fromPartition("preview");
    }, port);
    expect(own).toBe(true);
    await expect(win.getByRole("region", { name: "Preview" }).locator(".preview-host")).toHaveText(`localhost:${port}`);

    // A link to another site goes to the browser, a new window too, and a
    // file link nowhere; the view stays on the port. Main opens at most one
    // browser tab a second, so the second click waits out that gap.
    await inView("document.getElementById('out').click()");
    await expect.poll(opened).toEqual(["http://deck-preview.invalid/"]);
    await win.waitForTimeout(1100);
    await inView("document.getElementById('pop').click()");
    await inView("document.getElementById('file').click()");
    await expect.poll(opened).toEqual(["http://deck-preview.invalid/", "http://deck-preview.invalid/pop"]);
    await win.waitForTimeout(500);
    expect(await viewUrl()).toBe(`http://localhost:${port}/`);

    // The palette covers the view, so main hides it.
    await win.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await win.keyboard.press("Control+k");
    await expect.poll(shown).toEqual([{ visible: false, width: expect.any(Number) }]);
    await win.keyboard.press("Escape");
    await expect.poll(shown).toEqual([{ visible: true, width: expect.any(Number) }]);
    // Another tab hides it; closing the panel too.
    await win.getByRole("complementary", { name: "Side panel" }).getByRole("tab", { name: "Changes" }).click();
    await expect.poll(shown).toEqual([{ visible: false, width: expect.any(Number) }]);
  } finally {
    await app.close();
    server.close();
    await fake.close();
  }
});

// The real engine the app bundles (npm run build:deck), in a real git work
// tree: the window reads its branch and runs a shell under the CSP.
const engine = join(root, "bin", process.platform === "win32" ? "deck.exe" : "deck");
test("with the bundled engine: git status and a terminal", async () => {
  test.skip(!existsSync(engine) || process.platform === "win32", "needs `npm run build:deck`");
  const folder = mkdtempSync(join(tmpdir(), "deck-real-"));
  const git = (...args: string[]) =>
    execFileSync("git", args, {
      cwd: folder,
      env: { ...process.env, GIT_AUTHOR_NAME: "t", GIT_AUTHOR_EMAIL: "t@e", GIT_COMMITTER_NAME: "t", GIT_COMMITTER_EMAIL: "t@e" },
    });
  git("init", "-q", "-b", "main");
  writeFileSync(join(folder, "a.txt"), "a\n");
  git("add", "-A");
  git("commit", "-q", "-m", "first");
  writeFileSync(join(folder, "b.txt"), "b\n");
  const userData = mkdtempSync(join(tmpdir(), "deck-desktop-"));
  writeFileSync(join(userData, "recent.json"), JSON.stringify([folder]));
  const app = await launch(userData, { DECK_BIN: engine, HOME: mkdtempSync(join(tmpdir(), "deck-home-")) });
  try {
    const win = await app.firstWindow();
    await win.setViewportSize({ width: 1360, height: 860 });
    await expect(win.locator(".link")).toHaveText("Connected", { timeout: 20_000 });
    // One changed file: the next git step is a commit.
    await expect(win.locator(".git-actions").getByRole("button", { name: "Commit" })).toBeVisible();

    await win.keyboard.press("Control+j");
    const drawer = win.getByRole("region", { name: "Terminal" });
    await drawer.locator(".xterm").click();
    await win.keyboard.type("echo $((6*7)) && git branch --show-current");
    await win.keyboard.press("Enter");
    await expect(drawer.locator(".xterm-rows")).toContainText("42");
    await expect(drawer.locator(".xterm-rows")).toContainText("main");
    // xterm's own layout rules reach the page through the constructed
    // stylesheet, though the CSP refuses its <style> elements.
    const display = await drawer.locator(".xterm-rows span").first().evaluate((e) => getComputedStyle(e).display);
    expect(display).toBe("inline-block");
    await win.screenshot({ path: join(root, "docs", "terminal.png") });
    // The real workspace answers the team call: no link in a fresh home.
    await win.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Team", exact: true }).click();
    await expect(win.locator("[data-team]")).toContainText(/No team link yet|Teammates/);
  } finally {
    await app.close();
  }
});
