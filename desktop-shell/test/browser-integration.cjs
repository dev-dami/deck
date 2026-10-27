// Full desktop integration with the real engine and a deterministic agent.
// go build -o .deck/tools/deck-integration.exe .
// go build -o .deck/tools/browser-agent.exe desktop-shell/test/fixtures/browser-agent.go
// go build -o .deck/tools/browser-engine.exe desktop-shell/test/fixtures/browser-engine.go
// bun run build (in desktop-shell), then node test/browser-integration.cjs.
// Playwright's Electron debugger transport requires Node on Windows; use Bun
// for dependency installation, build, lint and unit test scripts.
const { _electron: electron, expect } = require('@playwright/test');
const { mkdtempSync, mkdirSync, writeFileSync, realpathSync, rmSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join, resolve, sep, basename } = require('node:path');
const { createServer } = require('node:http');
const { execFileSync } = require('node:child_process');
const assert = require('node:assert/strict');

const root = resolve(__dirname, '..');
const repo = resolve(root, '..');
const artifacts = join(repo, '.deck', 'tools');
const temp = mkdtempSync(join(tmpdir(), 'deck-integration-'));
const home = join(temp, 'home'), project = join(temp, 'browser-fixture'), userData = join(temp, 'desktop');
for (const dir of [join(home, '.deck'), project, userData, artifacts]) mkdirSync(dir, { recursive: true });
writeFileSync(join(home, '.deck', 'config.json'), JSON.stringify({
  claude_binary: join(artifacts, 'browser-agent.exe'), codex_binary: 'no-fixture-codex',
  opencode_binary: 'no-fixture-opencode', antigravity_binary: 'no-fixture-agy', pi_binary: 'no-fixture-pi',
  default_agent: 'claude', auto_title: false, track_changes: false, orchestration: 'off',
}));
writeFileSync(join(userData, 'recent.json'), JSON.stringify([project]));
const report = { temp, checks: [] };
let app, page, sid, deck;
const log = (text) => { report.checks.push(text); console.log(`PASS ${text}`); };
const rpc = async (method, request = {}) => {
  const r = await page.evaluate(([id, method, request]) => window.deck.rpcCall(id, method, request), [deck, method, request]);
  assert.equal(r.ok, true, `${method}: ${JSON.stringify(r)}`);
  return r.value;
};
const plainText = (m) => m.parts.filter(p => p.type === 'text').map(p => p.text).join('');
let turn = 0;
async function agent(op, args = {}) {
  const before = (await rpc('ListMessages', { session_id: sid, limit: 100 })).messages.map(m => m.id);
  await rpc('Send', { session_id: sid, text: `@claude FIXTURE:${JSON.stringify({ op, args, nonce: ++turn })}` });
  let answer;
  await expect.poll(async () => {
    const messages = (await rpc('ListMessages', { session_id: sid, limit: 100 })).messages;
    answer = messages.find(m => !before.includes(m.id) && m.role === 'assistant' && m.finished && plainText(m).startsWith('FIXTURE_RESULT '));
    return answer !== undefined;
  }, { timeout: 45000, message: `Fixture ${op} result` }).toBe(true);
  const result = JSON.parse(plainText(answer).slice('FIXTURE_RESULT '.length));
  return { ...result, text: result.content?.filter(b => b.type === 'text').map(b => b.text).join('\n') ?? '', message: answer };
}
const tabMeta = (result) => {
  const match = result.text.match(/\ntab=([^\n]+)/);
  assert.ok(match, `Missing tab metadata: ${result.text}`);
  return JSON.parse(match[1]);
};
const checkOK = (r) => assert.notEqual(r.isError, true, r.text);
const requests = [];
const fixture = createServer((req, res) => {
  requests.push({ host: req.headers.host, path: req.url, cookie: req.headers.cookie ?? '' });
  res.setHeader('content-type', 'text/html');
  if (req.url === '/login' && req.method === 'POST') {
    req.resume();
    res.writeHead(303, { 'Set-Cookie': 'fixture_session=signed-in; Max-Age=3600; Path=/; HttpOnly; SameSite=Lax', Location: '/account' });
    res.end();
    return;
  }
  if (req.url === '/account') {
    const signedIn = (req.headers.cookie ?? '').includes('fixture_session=signed-in');
    res.end(`<!doctype html><title>Fixture account</title><h1>${signedIn ? 'Signed in fixture account' : 'Signed out fixture account'}</h1>`);
    return;
  }
  res.end('<!doctype html><title>Integration fixture</title><h1>Local browser fixture</h1><label>Message <input aria-label="Message"></label><button onclick="document.title=\'Clicked\'">Submit</button><form method="POST" action="/login"><label>Password <input type="password" name="password" aria-label="Password"></label><button>Sign in</button></form>');
});

