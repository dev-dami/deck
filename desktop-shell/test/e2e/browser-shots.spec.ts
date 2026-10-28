import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, symlinkSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import type { AddressInfo } from "node:net";
import { tmpdir } from "node:os";
import { delimiter, join } from "node:path";
import { _electron as electron, type ElectronApplication, expect, type Page, test } from "@playwright/test";
import { FAKE_DECK } from "../bridge";
import { FakeRpcDeck } from "../fake-grpc";

// Screenshots of the browser in the real app, in dark and light, for design
// review: SHOTS=<dir> under a display (xvfb-run). The pages the browser shows
// are views laid over the window, which the window's own screenshot leaves
// out, so these are taken from the X display. Without SHOTS the run skips them.
const OUT = process.env["SHOTS"] ?? "";
const root = join(__dirname, "..", "..");
const built = existsSync(join(root, "dist", "app", "main", "main.js"));
const display = process.platform === "linux" && process.env["DISPLAY"] !== undefined;
test.skip(OUT === "" || !built || !display, "needs SHOTS=<dir>, `npm run build` and an X display");

const WIDTH = 1360;
const HEIGHT = 860;

// Four small sites, none a real one: a dev server's app, a docs page, a shop
// and a sign-in form.
const style = `<style>body{font:15px/1.5 system-ui,sans-serif;margin:0;color:#1d1d1f;background:#fff}header{padding:14px 28px;border-bottom:1px solid #e5e5ea;display:flex;gap:18px;align-items:center}
header b{font-size:17px}nav a{color:#555;margin-right:14px;text-decoration:none}main{padding:28px;max-width:640px}h1{font-size:26px;margin:0 0 6px}p{color:#555}
.card{border:1px solid #e5e5ea;border-radius:12px;padding:16px;margin:14px 0}button{font:inherit;padding:8px 16px;border-radius:8px;border:0;background:#0a84ff;color:#fff}
input{font:inherit;padding:8px 10px;border:1px solid #ccc;border-radius:8px;width:260px}label{display:block;margin:10px 0}code{background:#f2f2f7;padding:2px 6px;border-radius:5px}</style>`;
const SITES: Record<string, string> = {
  app: `${style}<title>Pairing lockout</title><header><b>Deck app</b><nav><a href="#">Rooms</a><a href="#">Settings</a></nav></header><main><h1>Pairing lockout</h1><p>The dev server is up. Three failed codes lock a device for a minute.</p><div class="card"><b>Locked devices</b><p>No device is locked.</p></div><button>Reset limiter</button></main>`,
  docs: `${style}<title>Rate limiting - Guide</title><header><b>Guide</b><nav><a href="#">Start</a><a href="#">Auth</a><a href="#">Limits</a></nav></header><main><h1>Rate limiting</h1><p>A limiter keys on the device, not the address. Use <code>Retry-After</code> to tell a caller when to return.</p><div class="card"><b>Token bucket</b><p>Refills one token a second up to a burst of five.</p></div><p><a href="#">Next: Backoff</a></p></main>`,
  shop: `${style}<title>Keyboards - Corner Shop</title><header><b>Corner Shop</b><nav><a href="#">Keyboards</a><a href="#">Mice</a><a href="#">Cart</a></nav></header><main><h1>Keyboards</h1><div class="card"><b>Low profile, 65%</b><p>Hot-swap, USB-C, 80 hours on a charge.</p><button>Add to cart</button></div><div class="card"><b>Full size</b><p>Quiet switches, numpad.</p><button>Add to cart</button></div></main>`,
  login: `${style}<title>Sign in - Example</title><header><b>Example</b></header><main><h1>Sign in</h1><form onsubmit="event.preventDefault();document.getElementById('o').textContent='Signed in'"><label>Email<br><input type="email" name="email"></label><label>Password<br><input type="password" name="pw"></label><button>Sign in</button></form><p id="o"></p></main>`,
};

function launch(userData: string, env: Record<string, string>, extra: string[]): Promise<ElectronApplication> {
  const args = [root, `--user-data-dir=${userData}`, "--no-sandbox", ...extra];
  const base = Object.fromEntries(Object.entries(process.env).filter((e): e is [string, string] => e[1] !== undefined));
  const executablePath = process.env["ELECTRON_BIN"];
  return electron.launch({ args, cwd: root, env: { ...base, ...env }, ...(executablePath === undefined ? {} : { executablePath }) });
}

async function shot(win: Page, name: string): Promise<void> {
  // Let the views paint.
  await win.waitForTimeout(500);
  const file = join(OUT, `${name}.png`);
  execFileSync("import", ["-window", "root", "-crop", `${WIDTH}x${HEIGHT}+0+0`, "+repage", file]);
}

