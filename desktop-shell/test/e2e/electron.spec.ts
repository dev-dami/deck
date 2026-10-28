import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { basename, delimiter, join } from "node:path";
import { _electron as electron, type ElectronApplication, expect, test } from "@playwright/test";
import { isAppIcon } from "../../src/main/appIcon";
import { APP_ICON_SIZES } from "../../src/shared/bridge";
import { FAKE_DECK } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";
import { FakeDeck } from "../fake-server";

const root = join(__dirname, "..", "..");
const built = existsSync(join(root, "dist", "app", "main", "main.js"));
const display = process.platform !== "linux" || process.env["DISPLAY"] !== undefined;

// The whole app: main, preload, IPC, the app:// protocol and the CSP. Run it
// with `npm run e2e` under a display (xvfb-run on a headless Linux).
test.skip(!built || !display, "needs `npm run build` and a display");

function launch(userData: string, env: Record<string, string> = {}, extra: string[] = []): Promise<ElectronApplication> {
  const args = [root, `--user-data-dir=${userData}`, ...extra];
  // Root in a container cannot use Chromium's sandbox; a desktop user can.
  if (process.getuid?.() === 0) args.push("--no-sandbox");
  const base = Object.fromEntries(Object.entries(process.env).filter((e): e is [string, string] => e[1] !== undefined));
  // ELECTRON_BIN runs another build of Electron than node_modules' (a machine that downloaded none).
  const executablePath = process.env["ELECTRON_BIN"];
  return electron.launch({ args, cwd: root, env: { ...base, ...env }, ...(executablePath === undefined ? {} : { executablePath }) });
}

test("over rpc: the app reopens the last folder and runs two agents", async () => {
  const fake = await FakeRpcDeck.start({ tickMs: 300 });
  // A folder named "deck", so the screenshot shows a project's name.
  const folder = join(mkdtempSync(join(tmpdir(), "deck-folder-")), "deck");
  mkdirSync(folder);
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
    // The first Electron start of a run is a cold one (on CI it follows the
    // binary's download), and the GitHub runner took over 5s to reopen the
    // folder there; later starts take about one.
    await expect(win.locator(".project")).toHaveText(basename(folder), { timeout: 20_000 });
    await expect(win.locator(".link")).toHaveText("Connected");
    const text = await win.evaluate(() => document.body.innerText);
    expect(text).not.toContain(fake.token);

    await win.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();
    const box = win.getByRole("textbox", { name: "Message" });
    await box.fill("$claude $codex split the work and compare");
    await box.press("Enter");
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
    await box.fill("$claude $codex split the work and compare");
    await box.press("Enter");
    await expect(win.locator('[data-agent="codex-1"].msg').last()).toContainText("split the work");
  } finally {
    await app.close();
    await fake.close();
  }
});

