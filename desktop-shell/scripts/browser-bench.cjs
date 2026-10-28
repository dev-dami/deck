// Measures what the shared browser costs as agents are added: memory and CPU
// of the whole Electron process tree (app.getAppMetrics) with 1..N agents,
// each working in one tab of a different site, on a mix of pages.
//
//   npm run build && xvfb-run -a electron --no-sandbox scripts/browser-bench.cjs [agents] [--json out.json]
//
// It runs the app's own Browser class with its real WebContentsView pages
// (dist/app/main), so the numbers are those of the app. Memory is the sum
// of PSS (proportional set size, /proc/<pid>/smaps_rollup) over the process
// tree, which counts pages shared between processes once; on a system
// without it, the resident set is used. CPU is the mean of percentCPUUsage
// over the sample window, in percent of one core.
const { app, BrowserWindow, WebContentsView, session } = require("electron");
const { createServer } = require("node:http");
const { readFileSync, writeFileSync } = require("node:fs");
const { join } = require("node:path");

const dist = join(__dirname, "..", "dist", "app", "main");
const { Browser } = require(join(dist, "browser.js"));

const argv = process.argv.slice(1).filter((a) => !a.startsWith("--") && !a.endsWith(".cjs"));
const AGENTS = Number(argv[0] ?? 12);
const jsonOut = process.argv.includes("--json") ? process.argv[process.argv.indexOf("--json") + 1] : null;
// --deck-only: just the browser as the app runs it, not the two comparisons.
const DECK_ONLY = process.argv.includes("--deck-only");
const SAMPLE_MS = 3000;
// How long a page stays visible after its agent's last op; the bench waits past it.
const REST_MS = 3000;

app.commandLine.appendSwitch("no-sandbox");
app.commandLine.appendSwitch("host-resolver-rules", "MAP *.bench.test 127.0.0.1");

// Six kinds of page, by the site's number: an article, a canvas animation,
// a page that updates the DOM from timers, a large table, a form app and an
// SVG animation. Heavy pages stay busy; the browser decides what to slow.
const PAGES = [
  (n) => `<h1>Article ${n}</h1>${"<p>Lorem ipsum dolor sit amet, consectetur adipiscing elit. ".repeat(3)}</p>`.repeat(60),
  () => `<canvas id=c width=640 height=360></canvas><script>const x=document.getElementById('c').getContext('2d');let t=0;(function f(){t++;x.fillStyle='#123';x.fillRect(0,0,640,360);for(let i=0;i<200;i++){x.fillStyle='hsl('+((i*7+t)%360)+',80%,60%)';x.fillRect((i*13+t*3)%640,(i*29+t*2)%360,18,18)}requestAnimationFrame(f)})()</script>`,
  () => `<div id=o></div><script>let n=0;setInterval(()=>{n++;const d=document.createElement('div');d.textContent='tick '+n;o.prepend(d);if(o.children.length>200)o.lastChild.remove()},10)</script>`,
  () => `<table>${Array.from({ length: 4000 }, (_, i) => `<tr><td>${i}</td><td>row ${i}</td><td><button>act ${i}</button></td></tr>`).join("")}</table>`,
  () => `<form><label>Name <input></label><label>Mail <input type=email></label><select><option>a<option>b</select><button>Save</button></form><ul>${"<li><a href='#'>item</a></li>".repeat(300)}</ul>`,
  () => `<svg width=600 height=300><circle id=k cx=50 cy=50 r=30 fill=orange><animate attributeName=cx from=50 to=550 dur=2s repeatCount=indefinite /></circle></svg><script>setInterval(()=>{document.title=Date.now()},500)</script>`,
];
const KIND = ["article", "canvas animation", "timer-driven DOM", "4000-row table", "form app", "SVG animation"];

const server = createServer((req, res) => {
  const n = Number((/^a(\d+)\./.exec(req.headers.host ?? "") ?? [])[1] ?? 0);
  res.setHeader("content-type", "text/html");
  res.end(`<!doctype html><title>Site ${n}</title>${PAGES[n % PAGES.length](n)}`);
}).listen(0, "127.0.0.1");

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function pss(pid) {
  try {
    const m = /Pss:\s+(\d+) kB/.exec(readFileSync(`/proc/${pid}/smaps_rollup`, "utf8"));
    return m ? Number(m[1]) / 1024 : null;
  } catch {
    return null;
  }
}

const median = (xs) => [...xs].sort((a, b) => a - b)[Math.floor(xs.length / 2)];

// The tree's memory now: PSS summed over its processes, and the resident set.
function memory() {
  let mem = 0;
  let rss = 0;
  for (const m of app.getAppMetrics()) {
    mem += pss(m.pid) ?? m.memory.workingSetSize / 1024;
    rss += m.memory.workingSetSize / 1024;
  }
  return { mem, rss };
}