for (const mode of ["dark", "light"] as const) {
  test(`the browser, ${mode}`, async () => {
    test.setTimeout(180_000);
    mkdirSync(OUT, { recursive: true });
    const fake = await FakeRpcDeck.start({ tickMs: 400 });
    fake.advertiseBrowser = true;
    const folder = join(mkdtempSync(join(tmpdir(), "deck-folder-")), "deck");
    mkdirSync(folder);
    const userData = mkdtempSync(join(tmpdir(), "deck-desktop-"));
    writeFileSync(join(userData, "recent.json"), JSON.stringify([folder]));
    const bin = join(mkdtempSync(join(tmpdir(), "deck-bin-")), "bin");
    mkdirSync(bin);
    symlinkSync(FAKE_DECK, join(bin, "deck"));
    const server = createServer((req, res) => {
      const site = (req.headers.host ?? "").split(".")[0] ?? "";
      res.setHeader("content-type", "text/html");
      res.end(SITES[site] ?? SITES["app"]);
    });
    await new Promise<void>((done) => server.listen(0, "127.0.0.1", done));
    const port = (server.address() as AddressInfo).port;
    const app = await launch(
      userData,
      { DECK_BIN: join(bin, "deck"), PATH: `${bin}${delimiter}${process.env["PATH"] ?? ""}`, DECK_FAKE_HANDSHAKE: fake.handshake },
      ["--host-resolver-rules=MAP *.test 127.0.0.1"],
    );
    const ask = async (agent: string, op: string, args: Record<string, unknown> = {}) => {
      const r = await fake.browserOp(agent, op, args, { free: true });
      return String(r["text"] ?? "");
    };
    try {
      const win = await app.firstWindow();
      // The window at the corner of the display, the size of the shots.
      await app.evaluate(({ BrowserWindow, nativeTheme }, [w, h, m]) => {
        nativeTheme.themeSource = m as "dark" | "light";
        const bw = BrowserWindow.getAllWindows()[0];
        bw?.setBounds({ x: 0, y: 0, width: w as number, height: h as number });
      }, [WIDTH, HEIGHT, mode] as const);
      await win.evaluate((m) => localStorage.setItem("deck.desktop.prefs", JSON.stringify({ theme: m })), mode);
      await win.reload();
      await expect(win.locator(".link")).toHaveText("Connected", { timeout: 20_000 });
      await expect.poll(() => fake.browserHosted, { timeout: 10_000 }).toBe(true);
      await win.getByRole("navigation", { name: "Threads" }).getByRole("button", { name: "Fix the pairing lockout", exact: true }).click();

      // The dev server the person starts from, an agent's docs, shop and sign-in.
      await win.keyboard.press("Control+j");
      const drawer = win.getByRole("region", { name: "Terminal" });
      await expect(drawer.locator(".xterm-rows")).toContainText("fake shell");
      await drawer.locator(".xterm").click();
      await win.keyboard.type(`echo Local: http://localhost:${port}/`);
      await win.keyboard.press("Enter");
      await win.getByRole("group", { name: "Dev servers" }).getByRole("button", { name: `Preview :${port}` }).click();
      await win.keyboard.press("Control+j");
      const panel = win.getByRole("complementary", { name: "Side panel" });
      await expect(panel.getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");
      const pane = panel.getByRole("region", { name: "Browser" });
      await expect(pane.getByRole("tab", { name: /Pairing lockout/ })).toBeVisible({ timeout: 15_000 });

      await ask("claude1", "open", { url: `http://docs.test:${port}/guide/limits` });
      await ask("codex1", "open", { url: `http://shop.test:${port}/keyboards` });
      await ask("pi1", "open", { url: `http://login.test:${port}/signin` });
      await expect(pane.getByRole("tab")).toHaveCount(4);
      // The person's own tab, with the agents' beside it.
      await pane.getByRole("tab", { name: /Pairing lockout/ }).click();
      await shot(win, `${mode}-1-three-agent-tabs`);

      // An agent acts: its tab is shown, the strip says what it does.
      await pane.locator('[data-owner="pi1"]').click();
      const snap = await ask("pi1", "snapshot");
      const email = /textbox \[(e\d+)\] "Email"/.exec(snap)?.[1] ?? "";
      const pw = /textbox \[(e\d+)\] "Password"/.exec(snap)?.[1] ?? "";
      const go = /button \[(e\d+)\] "Sign in"/.exec(snap)?.[1] ?? "";
      await ask("pi1", "type", { ref: email, text: "ada@example.com" });
      await ask("pi1", "type", { ref: pw, text: "correct horse battery" });
      await ask("pi1", "click", { ref: go });
      await shot(win, `${mode}-2-agent-acting`);

      // Taken over.
      await pane.getByRole("button", { name: "Take over" }).click();
      await shot(win, `${mode}-3-taken-over`);

      // Empty: every tab closed, the dev server still answering.
      for (let i = 0; i < 4; i++) {
        const tab = pane.getByRole("tab").first();
        await tab.hover();
        await tab.getByRole("button", { name: /^Close/ }).click();
      }
      await expect(pane.getByRole("tab")).toHaveCount(0);
      await shot(win, `${mode}-4-empty-with-dev-server`);
      // Stop the server: no port left.
      server.close();
      server.closeAllConnections();
      await expect(win.getByRole("group", { name: "Dev servers" })).toBeHidden({ timeout: 12_000 });
      await shot(win, `${mode}-5-empty`);
    } finally {
      await app.close();
      server.close();
      await fake.close();
    }
  });
}
