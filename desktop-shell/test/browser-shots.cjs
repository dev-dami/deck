// Screenshots of the agent browser in the real app, dark and light, for
// design review: the strip with several agents' tabs, an agent acting, a
// takeover, the empty state and Highlights. The pages are views laid over the
// window, which the window's own screenshot leaves out, so these are taken
// from the X display:
//
//   (build as test/browser-integration.cjs says), then
//   SHOTS=<dir> xvfb-run -a -s "-screen 0 1600x1000x24" node test/browser-shots.cjs
//
// Needs Linux with ImageMagick's `import`; ELECTRON_OVERRIDE_DIST_PATH and
// ELECTRON_EXTRA_LAUNCH_ARGS=--no-sandbox may be set for an Electron outside node_modules.
const { _electron: electron, expect } = require('@playwright/test');
const { mkdtempSync, mkdirSync, writeFileSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const { createServer } = require('node:http');
const { execFileSync } = require('node:child_process');
const assert = require('node:assert/strict');

const root = resolve(__dirname, '..');
const repo = resolve(root, '..');
const artifacts = join(repo, '.deck', 'tools');
const out = process.env.SHOTS;
if (!out) throw new Error('SHOTS=<dir> is required');
mkdirSync(out, { recursive: true });
const temp = mkdtempSync(join(tmpdir(), 'deck-shots-'));
const home = join(temp, 'home'), project = join(temp, 'deck'), userData = join(temp, 'desktop');
for (const dir of [join(home, '.deck'), project, userData]) mkdirSync(dir, { recursive: true });
writeFileSync(join(home, '.deck', 'config.json'), JSON.stringify({
  claude_binary: join(artifacts, 'browser-agent.exe'), codex_binary: 'none', opencode_binary: 'none',
  antigravity_binary: 'none', pi_binary: 'none', default_agent: 'claude', auto_title: false, track_changes: false, orchestration: 'off',
}));
writeFileSync(join(userData, 'recent.json'), JSON.stringify([project]));

const WIDTH = 1360, HEIGHT = 860;
let app, page, deck, sid;
const rpc = async (method, request = {}) => {
  const r = await page.evaluate(([id, method, request]) => window.deck.rpcCall(id, method, request), [deck, method, request]);
  assert.equal(r.ok, true, `${method}: ${JSON.stringify(r)}`);
  return r.value;
};
const plainText = (m) => m.parts.filter(p => p.type === 'text').map(p => p.text).join('');
let turn = 0;
// One op as an agent, through the real engine; resolves with the agent's result.
function agent(who, op, args = {}) {
  return (async () => {
    const before = (await rpc('ListMessages', { session_id: sid, limit: 100 })).messages.map(m => m.id);
    await rpc('Send', { session_id: sid, text: `@${who} FIXTURE:${JSON.stringify({ op, args, nonce: ++turn })}` });
    let answer;
    await expect.poll(async () => {
      const messages = (await rpc('ListMessages', { session_id: sid, limit: 100 })).messages;
      answer = messages.find(m => !before.includes(m.id) && m.role === 'assistant' && m.finished && plainText(m).startsWith('FIXTURE_RESULT '));
      return answer !== undefined;
    }, { timeout: 60000, message: `${who} ${op}` }).toBe(true);
    const result = JSON.parse(plainText(answer).slice('FIXTURE_RESULT '.length));
    return { ...result, text: result.content?.filter(b => b.type === 'text').map(b => b.text).join('\n') ?? '' };
  })();
}
const tabId = (r) => JSON.parse(r.text.match(/\ntab=([^\n]+)/)[1]).id;

const style = `<style>body{font:15px/1.5 system-ui,sans-serif;margin:0;color:#1d1d1f;background:#fff}header{padding:14px 28px;border-bottom:1px solid #e5e5ea;display:flex;gap:18px;align-items:center}
header b{font-size:17px}nav a{color:#555;margin-right:14px;text-decoration:none}main{padding:28px;max-width:640px}h1{font-size:26px;margin:0 0 6px}p{color:#555}
.card{border:1px solid #e5e5ea;border-radius:12px;padding:16px;margin:14px 0}button{font:inherit;padding:8px 16px;border-radius:8px;border:0;background:#0a84ff;color:#fff}
input{font:inherit;padding:8px 10px;border:1px solid #ccc;border-radius:8px;width:260px}label{display:block;margin:10px 0}code{background:#f2f2f7;padding:2px 6px;border-radius:5px}</style>`;
const SITES = {
  '/app': `${style}<title>Pairing lockout</title><header><b>Deck app</b><nav><a href="#">Rooms</a><a href="#">Settings</a></nav></header><main><h1>Pairing lockout</h1><p>The dev server is up. Three failed codes lock a device for a minute.</p><div class="card"><b>Locked devices</b><p>No device is locked.</p></div><button>Reset limiter</button></main>`,
  '/docs': `${style}<title>Rate limiting - Guide</title><header><b>Guide</b><nav><a href="#">Start</a><a href="#">Auth</a><a href="#">Limits</a></nav></header><main><h1>Rate limiting</h1><p>A limiter keys on the device, not the address. Use <code>Retry-After</code> to tell a caller when to return.</p><div class="card"><b>Token bucket</b><p>Refills one token a second up to a burst of five.</p></div></main>`,
  '/shop': `${style}<title>Keyboards - Corner Shop</title><header><b>Corner Shop</b><nav><a href="#">Keyboards</a><a href="#">Mice</a><a href="#">Cart</a></nav></header><main><h1>Keyboards</h1><div class="card"><b>Low profile, 65%</b><p>Hot-swap, USB-C, 80 hours on a charge.</p><button>Add to cart</button></div></main>`,
  '/login': `${style}<title>Sign in - Example</title><header><b>Example</b></header><main><h1>Sign in</h1><form><label>Email<br><input type="email" name="email"></label><label>Password<br><input type="password" name="pw"></label><button>Sign in</button></form></main>`,
};
const site = createServer((req, res) => {
  res.setHeader('content-type', 'text/html');
  res.end(SITES[req.url.split('?')[0]] ?? SITES['/app']);
});

async function shot(name) {
  await page.waitForTimeout(700); // let the native views paint
  execFileSync('import', ['-window', 'root', '-crop', `${WIDTH}x${HEIGHT}+0+0`, '+repage', join(out, `${name}.png`)]);
  console.log(`shot ${name}`);
}

const panel = () => page.getByRole('complementary', { name: 'Side panel' });
const browserTab = () => panel().getByRole('tab', { name: 'Browser', exact: true });
const strip = () => page.getByRole('region', { name: 'Browser', exact: true }).getByRole('tablist', { name: 'Open pages' });

async function theme(mode) {
  await app.evaluate(({ BrowserWindow, nativeTheme }, m) => {
    nativeTheme.themeSource = m;
    const w = BrowserWindow.getAllWindows()[0];
    w.setSize(1360, 860);
    w.setPosition(0, 0);
  }, mode);
  await page.evaluate(m => localStorage.setItem('deck.desktop.prefs', JSON.stringify({ theme: m })), mode);
  await page.reload();
  await expect(page.locator('.link')).toHaveText('Connected', { timeout: 45000 });
}

async function openRoom(title) {
  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: title, exact: true }).click();
  if ((await panel().count()) === 0) await page.getByRole('button', { name: 'Show the side panel' }).click();
  await browserTab().click();
}