// One sample: the tree's CPU averaged over the window, and its memory as the
// median of three readings across it (a page's garbage collector moves it).
async function sample(label, extra = {}) {
  app.getAppMetrics(); // resets the CPU counters
  const cpu = new Map();
  const mems = [];
  const ticks = 6;
  for (let i = 0; i < ticks; i++) {
    await sleep(SAMPLE_MS / ticks);
    for (const m of app.getAppMetrics()) cpu.set(m.pid, (cpu.get(m.pid) ?? 0) + m.cpu.percentCPUUsage);
    if (i % 2 === 1) mems.push(memory());
  }
  const ms = app.getAppMetrics();
  const byType = {};
  for (const m of ms) byType[m.type] = (byType[m.type] ?? 0) + 1;
  const total = [...cpu.values()].reduce((a, b) => a + b, 0) / ticks;
  return {
    label,
    processes: ms.length,
    tabProcesses: byType.Tab ?? 0,
    memMB: Math.round(median(mems.map((m) => m.mem))),
    rssMB: Math.round(median(mems.map((m) => m.rss))),
    cpuPct: Math.round(total * 10) / 10,
    ...extra,
  };
}

// One run: AGENTS agents, each opening a tab of its own site and reading it.
// rest is how long a page stays visible after its agent: past it the page is
// hidden again, and Chromium may slow it.
async function run(win, name, limits, restMs) {
  const profile = session.fromPartition(`bench-${name}`);
  const browser = new Browser({
    create: (prefs) => {
      const v = new WebContentsView({ webPreferences: prefs });
      win.contentView.addChildView(v);
      return v;
    },
    remove: (v) => win.contentView.removeChildView(v),
    session: () => profile,
    zoom: () => 1,
    changed: () => {},
    approve: async () => true,
    limits: { live: 8, perAgent: 6, idleMs: 300_000, ...limits },
    restMs,
  });
  const port = server.address().port;
  const rows = [];
  let call = 0;
  const op = (agent, command) =>
    browser.execute("bench", { id: `b${++call}`, session_id: "room", controller_id: "c", controller_generation: "g", owner: agent, command, deadline_at: Date.now() + 30_000 });
  const live = () => browser.state("bench", "room").tabs.filter((t) => !t.asleep).length;
  rows.push(await sample(`${name}: 0 agents`, { agents: 0, liveTabs: 0 }));
  const tabs = {};
  for (let n = 1; n <= AGENTS; n++) {
    const opened = await op(`agent${n}`, { action: "open", url: `http://a${n}.bench.test:${port}/` });
    if (opened.error_code) throw new Error(opened.result.text);
    tabs[n] = opened.tab.id;
    const snap = await op(`agent${n}`, { action: "snapshot", tab_id: opened.tab.id, interactive: true });
    if (snap.error_code) throw new Error(snap.result.text);
    await sleep(Math.min(restMs, REST_MS) + 200);
    rows.push(await sample(`${name}: ${n} agents`, { agents: n, liveTabs: live(), tabs: n, kind: KIND[n % PAGES.length] }));
  }
  return { browser, rows, op, tabs, live };
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1360, height: 860, show: true });
  await win.loadURL("about:blank");
  await sleep(1500);
  const out = {};

  // The browser as the app runs it: eight live pages, hidden ones slowed.
  const a = await run(win, "deck", {}, REST_MS);
  out.deck = a.rows;
  // Idle tabs are let go: the same browser with a short idle time.
  a.browser.limits.idleMs = 1000;
  await sleep(1500);
  const gone = a.browser.sweep();
  await sleep(1500);
  out.afterIdle = await sample("deck: after the idle sweep", { agents: AGENTS, liveTabs: a.live(), discarded: gone.length });
  // One agent returns to its discarded tab: restored by its address.
  const back = await a.op("agent1", { action: "snapshot", tab_id: a.tabs[1] });
  out.restored = { ok: !back.error_code, firstLine: (back.result.text.split("\n")[3] ?? "").slice(0, 80) };
  a.browser.close();
  await sleep(1500);

  if (!DECK_ONLY) {
    // The same agents with no cap on live pages: what the cap saves.
    const c = await run(win, "throttled, no cap", { live: 100 }, REST_MS);
    out.uncapped = c.rows;
    c.browser.close();
    await sleep(1500);

    // And with every page left visible and running at full speed, as a
    // browser that does not slow what nobody sees would: what the
    // throttling saves.
    const b = await run(win, "unthrottled, no cap", { live: 100 }, 1e9);
    out.unthrottled = b.rows;
    b.browser.close();
  }

  const fmt = (r) => `${r.label.padEnd(30)} procs ${String(r.processes).padStart(2)} (tab ${String(r.tabProcesses).padStart(2)})  live ${String(r.liveTabs ?? "").padStart(2)}  mem ${String(r.memMB).padStart(5)} MB  rss ${String(r.rssMB).padStart(5)} MB  cpu ${String(r.cpuPct).padStart(5)} %`;
  for (const k of ["deck", "afterIdle", "uncapped", "unthrottled"]) for (const r of [].concat(out[k] ?? [])) console.log(fmt(r));
  console.log("restored:", JSON.stringify(out.restored));
  if (jsonOut) writeFileSync(jsonOut, JSON.stringify(out, null, 2));
  server.close();
  app.exit(0);
});
