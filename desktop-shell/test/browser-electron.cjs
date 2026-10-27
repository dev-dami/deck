// Run after `bun run build` with the installed Electron executable:
// electron test/browser-electron.cjs
// Uses only a local fixture, offscreen Chromium and a temporary browser profile.
const { app, BrowserWindow, WebContentsView, session } = require("electron");
const { createServer } = require("node:http");
const { mkdtempSync, writeFileSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const assert = require("node:assert/strict");
const { Browser } = require("../dist/app/main/browser.js");

app.setPath("userData", mkdtempSync(join(tmpdir(), "deck-browser-smoke-")));
app.disableHardwareAcceleration();
app.commandLine.appendSwitch("host-resolver-rules", "MAP app.browser.test 127.0.0.1, MAP auth.browser.test 127.0.0.1");
app.commandLine.appendSwitch("no-proxy-server");
const timeout = setTimeout(() => {
  console.error("browser smoke timed out");
  app.exit(1);
}, 45000);

app
  .whenReady()
  .then(async () => {
    const submissions = [];
    const origins = [];
    const fixture = createServer((req, res) => {
      const chunks = [];
      req.on("data", (chunk) => chunks.push(chunk));
      req.on("end", () => {
        const port = fixture.address().port;
        if (req.url === "/redirect") {
          res.writeHead(307, { Location: `http://auth.browser.test:${port}/final` });
          res.end();
          return;
        }
        res.setHeader("Content-Type", "text/html");
        if (req.url === "/final") {
          submissions.push({ method: req.method, body: Buffer.concat(chunks).toString() });
          res.end("<!doctype html><title>POST preserved</title>Signed-in fixture");
          return;
        }
        res.end(
          `<!doctype html><title>Browser fixture</title><label>Message <input id="message" aria-label="Message"></label><label>Password <input type="password" aria-label="Password" value="never-return-this"></label><button onclick="document.title='Clicked'">Submit</button><form method="POST" action="http://app.browser.test:${port}/redirect"><input type="hidden" name="proof" value="fixture-post-body"><button>Sign in</button></form>`,
        );
      });
    });
    await new Promise((resolve) => fixture.listen(0, "127.0.0.1", resolve));
    const url = `http://127.0.0.1:${fixture.address().port}/`;
    const win = new BrowserWindow({
      show: false,
      width: 1280,
      height: 900,
      webPreferences: { sandbox: true, contextIsolation: true, nodeIntegration: false, offscreen: true },
    });
    await win.loadURL("about:blank");
    const pages = [];
    const profile = session.fromPartition("persist:browser-smoke");
    const browser = new Browser({
      create: (webPreferences) => {
        const v = new WebContentsView({ webPreferences: { ...webPreferences, offscreen: true } });
        win.contentView.addChildView(v);
        pages.push(v.webContents);
        return v;
      },
      remove: (v) => win.contentView.removeChildView(v),
      session: () => profile,
      zoom: () => 1,
      changed: () => {},
      approve: async (_deck, _request, origin) => {
        assert.ok(origin.includes(".browser.test:"), "Loopback unexpectedly requested origin approval");
        origins.push(origin);
        return true;
      },
    });
    let next = 0;
    const run = (command, owner = "codex") =>
      browser.execute("deck", {
        id: `smoke-${++next}`,
        session_id: "room",
        controller_id: "local",
        controller_generation: "g1",
        owner,
        command,
        deadline_at: Date.now() + 30000,
      });
    try {
      const opened = await run({ action: "open", url });
      assert.equal(opened.error_code, "", opened.result?.text);
      const tab = opened.tab.id;
      browser.present({ deck_id: "deck", session_id: "room", tab_id: tab, bounds: { x: 0, y: 0, w: 1280, h: 800, zoom: 1 } });
      const snap = await run({ action: "snapshot", tab_id: tab });
      assert.equal(snap.error_code, "", snap.result?.text);
      assert.ok(snap.result.text.includes("UNTRUSTED PAGE CONTENT"));
      assert.ok(!snap.result.text.includes("never-return-this"));
      const ref = (role, name) => {
        const match = snap.result.text.match(new RegExp(`\\[(e\\d+)\\] ${role}: ${name}`));
        assert.ok(match, `Missing ${role} ${name}: ${snap.result.text}`);
        return match[1];
      };
      const typed = await run({
        action: "type",
        tab_id: tab,
        element_ref: ref("textbox", "Message"),
        snapshot_version: snap.tab.snapshot_version,
        text: "Deck native browser",
      });
      assert.equal(typed.error_code, "", typed.result?.text);
      assert.equal(await pages[0].executeJavaScript('document.querySelector("#message").value'), "Deck native browser");
      const fresh = await run({ action: "snapshot", tab_id: tab });
      const button = fresh.result.text.match(/\[(e\d+)\] button: Submit/)[1];
      const clicked = await run({ action: "click", tab_id: tab, element_ref: button, snapshot_version: fresh.tab.snapshot_version });
      assert.equal(clicked.error_code, "", clicked.result?.text);
      for (let n = 0; n < 20 && (await pages[0].executeJavaScript("document.title")) !== "Clicked"; n++)
        await new Promise((resolve) => setTimeout(resolve, 20));
      assert.equal(await pages[0].executeJavaScript("document.title"), "Clicked");
      const stale = await run({ action: "click", tab_id: tab, element_ref: button, snapshot_version: fresh.tab.snapshot_version });
      assert.equal(stale.error_code, "stale_snapshot");
      const image = await run({ action: "screenshot", tab_id: tab });
      assert.equal(image.error_code, "", image.result?.text);
      const png = image.result.images[0].data;
      assert.equal(Buffer.from(png).subarray(1, 4).toString(), "PNG");
      assert.ok(png.length > 100);
      if (process.env.DECK_BROWSER_SCREENSHOT) writeFileSync(process.env.DECK_BROWSER_SCREENSHOT, png);
      assert.equal((await run({ action: "take_control", tab_id: tab }, "")).tab.user_in_control, true);
      assert.equal((await run({ action: "snapshot", tab_id: tab })).error_code, "user_in_control");
      assert.equal((await run({ action: "screenshot", tab_id: tab })).error_code, "user_in_control");
      assert.equal((await run({ action: "release_control", tab_id: tab }, "")).tab.user_in_control, false);
      await profile.cookies.set({ url, name: "browser-smoke", value: "persisted" });
      await profile.cookies.flushStore();
      const appOrigin = `http://app.browser.test:${fixture.address().port}`;
      const authOrigin = `http://auth.browser.test:${fixture.address().port}`;
      assert.equal((await run({ action: "navigate", tab_id: tab, url: appOrigin })).error_code, "");
      const form = await run({ action: "snapshot", tab_id: tab });
      const signIn = form.result.text.match(/\[(e\d+)\] button: Sign in/)[1];
      assert.equal(
        (await run({ action: "click", tab_id: tab, element_ref: signIn, snapshot_version: form.tab.snapshot_version })).error_code,
        "",
      );
      for (let n = 0; n < 100 && (await pages[0].executeJavaScript("document.title")) !== "POST preserved"; n++)
        await new Promise((resolve) => setTimeout(resolve, 20));
      assert.equal(await pages[0].executeJavaScript("document.title"), "POST preserved");
      assert.deepEqual(submissions, [{ method: "POST", body: "proof=fixture-post-body" }]);
      assert.deepEqual(origins, [appOrigin, authOrigin]);
      browser.closeDeck("deck");
      assert.equal(browser.state("deck", "room").tabs.length, 0);
      assert.equal((await profile.cookies.get({ url, name: "browser-smoke" }))[0].value, "persisted");
      console.log(
        JSON.stringify({
          ok: true,
          rendering: "offscreen Chromium/WebContentsView",
          checks: [
            "loopback navigation",
            "AX snapshot",
            "password value excluded",
            "CDP type",
            "CDP click",
            "stale handle",
            "PNG capture",
            "takeover pauses reads and captures",
            "explicit release",
            "persistent cookie store",
            "origin approval preserves POST across a 307 redirect",
            "final-tab cleanup",
          ],
          screenshot_bytes: png.length,
        }),
      );
    } finally {
      browser.close();
      win.destroy();
      fixture.close();
      clearTimeout(timeout);
    }
    app.exit(0);
  })
  .catch((err) => {
    console.error(err);
    clearTimeout(timeout);
    app.exit(1);
  });