(async () => {
  await new Promise(done => site.listen(0, '127.0.0.1', done));
  const base = `http://127.0.0.1:${site.address().port}`;
  const env = { ...process.env, DECK_TEST_HOME: home, DECK_TEST_ENGINE: join(artifacts, 'deck-integration.exe'), DECK_BIN: join(artifacts, 'browser-engine.exe') };
  delete env.ELECTRON_RUN_AS_NODE;
  app = await electron.launch({ args: [root, `--user-data-dir=${userData}`, '--disable-gpu', '--no-proxy-server'], cwd: root, env, timeout: 45000 });
  page = await app.firstWindow();
  page.on('pageerror', e => console.error('RENDERER', e.message));
  await expect(page.locator('.link')).toHaveText('Connected', { timeout: 45000 });
  const opened = await page.evaluate(dir => window.deck.openFolder(dir), project);
  assert.equal(opened.ok, true, JSON.stringify(opened));
  deck = opened.deck.id;
  // Three named agents, each with a hue of its own (a kind mention would reach any of them).
  for (const [name, color, role] of [['atlas', '#7a6ff0', 'maps the codebase'], ['scout', '#e0793c', 'reads docs'], ['tester', '#3f9d6a', 'tries the flows']]) {
    await rpc('SaveProfile', { profile: { name, kind: 'claude', role, color, scope: 'project' } });
  }
  const room = await rpc('CreateSession', { title: 'Fix the pairing lockout', level: 'off' });
  sid = room.id;
  await rpc('CreateSession', { title: 'Nothing open yet', level: 'off' });
  await theme('dark');
  await openRoom('Fix the pairing lockout');
  await expect.poll(async () => (await rpc('BrowserState', { session_id: sid })).available).toBe(true);

  // A reload of the window closes the tabs (they live as long as the window's
  // controller), so each look opens them again.
  async function populate() {
    // The person opens the dev server first.
    const address = page.getByRole('region', { name: 'Browser', exact: true }).getByRole('textbox', { name: 'Address' });
    await address.fill(`${base}/app`);
    await address.press('Enter');
    await expect(strip().getByRole('tab', { name: 'Pairing lockout' })).toBeVisible({ timeout: 20000 });
    // Then the agents, each in a tab of its own.
    const mine = await agent('atlas', 'browser.open', { url: `${base}/docs` });
    const scout = await agent('scout', 'browser.open', { url: `${base}/shop` });
    const tester = await agent('tester', 'browser.open', { url: `${base}/login` });
    for (const r of [mine, scout, tester]) assert.notEqual(r.isError, true, r.text);
    await expect(strip().getByRole('tab')).toHaveCount(4, { timeout: 20000 });
    return { scout: tabId(scout) };
  }
  // A highlight and a summary for the Highlights panel.
  const flagged = await agent('atlas', 'highlight.add', { key: 'limiter', kind: 'finding', title: 'The limiter keys on the device', detail: 'Found in the Guide: a caller waits per device, not per address.' });
  assert.notEqual(flagged.isError, true, flagged.text);
  await agent('scout', 'summary.update', { text: 'Reading the rate limiting guide and the shop for comparison' });

  for (const mode of ['dark', 'light']) {
    if (mode === 'light') {
      await theme('light');
      await openRoom('Fix the pairing lockout');
    }
    const ids = await populate();
    await strip().getByRole('tab', { name: 'Rate limiting - Guide' }).click();
    await page.waitForTimeout(3200); // what the agents just did stops showing
    await shot(`${mode}-1-tabs`);

    // An agent acting: a wait that runs for a while shows on its tab.
    const waiting = agent('scout', 'browser.wait', { tab_id: ids.scout, text: 'never shown', timeout_s: 10 });
    await expect(strip().locator('[data-tab][data-working]')).toBeVisible({ timeout: 30000 });
    await strip().locator('[data-tab][data-working]').getByRole('tab').click();
    await shot(`${mode}-2-agent-acting`);
    await waiting;

    // The person takes the tab over.
    await strip().getByRole('tab', { name: 'Keyboards - Corner Shop' }).click();
    const region = page.getByRole('region', { name: 'Browser', exact: true });
    await region.getByRole('button', { name: 'Take control', exact: true }).click();
    await expect(region.getByRole('button', { name: 'Release', exact: true })).toBeVisible();
    await shot(`${mode}-3-takeover`);
    await region.getByRole('button', { name: 'Release', exact: true }).click();

    // A room with no page open.
    await openRoom('Nothing open yet');
    await expect(page.getByRole('region', { name: 'Browser', exact: true }).getByText('No page is open.')).toBeVisible();
    await shot(`${mode}-4-empty`);

    // Highlights, beside the same room.
    await openRoom('Fix the pairing lockout');
    await panel().getByRole('tab', { name: 'Highlights', exact: true }).click();
    await expect(page.getByRole('region', { name: 'Highlights', exact: true }).getByText('The limiter keys on the device')).toBeVisible();
    await shot(`${mode}-5-highlights`);
    await browserTab().click();
  }
})().catch(async e => {
  console.error(e);
  if (page) await page.screenshot({ path: join(out, 'failure.png') }).catch(() => {});
  process.exitCode = 1;
}).finally(async () => {
  site.closeAllConnections();
  site.close();
  if (app) {
    const proc = app.process();
    await app.evaluate(({ app }) => { setTimeout(() => app.quit(), 50); }).catch(() => {});
    await new Promise(r => setTimeout(r, 1500));
    proc.kill('SIGKILL');
  }
  rmSync(temp, { recursive: true, force: true, maxRetries: 10, retryDelay: 100 });
});