async function captureWindow() {
  if (process.platform !== 'win32') return;
  const handle = await app.evaluate(({ BrowserWindow }) => {
    const w = BrowserWindow.getAllWindows()[0]; w.restore(); w.show(); w.focus();
    return w.getNativeWindowHandle().readBigUInt64LE().toString();
  });
  // Page.screenshot omits native child views. Capture only the app's HWND
  // rectangle from the display, after foregrounding this isolated window.
  execFileSync('powershell', ['-NoProfile', '-Command', `
Add-Type -AssemblyName System.Drawing
Add-Type @'
using System;
using System.Runtime.InteropServices;
public class DeckCapture {
 [StructLayout(LayoutKind.Sequential)] public struct Rect { public int Left,Top,Right,Bottom; }
 [DllImport("user32.dll")] public static extern bool GetWindowRect(IntPtr h, out Rect r);
 [DllImport("user32.dll")] public static extern bool SetProcessDpiAwarenessContext(IntPtr c);
}
'@
[DeckCapture]::SetProcessDpiAwarenessContext([IntPtr](-4)) | Out-Null
$rect = New-Object DeckCapture+Rect
if (-not [DeckCapture]::GetWindowRect([IntPtr]([long]$env:DECK_CAPTURE_HANDLE), [ref]$rect)) { throw 'Cannot find test window' }
$bitmap = New-Object Drawing.Bitmap(($rect.Right-$rect.Left),($rect.Bottom-$rect.Top))
$graphics = [Drawing.Graphics]::FromImage($bitmap)
try { $graphics.CopyFromScreen($rect.Left,$rect.Top,0,0,$bitmap.Size); $bitmap.Save($env:DECK_CAPTURE_PATH,[Drawing.Imaging.ImageFormat]::Png) }
finally { $graphics.Dispose(); $bitmap.Dispose() }
`], { windowsHide: true, env: { ...process.env, DECK_CAPTURE_HANDLE: handle, DECK_CAPTURE_PATH: join(artifacts, 'browser-integration-native-window.png') } });
}