test("the browser: a dev server for the person, tabs for agents, one session per Deck, and a page that gets nothing of the app", async () => {
  test.setTimeout(120_000);
  const fake = await FakeRpcDeck.start({ tickMs: 300 });
  fake.advertiseBrowser = true;
  const folder = mkdtempSync(join(tmpdir(), "deck-folder-"));
  const userData = mkdtempSync(join(tmpdir(), "deck-desktop-"));
  writeFileSync(join(userData, "recent.json"), JSON.stringify([folder]));
  const bin = join(mkdtempSync(join(tmpdir(), "deck-bin-")), "bin");
  mkdirSync(bin);
  symlinkSync(FAKE_DECK, join(bin, "deck"));
  // The port is known once it listens; the page links to another name for this machine.
  let port = 0;
  const server = createServer((req, res) => {
    res.setHeader("content-type", "text/html");
    res.setHeader("set-cookie", "seen=1; Path=/");
    res.end(
      `<title>Fixture</title><h1>Preview fixture</h1><p id="host">${req.headers.host}</p>` +
        `<a id="out" href="http://elsewhere.test:${port}/other">out</a><a id="file" href="file:///etc/hosts">file</a>` +
        "<form onsubmit=\"event.preventDefault();document.title='sent '+document.getElementById('q').value\"><label>Search <input id=\"q\"></label><button>Go</button></form>",
    );
  });
  await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
  port = (server.address() as AddressInfo).port;
  const app = await launch(
    userData,
    {
      DECK_BIN: join(bin, "deck"),
      PATH: `${bin}${delimiter}${process.env["PATH"] ?? ""}`,
      DECK_FAKE_HANDSHAKE: fake.handshake,
    },
    // Not a loopback name for the policy, but this machine for the network.
    [`--host-resolver-rules=MAP elsewhere.test 127.0.0.1`],
  );
  // A tab's page, read from main through Playwright: the app has no hook for it.
  const pageUrls = () =>
    app.evaluate(({ webContents }) => webContents.getAllWebContents().map((w) => w.getURL()).filter((u) => u.startsWith("http")));
  const inPage = (needle: string, code: string) =>
    app.evaluate(({ webContents }, [n, js]) => webContents.getAllWebContents().find((w) => w.getURL().includes(n as string))?.executeJavaScript(js as string, true), [needle, code] as const);
  const views = () =>
    app.evaluate(({ BrowserWindow }) => {
      const kids = BrowserWindow.getAllWindows()[0]?.contentView.children ?? [];
      return kids.map((v) => ({ visible: v.getVisible(), width: v.getBounds().width }));
    });
  const ask = async (agent: string, op: string, args: Record<string, unknown> = {}, extra: Record<string, unknown> = {}) => {
    const r = await fake.browserOp(agent, op, args, extra);
    return { text: String(r["text"] ?? ""), isError: r["is_error"] === true };
  };
  try {
    const win = await app.firstWindow();
    await win.setViewportSize({ width: 1360, height: 860 });
    await expect(win.locator(".link")).toHaveText("Connected");
    // The window told the engine it hosts the browser.
    await expect.poll(() => fake.browserHosted, { timeout: 10_000 }).toBe(true);

    // The person: a dev server a terminal printed opens in the Preview tab.
    await win.keyboard.press("Control+j");
    const drawer = win.getByRole("region", { name: "Terminal" });
    await expect(drawer.locator(".xterm-rows")).toContainText("fake shell");
    await drawer.locator(".xterm").click();
    await win.keyboard.type(`echo Local: http://localhost:${port}/`);
    await win.keyboard.press("Enter");
    await win.getByRole("group", { name: "Dev servers" }).getByRole("button", { name: `Preview :${port}` }).click();
    const panel = win.getByRole("complementary", { name: "Side panel" });
    await expect(panel.getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");
    const pane = panel.getByRole("region", { name: "Browser" });
    await expect.poll(pageUrls, { timeout: 10_000 }).toContain(`http://localhost:${port}/`);
    await expect.poll(() => inPage(`:${port}/`, "document.querySelector('h1')?.textContent")).toBe("Preview fixture");
    await expect(pane.getByRole("textbox", { name: "Address" })).toHaveValue(`http://localhost:${port}/`);
    await expect.poll(async () => (await views()).filter((v) => v.visible).length).toBe(1);
    expect((await views()).find((v) => v.visible)?.width).toBeGreaterThan(100);
    // The page has none of the app: no bridge, no Node, a session of its own.
    expect(await inPage(`:${port}/`, "[typeof window.deck, typeof require, typeof process].join(' ')")).toBe("undefined undefined undefined");
    const own = await app.evaluate(({ webContents, session }, p) => {
      const wc = webContents.getAllWebContents().find((w) => w.getURL().includes(`:${p}/`));
      return wc !== undefined && wc.session !== session.defaultSession && wc.session === session.fromPartition("browser-local-1");
    }, port);
    expect(own).toBe(true);

    // The person browses anywhere: a link to another site opens in the tab.
    await inPage(`:${port}/`, "document.getElementById('out').click()");
    await expect.poll(() => inPage("elsewhere.test", "document.getElementById('host')?.textContent")).toBe(`elsewhere.test:${port}`);
    // A file link goes nowhere.
    await inPage("elsewhere.test", "document.getElementById('file').click()");
    await win.waitForTimeout(300);
    expect(await pageUrls()).toEqual([`http://elsewhere.test:${port}/other`]);
    await pane.getByRole("button", { name: "Back" }).click();
    await expect.poll(pageUrls).toEqual([`http://localhost:${port}/`]);

    // An agent: its own tab, read as an outline, driven by refs.
    // The person's tab sets a cookie; the agent's tab, in the same session, sees it.
    await inPage(`localhost:${port}`, "document.cookie = 'shared=yes; path=/'");
    const opened = await ask("claude1", "open", { url: `http://localhost:${port}/` });
    expect(opened.isError).toBe(false);
    expect(opened.text).toContain("opened t2: Fixture");
    const snap = await ask("claude1", "snapshot");
    const search = /textbox \[(e\d+)\] "Search" = ""/.exec(snap.text)?.[1] ?? "";
    expect(search).not.toBe("");
    expect((await ask("claude1", "type", { ref: search, text: "moon", submit: true })).isError).toBe(false);
    await expect.poll(async () => (await ask("claude1", "eval", { js: "document.title" })).text).toBe('"sent moon"');
    expect((await ask("claude1", "eval", { js: "document.cookie" })).text).toMatch(/shared=yes/);
    await expect(pane.locator('[data-tab="t2"]')).toHaveAttribute("data-owner", "claude1");

    // Its page is kept to the sites the person let it go to.
    const link = await ask("claude1", "snapshot", { interactive: true });
    const outRef = /link \[(e\d+)\] "out"/.exec(link.text)?.[1] ?? "";
    const click = await ask("claude1", "click", { ref: outRef });
    expect(click.isError).toBe(false);
    // The click's answer says where the page tried to go, and the page stayed.
    expect(click.text).toContain(`tried to go to http://elsewhere.test:${port}/other`);
    const after = await ask("claude1", "snapshot", { interactive: true });
    expect(after.text).toContain(`http://localhost:${port}/`);
    // Approved by the person (the engine passes the origin): it goes.
    await ask("claude1", "open", { url: `http://elsewhere.test:${port}/other` }, { allowed: [`http://elsewhere.test:${port}`] });
    await expect.poll(() => inPage(`elsewhere.test:${port}/other`, "document.getElementById('host')?.textContent")).toBe(`elsewhere.test:${port}`);
    // A scheme that is no web page never loads.
    expect((await ask("claude1", "open", { url: "file:///etc/passwd" }, { free: true })).isError).toBe(true);

    // A second agent works in a tab of its own, beside the first's.
    const second = await ask("codex1", "open", { url: `http://localhost:${port}/` });
    expect(second.text).toContain("opened t3");
    await expect(pane.getByRole("tab")).toHaveCount(3);

    // Hidden tabs are one pixel, not shown to the person; the shown one is the person's.
    const shown = await views();
    expect(shown.filter((v) => v.visible && v.width > 10)).toHaveLength(1);

    // The palette covers the view, so main hides it.
    await win.evaluate(() => (document.activeElement as HTMLElement | null)?.blur());
    await win.keyboard.press("Control+k");
    await expect.poll(async () => (await views()).filter((v) => v.visible && v.width > 10).length).toBe(0);
    await win.keyboard.press("Escape");
    await expect.poll(async () => (await views()).filter((v) => v.visible && v.width > 10).length).toBe(1);
    // Another tab of the panel hides it.
    await panel.getByRole("tab", { name: "Changes" }).click();
    await expect.poll(async () => (await views()).filter((v) => v.visible && v.width > 10).length).toBe(0);
  } finally {
    await app.close();
    server.close();
    server.closeAllConnections();
    await fake.close();
  }
});

// The real engine the app bundles (npm run build:deck), in a real git work
// tree: the window reads its branch and runs a shell under the CSP.
const engine = join(root, "bin", process.platform === "win32" ? "deck.exe" : "deck");
test("with the bundled engine: git status and a terminal", async () => {
  test.skip(!existsSync(engine) || process.platform === "win32", "needs `npm run build:deck`");
  // A folder named "deck", so the screenshot shows a project's name.
  const folder = join(mkdtempSync(join(tmpdir(), "deck-real-")), "deck");
  mkdirSync(folder);
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

test("the app icon: the window draws it, main checks it, shows it and keeps it for the next start", async () => {
  const userData = mkdtempSync(join(tmpdir(), "deck-desktop-"));
  const saved = join(userData, "app-icon.json");
  const app = await launch(userData);
  try {
    await app.firstWindow();
    // The default theme's icon came over IPC as PNG bytes and passed main's checks.
    await expect.poll(() => existsSync(saved), { timeout: 15_000 }).toBe(true);
    const file = JSON.parse(readFileSync(saved, "utf8")) as { images: { size: number; png: string }[] };
    const images = file.images.map((i) => ({ size: i.size, png: new Uint8Array(Buffer.from(i.png, "base64")) }));
    expect(isAppIcon(images)).toBe(true);
    expect(images.map((i) => i.size)).toEqual([...(process.platform === "darwin" ? APP_ICON_SIZES.mac : APP_ICON_SIZES.other)]);
  } finally {
    await app.close();
  }
});
