// Run after npm run build. Build fixtures with:
// go build -o .deck/tools/artifact-engine.exe desktop-shell/test/fixtures/artifact-engine.go
// go build -o .deck/tools/artifact-agent.exe desktop-shell/test/fixtures/browser-agent.go
// This uses production workspace, adapter, hub, RPC, renderer and native views,
// with a deterministic CLI fixture and explicit isolated temporary data stores.
const { _electron: electron, expect } = require('@playwright/test');
const { mkdtempSync, mkdirSync, writeFileSync } = require('node:fs');
const { tmpdir } = require('node:os');
const { join, resolve } = require('node:path');
const { execFileSync } = require('node:child_process');
const assert = require('node:assert/strict');
const root = resolve(__dirname, '..'), repo = resolve(root, '..');
const output = join(repo, '.deck', 'tools');
const temp = mkdtempSync(join(tmpdir(), 'deck-artifact-integration-'));
const project = join(temp, 'project'), data = join(temp, 'engine'), userData = join(temp, 'desktop');
for (const path of [project, data, userData, output]) mkdirSync(path, { recursive: true });
writeFileSync(join(userData, 'recent.json'), JSON.stringify([project]));
const report = { temp, checks: [] };
let app, page, deck, sid, turn = 0;
const pass = (message) => { report.checks.push(message); console.log('PASS ' + message); };
const call = (method, request = {}) => page.evaluate(([id, method, request]) => window.deck.rpcCall(id, method, request), [deck, method, request]);
const rpc = async (method, request = {}) => { const r = await call(method, request); assert.equal(r.ok, true, JSON.stringify(r)); return r.value; };
const plainText = (m) => m.parts.filter(p => p.type === 'text').map(p => p.text).join('');
async function agent(op, args = {}) {
  const before = new Set((await rpc('ListMessages', { session_id: sid, limit: 100 })).messages.map(m => m.id));
  await rpc('Send', { session_id: sid, text: `@claude FIXTURE:${JSON.stringify({ op, args, nonce: ++turn })}` });
  let result;
  await expect.poll(async () => {
    const messages = (await rpc('ListMessages', { session_id: sid, limit: 100 })).messages;
    const answer = messages.find(m => !before.has(m.id) && m.role === 'assistant' && m.finished && plainText(m).startsWith('FIXTURE_RESULT '));
    if (!answer) return false;
    result = JSON.parse(plainText(answer).slice('FIXTURE_RESULT '.length));
    result.text = result.content?.filter(b => b.type === 'text').map(b => b.text).join('\n') ?? '';
    return true;
  }, { timeout: 60000, message: op }).toBe(true);
  return result;
}
const good = (r) => { assert.notEqual(r.isError, true, r.text); return JSON.parse(r.text); };
const native = (expression) => app.evaluate(async ({ webContents }, expression) => {
  const wc = webContents.getAllWebContents().find(w => w.getURL().startsWith('deck-artifact://'));
  return wc ? wc.executeJavaScript(expression) : null;
}, expression);
async function close() {
  if (!app) return;
  const target = app; app = null;
  const pid = await target.evaluate(() => process.pid);
  await target.evaluate(({ app }) => { setTimeout(() => app.quit(), 20); }).catch(() => {});
  const end = Date.now() + 30000;
  while (Date.now() < end) {
    try { process.kill(pid, 0); } catch (e) { if (e.code === 'ESRCH') break; throw e; }
    await new Promise(r => setTimeout(r, 100));
  }
  try { process.kill(pid, 0); execFileSync('taskkill', ['/PID', String(target.process().pid), '/T', '/F'], { windowsHide: true, stdio: 'ignore' }); }
  catch (e) { if (e.code !== 'ESRCH') throw e; }
  target.process().kill(); target.process().stdout?.destroy(); target.process().stderr?.destroy();
}
async function launch() {
  const env = { ...process.env, DECK_BIN: join(output, 'artifact-engine.exe'), DECK_ARTIFACT_TEST_DATA: data, DECK_ARTIFACT_TEST_AGENT: join(output, 'artifact-agent.exe'), DECK_ARTIFACT_TRACE: '1' };
  delete env.ELECTRON_RUN_AS_NODE;
  app = await electron.launch({ args: [root, `--user-data-dir=${userData}`, '--disable-gpu'], cwd: root, env, timeout: 60000 });
  app.process().stderr?.on('data', b => { if (/panic:|TypeError|Unhandled/.test(String(b))) console.error(String(b)); });
  app.process().stdout?.on('data', b => { if (String(b).includes('ARTIFACT_TRACE')) console.log(String(b).trim()); });
  page = await app.firstWindow();
  page.on('pageerror', e => console.error('RENDERER', e.message));
  await expect(page.locator('.link')).toHaveText('Connected', { timeout: 60000 });
  const opened = await page.evaluate(dir => window.deck.openFolder(dir), project);
  assert.equal(opened.ok, true, JSON.stringify(opened)); deck = opened.deck.id;
  await expect(page.locator('.link')).toHaveText('Connected', { timeout: 60000 });
}
const main = async () => {
  await launch();
  assert.ok((await rpc('Info')).capabilities.includes('ArtifactAction'));
  sid = (await rpc('CreateSession', { title: 'Artifacts integration', level: 'off' })).id;
  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: 'Artifacts integration', exact: true }).click();
  const files = [
    { path: 'App.tsx', content: 'import {useState} from "react"; import {Label} from "./parts/Label"; import "./style.css"; export default function App(){const [n,setN]=useState(0);return <main><h1><Label/></h1><button onClick={()=>setN(n+1)}>Count {n}</button></main>}' },
    { path: 'parts/Label.tsx', content: 'export const Label = () => <>React artifact café</>;' },
    { path: 'style.css', content: 'body{background:#102338;color:#fff;font:20px system-ui;padding:24px}button{padding:12px}' },
  ];
  const created = good(await agent('artifact.create', { key: 'react-app', title: 'React counter', kind: 'react', entrypoint: 'App.tsx', files }));
  const id = created.artifact.id;
  assert.equal(created.revision.agent, 'claude');
  await expect(page.locator(`[data-artifact="${id}"]`)).toBeVisible({ timeout: 60000 });
  await expect(page.getByRole('button', { name: 'Open artifact: React counter', exact: true })).toBeVisible();
  await expect.poll(() => native('document.body.innerText'), { timeout: 60000 }).toContain('React artifact café');
  await native('document.querySelector("button").click()');
  await expect.poll(() => native('document.body.innerText')).toContain('Count 1');
  await page.screenshot({ path: join(output, 'artifacts-panel.png') });
  const preview = await app.evaluate(async ({ webContents }) => {
    const wc = webContents.getAllWebContents().find(w => w.getURL().startsWith('deck-artifact://'));
    return (await wc.capturePage()).toPNG().toString('base64');
  });
  writeFileSync(join(output, 'artifacts-react.png'), Buffer.from(preview, 'base64'));
  pass('agent MCP create opens a provenance-linked chat card and interactive multi-file TSX/CSS preview');
  const panel = page.locator(`[data-artifact="${id}"]`);
  await panel.getByRole('button', { name: 'Source', exact: true }).click();
  await expect(panel.locator('pre')).toContainText('useState');
  await expect.poll(() => native('document.body.innerText')).toBe(null);
  await app.evaluate(({ BrowserWindow }, path) => {
    globalThis.artifactDownload = null;
    BrowserWindow.getAllWindows()[0].webContents.session.once('will-download', (_event, item) => {
      item.setSavePath(path);
      item.once('done', (_event, state) => { globalThis.artifactDownload = state; });
    });
  }, join(output, 'artifacts-source.zip'));
  await panel.getByRole('button', { name: 'Download source', exact: true }).click();
  await expect.poll(() => app.evaluate(() => globalThis.artifactDownload), { timeout: 30000 }).toBe('completed');
  // Independent consumer validates ZIP headers, CRCs, paths and UTF-8.
  execFileSync('python', ['-c', 'import zipfile,sys; z=zipfile.ZipFile(sys.argv[1]); assert z.testzip() is None; assert sorted(z.namelist()) == ["App.tsx", "parts/Label.tsx", "style.css"]; assert "café" in z.read("parts/Label.tsx").decode("utf-8")', join(output, 'artifacts-source.zip')], { windowsHide: true });
  pass('source tab destroys the native page; source download round-trips through Python zipfile with folders, CRC and Unicode intact');
  await panel.getByRole('button', { name: 'Preview', exact: true }).click();
  good(await agent('artifact.update', { id, base_revision: 1, title: 'React counter', entrypoint: 'App.tsx', files: [{ path: 'App.tsx', content: 'import fs from "node:fs"; export default function App(){return <p>{fs}</p>}' }] }));
  await expect(panel.locator('[data-artifact-errors]')).toContainText('Cannot import', { timeout: 60000 });
  await expect.poll(async () => (await rpc('ArtifactRead', { session_id: sid, id, revision: 2 })).builds?.some(b => !b.ok)).toBe(true);
  const feedback = good(await agent('artifact.read', { id, revision: 2 }));
  assert.ok(JSON.stringify(feedback).includes('Cannot import'));
  pass('compile failure is visible in Errors and persisted for agents to read');
  good(await agent('artifact.update', { id, base_revision: 2, title: 'React counter', entrypoint: 'App.tsx', files: [{ path: 'App.tsx', content: 'export default function App(){return <h1>Revision three</h1>}' }] }));
  await panel.getByRole('button', { name: 'Preview', exact: true }).click();
  await expect.poll(() => native('document.body.innerText'), { timeout: 60000 }).toContain('Revision three');
  await panel.getByLabel('Revision', { exact: true }).selectOption('1');
  await expect.poll(() => native('document.body.innerText'), { timeout: 60000 }).toContain('React artifact café');
  await panel.getByRole('button', { name: 'Restore', exact: true }).click();
  await expect.poll(async () => (await rpc('Artifacts', { session_id: sid })).items.find(a => a.id === id).current_revision).toBe(4);
  const conflict = await call('ArtifactAction', { session_id: sid, action: 'restore', id, base_revision: 1, restore_revision: 1 });
  assert.equal(conflict.ok, false); assert.equal(conflict.code, 10); assert.equal(conflict.artifact_conflict.currentRevision, 4);
  pass('live edits, historical preview and restore append revisions; stale restore exposes the typed conflict through IPC');
  const html = good(await agent('artifact.create', { key: 'html', title: 'Nested HTML', kind: 'html', entrypoint: 'nested/index.html', files: [
    { path: 'nested/index.html', content: '<link rel="stylesheet" href="style.css"><h1 id="h">Before</h1><script src="app.js"></script>' },
    { path: 'nested/style.css', content: 'h1{color:rgb(1, 2, 3)}' },
    { path: 'nested/app.js', content: 'document.getElementById("h").textContent="Offline siblings work"' },
  ] }));
  await expect.poll(() => native('document.body.innerText')).toContain('Offline siblings work');
  assert.equal(await native('getComputedStyle(document.querySelector("h1")).color'), 'rgb(1, 2, 3)');
  good(await agent('artifact.create', { key: 'svg', title: 'SVG diagram', kind: 'svg', entrypoint: 'diagram.svg', files: [{ path: 'diagram.svg', content: '<svg xmlns="http://www.w3.org/2000/svg" width="250" height="100"><text x="10" y="40">SVG works</text></svg>' }] }));
  await expect.poll(() => native('document.documentElement.textContent')).toContain('SVG works');
  good(await agent('artifact.create', { key: 'md', title: 'Markdown note', kind: 'markdown', entrypoint: 'note.md', files: [{ path: 'note.md', content: '# Markdown works\n\n<script>window.artifactUnsafe=true</script>' }] }));
  await expect(page.locator('[data-artifact] .md').getByText('Markdown works', { exact: true })).toBeVisible();
  assert.equal(await page.evaluate(() => window.artifactUnsafe), undefined);
  await expect.poll(() => native('document.body.innerText')).toBe(null);
  await page.getByRole('button', { name: 'Open artifact: Nested HTML', exact: true }).click();
  await expect(page.locator(`[data-artifact="${html.artifact.id}"]`)).toBeVisible();
  await expect.poll(() => native('document.body.innerText')).toContain('Offline siblings work');
  pass('HTML relative CSS/JS, SVG, safe Markdown and selection of the correct artifact from chat all work');
  const other = (await rpc('CreateSession', { title: 'Empty artifacts room', level: 'off' })).id;
  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: 'Empty artifacts room', exact: true }).click();
  await expect.poll(() => native('document.body.innerText')).toBe(null);
  assert.equal((await rpc('Artifacts', { session_id: other })).items.length, 0);
  await close(); await launch();
  await page.getByRole('navigation', { name: 'Threads' }).getByRole('button', { name: 'Artifacts integration', exact: true }).click();
  assert.equal((await rpc('Artifacts', { session_id: sid })).items.length, 4);
  assert.equal((await rpc('ArtifactRevisions', { session_id: sid, id })).items.length, 4);
  await rpc('SetMode', { session_id: sid, mode: 'plan' });
  const refused = await call('ArtifactAction', { session_id: sid, action: 'restore', id, base_revision: 4, restore_revision: 1 });
  assert.equal(refused.ok, false);
  assert.equal((await rpc('ArtifactRead', { session_id: sid, id })).revision, 4);
  pass('room switch destroys the page; restart restores engine snapshots; plan mode refuses source mutations and permits reads');
  await page.screenshot({ path: join(output, 'artifacts-integration.png') });
};
main().catch(async error => {
  report.error = String(error.stack ?? error); console.error(error); process.exitCode = 1;
  if (page) {
    report.panel = await page.locator('[data-artifact]').innerText({ timeout: 3000 }).catch(() => 'unavailable');
    console.error('PANEL', report.panel);
    if (sid) report.artifacts = await rpc('Artifacts', { session_id: sid }).catch(() => null);
  }
}).finally(async () => {
  try { await close(); } catch (error) { console.error(error); process.exitCode = 1; }
  writeFileSync(join(output, 'artifacts-integration.json'), JSON.stringify(report, null, 2));
});