async function closeApp() {
  if (!app) return;
  // Schedule quit outside the active inspector evaluation. Playwright itself
  // detaches when Electron reports it is waiting for the debugger.
  const quitting = app;
  app = null;
  let quitTimer;
  const timeout = new Promise((_, reject) => { quitTimer = setTimeout(() => reject(new Error('Electron quit did not finish within 30s')), 30000); });
  const finish = async () => {
    const pid = await quitting.evaluate(() => process.pid);
    await quitting.evaluate(({ app }) => {
    setTimeout(() => app.quit(), 50);
    }).catch(() => {});
    while (true) {
      try { process.kill(pid, 0); }
      catch (e) { if (e.code === 'ESRCH') break; throw e; }
      await new Promise(resolve => setTimeout(resolve, 100));
    }
  };
  try { await Promise.race([finish(), timeout]); }
  catch (e) {
    if (process.platform === 'win32' && quitting.process().exitCode === null)
      execFileSync('taskkill', ['/PID', String(quitting.process().pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' });
    else quitting.process().kill('SIGKILL');
    throw e;
  } finally { clearTimeout(quitTimer); }
  // Playwright 1.56 wraps Electron in cmd.exe on Windows. After Electron
  // has exited, dispose that transport wrapper and its inherited pipes.
  quitting.process().kill();
  quitting.process().stdout?.destroy();
  quitting.process().stderr?.destroy();
}

(async () => {
  await new Promise(done => fixture.listen(0, '127.0.0.1', done));
  const url = `http://127.0.0.1:${fixture.address().port}/`;
  const env = { ...process.env, DECK_TEST_HOME: home, DECK_TEST_ENGINE: join(artifacts, 'deck-integration.exe'), DECK_BIN: join(artifacts, 'browser-engine.exe') };
  delete env.ELECTRON_RUN_AS_NODE;
  delete env.GIT_CONFIG_PARAMETERS;
  const launch = () => electron.launch({ args: [root, `--user-data-dir=${userData}`, '--disable-gpu', '--no-proxy-server', '--host-resolver-rules=MAP site.browser.test 127.0.0.1, MAP denied.browser.test 127.0.0.1'], cwd: root, env, timeout: 45000 });
  app = await launch();
  page = await app.firstWindow();
  page.on('pageerror', e => console.error('RENDERER', e.message));
  await expect(page.locator('.link')).toHaveText('Connected', { timeout: 45000 });
  const opened = await page.evaluate(dir => window.deck.openFolder(dir), project);
  assert.equal(opened.ok, true, JSON.stringify(opened));
  deck = opened.deck.id;
  const info = await rpc('Info');
  assert.ok(info.capabilities.includes('Highlights'));
  assert.ok(info.capabilities.includes('BrowserState'));
  const privateCall = await page.evaluate(id => window.deck.rpcCall(id, 'RegisterBrowserController', {}).then(r => r.ok ? 'allowed' : 'rejected').catch(() => 'rejected'), deck);
  assert.equal(privateCall, 'rejected');
  const room = await rpc('CreateSession', { title: 'Browser and Highlights integration', level: 'off' });
  sid = room.id;
  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: 'Browser and Highlights integration', exact: true }).click();
  await expect.poll(async () => (await rpc('BrowserState', { session_id: sid })).available).toBe(true);
  log('real engine/controller connects through Electron IPC and local RPC');

  const finding = await agent('highlight.add', { key: 'fixture-risk', kind: 'blocker', title: 'Inspect the integration result', detail: 'Explicit fixture finding' });
  checkOK(finding);
  const record = JSON.parse(finding.text);
  assert.ok(record.source.turn_id);
  await agent('summary.update', { text: 'Checking the desktop integration' }).then(checkOK);
  await expect(page.getByRole('button', { name: /Highlights.*1/ }).first()).toBeVisible();
  await page.getByRole('button', { name: /Highlights.*1/ }).first().click();
  const highlights = page.getByRole('region', { name: 'Highlights', exact: true });
  await expect(highlights.getByText('Inspect the integration result', { exact: true })).toBeVisible();
  await expect.poll(async () => (await rpc('Highlights', { session_id: sid })).items[0].unread).toBe(false);
  assert.equal((await rpc('Highlights', { session_id: sid })).items[0].state, 'open');
  await highlights.getByRole('tab', { name: 'Summary', exact: true }).click();
  await expect(highlights.getByText('Checking the desktop integration', { exact: true })).toBeVisible();
  await highlights.getByRole('tab', { name: 'Highlights', exact: true }).click();
  const enriched = (await rpc('Highlights', { session_id: sid })).items[0];
  assert.equal(enriched.source.message_id, finding.message.id);
  await highlights.getByRole('button', { name: 'Show source', exact: true }).click();
  const messageRow = page.locator(`[data-mid="${finding.message.id}"], [data-mids~="${finding.message.id}"]`).first();
  await expect(messageRow).toBeVisible();
  await messageRow.hover();
  if (await messageRow.getByRole('button', { name: 'Pin to Highlights', exact: true }).count() === 0) {
    writeFileSync(join(artifacts, 'browser-integration-pin-row.html'), await messageRow.evaluate(el => el.outerHTML));
  }
  await expect(messageRow.getByRole('button', { name: 'Pin to Highlights', exact: true }).first()).toBeVisible({ timeout: 10000 });
  await messageRow.getByRole('button', { name: 'Pin to Highlights', exact: true }).first().click();
  await messageRow.getByRole('button', { name: 'Pin to Highlights', exact: true }).first().click();
  await expect.poll(async () => (await rpc('Highlights', { session_id: sid })).items.length).toBe(2);
  const pin = (await rpc('Highlights', { session_id: sid })).items.find(i => i.key === `pin:${finding.message.id}`);
  assert.equal(pin.agent, 'claude');
  await highlights.getByRole('button', { name: `Dismiss ${pin.title}`, exact: true }).click();
  await expect.poll(async () => (await rpc('Highlights', { session_id: sid })).items.find(i => i.id === pin.id).state).toBe('dismissed');
  log('source enrichment, jump and repeated UI pin preserve author and deduplicate');
  log('agent-authored Highlights and Summary appear; viewing clears unread only');

  const createdTask = await agent('task.create', { title: 'Fixture decision remains blocked', owner: 'me' });
  checkOK(createdTask);
  const taskId = createdTask.text.match(/\bt\d+\b/)[0];
  checkOK(await agent('summary.update', { text: 'Checking the desktop integration', source: { task_id: taskId } }));
  await highlights.getByRole('tab', { name: 'Summary', exact: true }).click();
  await expect(highlights.getByText('agent update', { exact: true })).toBeVisible();
  await highlights.getByRole('tab', { name: 'Highlights', exact: true }).click();
  checkOK(await agent('task.update', { id: taskId, status: 'blocked' }));
  await expect(highlights.locator('[data-attention]')).toContainText('Fixture decision remains blocked', { timeout: 15000 });
  assert.equal((await rpc('Highlights', { session_id: sid })).items.length, 2);
  checkOK(await agent('task.update', { id: taskId, status: 'ready' }));
  checkOK(await agent('task.done', { id: taskId, result: 'Fixture decision handled explicitly' }));
  await expect(highlights.locator('[data-attention]')).toHaveCount(0, { timeout: 15000 });
  log('Needs you derives blocked task state without new Highlights; task-linked summary stays agent-authored');

  await highlights.getByRole('button', { name: 'Resolve Inspect the integration result', exact: true }).click();
  await expect.poll(async () => (await rpc('Highlights', { session_id: sid })).items.find(i => i.id === record.id).state).toBe('resolved');
  log('explicit UI resolution persists through the real RPC');

  const openedTab = await agent('browser.open', { url });
  checkOK(openedTab);
  const tab = tabMeta(openedTab);
  const refused = await agent('browser.navigate', { tab_id: tab.id, url: 'file:///fixture-denied' });
  assert.equal(refused.isError, true);
  await page.getByRole('complementary', { name: 'Side panel' }).getByRole('tab', { name: 'Browser', exact: true }).click();
  const browser = page.getByRole('region', { name: 'Browser', exact: true });
  await expect(browser.getByRole('textbox', { name: 'Address' })).toHaveValue(url);
  const shown = () => app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].contentView.children.map(v => ({ visible: v.getVisible(), ...v.getBounds() })));
  await expect.poll(async () => (await shown()).some(v => v.visible && v.width > 100 && v.height > 100)).toBe(true);
  const snap = await agent('browser.snapshot', { tab_id: tab.id });
  checkOK(snap);
  assert.ok(snap.text.includes('UNTRUSTED PAGE CONTENT'));
  let meta = tabMeta(snap);
  const input = snap.text.match(/\[(e\d+)\] textbox: Message/)[1];
  const typed = await agent('browser.type', { tab_id: tab.id, element_ref: input, snapshot_version: meta.snapshot_version, text: 'typed by agent' });
  checkOK(typed);
  const clickSnap = await agent('browser.snapshot', { tab_id: tab.id });
  const button = clickSnap.text.match(/\[(e\d+)\] button: Submit/)[1];
  checkOK(await agent('browser.click', { tab_id: tab.id, element_ref: button, snapshot_version: tabMeta(clickSnap).snapshot_version }));
  await expect(browser.getByRole('tab', { name: 'Clicked', exact: true })).toBeVisible();
  const stale = await agent('browser.click', { tab_id: tab.id, element_ref: button, snapshot_version: tabMeta(clickSnap).snapshot_version });
  assert.equal(stale.isError, true);
  assert.ok(stale.text.includes('stale_snapshot'));
  const image = await agent('browser.screenshot', { tab_id: tab.id });
  checkOK(image);
  const png = image.content.find(b => b.type === 'image');
  assert.equal(png.mimeType, 'image/png');
  assert.equal(png.signature, '89504e470d0a1a0a');
  assert.ok(png.bytes > 100);
  log('agent open/snapshot/type/click/PNG traverses real engine, native Chromium and MCP helper');
  await page.keyboard.press('Control+k');
  await expect.poll(async () => (await shown()).every(v => !v.visible)).toBe(true);
  await page.keyboard.press('Escape');
  await expect.poll(async () => (await shown()).some(v => v.visible)).toBe(true);
  await app.evaluate(({ BrowserWindow }) => BrowserWindow.getAllWindows()[0].minimize());
  checkOK(await agent('browser.snapshot', { tab_id: tab.id }));
  const background = await agent('browser.screenshot', { tab_id: tab.id });
  checkOK(background);
  assert.ok(background.content.some(b => b.type === 'image' && b.bytes > 100));
  await app.evaluate(({ BrowserWindow }) => { const w = BrowserWindow.getAllWindows()[0]; w.restore(); w.focus(); });
  log('native view hides under dialogs; agent read/capture works with window minimized');

  await browser.getByRole('button', { name: 'Take control', exact: true }).click();
  await expect(browser.getByRole('button', { name: 'Release', exact: true })).toBeVisible();
  const blocked = await agent('browser.snapshot', { tab_id: tab.id });
  assert.equal(blocked.isError, true);
  assert.ok(blocked.text.includes('user_in_control'));
  await browser.getByRole('button', { name: 'Release', exact: true }).click();
  checkOK(await agent('browser.snapshot', { tab_id: tab.id }));
  log('UI Take control blocks agent observation; explicit Release restores access');

  const site = `http://site.browser.test:${fixture.address().port}`;
  const visiting = agent('browser.navigate', { tab_id: tab.id, url: site + '/' });
  const approval = page.getByRole('group', { name: 'Website access', exact: true });
  await expect(approval).toContainText(site, { timeout: 15000 });
  assert.ok(!requests.some(r => r.host.startsWith('site.browser.test')));
  await page.getByRole('complementary', { name: 'Side panel' }).getByRole('tab', { name: 'Highlights', exact: true }).click();
  await expect(highlights.locator('[data-approval-row]')).toContainText(site);
  await approval.getByRole('button', { name: 'Approve', exact: true }).click();
  checkOK(await visiting);
  await expect(highlights.locator('[data-approval-row]')).toHaveCount(0);
  await page.getByRole('complementary', { name: 'Side panel' }).getByRole('tab', { name: 'Browser', exact: true }).click();
  const deniedVisit = agent('browser.navigate', { tab_id: tab.id, url: `http://denied.browser.test:${fixture.address().port}/` });
  await expect(approval).toContainText('denied.browser.test', { timeout: 15000 });
  await approval.getByRole('button', { name: 'Decline', exact: true }).click();
  const denied = await deniedVisit;
  assert.equal(denied.isError, true);
  assert.ok(denied.text.includes('origin_denied'));
  assert.ok(!requests.some(r => r.host.startsWith('denied.browser.test')));
  log('site approval is visible in existing Needs you state; approval/decline gates network access');

  await browser.getByRole('button', { name: 'Take control', exact: true }).click();
  // Synthetic user sign-in in the native view; never sent in an agent op.
  await app.evaluate(async ({ webContents }, origin) => {
    const wc = webContents.getAllWebContents().find(w => w.getURL().startsWith(origin));
    if (!wc) throw new Error('Cannot find native sign-in view');
    await wc.executeJavaScript('document.querySelector("input[type=password]").value="synthetic-only-secret"; document.querySelector("form").requestSubmit()');
  }, site);
  await expect(browser.getByRole('textbox', { name: 'Address' })).toHaveValue(site + '/account');
  const hiddenCapture = await agent('browser.screenshot', { tab_id: tab.id });
  assert.equal(hiddenCapture.isError, true);
  assert.ok(hiddenCapture.text.includes('user_in_control'));
  await browser.getByRole('button', { name: 'Release', exact: true }).click();
  const account = await agent('browser.snapshot', { tab_id: tab.id });
  checkOK(account);
  assert.ok(account.text.includes('Signed in fixture account'));
  assert.ok(!account.text.includes('synthetic-only-secret'));
  await captureWindow();
  log('synthetic user sign-in works under takeover; agent captures stay paused');

  await page.screenshot({ path: join(artifacts, 'browser-integration-desktop.png') });
  await page.reload();
  await expect(page.locator('.link')).toHaveText('Connected', { timeout: 30000 });
  await expect.poll(async () => (await rpc('Highlights', { session_id: sid })).items.find(i => i.id === record.id).state).toBe('resolved');
  const persisted = await rpc('Highlights', { session_id: sid });
  assert.equal(persisted.items.find(i => i.id === record.id).unread, false);
  assert.equal(persisted.summaries[0].text, 'Checking the desktop integration');
  log('resolved state, seen state and explicit summary survive renderer reload');
  await closeApp();
  app = await launch();
  page = await app.firstWindow();
  await expect(page.locator('.link')).toHaveText('Connected', { timeout: 45000 });
  assert.equal((await rpc('Highlights', { session_id: sid })).items.find(i => i.id === record.id).state, 'resolved');
  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: 'Browser and Highlights integration', exact: true }).click();
  const again = agent('browser.open', { url: site + '/account' });
  await expect(page.getByRole('group', { name: 'Website access', exact: true })).toContainText(site, { timeout: 15000 });
  await page.getByRole('group', { name: 'Website access', exact: true }).getByRole('button', { name: 'Approve', exact: true }).click();
  const freshTab = await again;
  checkOK(freshTab);
  const restored = await agent('browser.snapshot', { tab_id: tabMeta(freshTab).id });
  checkOK(restored);
  assert.ok(restored.text.includes('Signed in fixture account'));
  log('signed-in profile and Highlights survive full app/engine restart; site permission is renewed');
  report.ok = true;
})().catch(async e => {
  report.ok = false;
  report.error = e.stack;
  console.error(e);
  if (page) {
    await page.screenshot({ path: join(artifacts, 'browser-integration-failure.png') }).catch(() => {});
    writeFileSync(join(artifacts, 'browser-integration-dom.txt'), await page.locator('body').innerText().catch(() => 'unavailable'));
  }
  process.exitCode = 1;
}).finally(async () => {
  await closeApp().catch(e => { console.error(e); report.ok = false; report.teardownError = e.stack; process.exitCode = 1; });
  fixture.closeAllConnections();
  fixture.close();
  const target = realpathSync(temp);
  const parent = realpathSync(tmpdir()) + sep;
  if (!target.startsWith(parent) || !basename(target).startsWith('deck-integration-')) throw new Error('Unexpected integration temp directory');
  try { rmSync(target, { recursive: true, force: true, maxRetries: 20, retryDelay: 100 }); }
  catch (e) { report.ok = false; report.cleanupError = e.stack; process.exitCode = 1; }
  writeFileSync(join(artifacts, 'browser-integration-results.json'), JSON.stringify(report, null, 2));
});
