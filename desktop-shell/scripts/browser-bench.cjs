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
const { electronPages } = require(join(dist, "browserView.js"));
const { browserPartition } = require(join(dist, "preview.js"));

const argv = process.argv.slice(1).filter((a) => !a.startsWith("--") && !a.endsWith(".cjs"));
const AGENTS = Number(argv[0] ?? 12);
const jsonOut = process.argv.includes("--json") ? process.argv[process.argv.indexOf("--json") + 1] : null;
const SAMPLE_MS = 3000;

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

async function run(win, name, limits, unthrottled) {
  const states = [];
  const browser = new Browser({
    factory: electronPages({
      window: () => win,
      zoom: () => 1,
      newView: (webPreferences) => new WebContentsView({ webPreferences }),
      sessionFor: (d) => session.fromPartition(browserPartition(d)),
    }),
    send: (s) => states.push(s),
    limits: { live: 8, perAgent: 6, total: 40, idleMs: 300_000, ...limits },
  });
  const port = server.address().port;
  const rows = [];
  let call = 0;
  const op = (agent, o, args = {}) =>
    browser.handle("bench", { id: String(++call), agent, name: "agent", kind: "claude", op: o, args: JSON.stringify(args), free: true, allowed: [], timeout_ms: 30000 });
  rows.push(await sample(`${name}: 0 agents`, { agents: 0, liveTabs: 0 }));
  for (let n = 1; n <= AGENTS; n++) {
    const r = await op(`agent${n}`, "open", { url: `http://a${n}.bench.test:${port}/` });
    if (r.isError) throw new Error(r.text);
    await op(`agent${n}`, "snapshot", { interactive: true });
    if (unthrottled) {
      for (const t of browser.tabs.list()) {
        t.page?.apply({ visible: true, throttling: false, muted: true, frameRate: 60 });
      }
    }
    await sleep(3200); // past the rest time: the page goes back to one hidden pixel
    const st = browser.state();
    rows.push(await sample(`${name}: ${n} agents`, { agents: n, liveTabs: st.tabs.filter((t) => t.live).length, tabs: st.tabs.length, kind: KIND[n % PAGES.length] }));
  }
  return { browser, rows };
}

app.whenReady().then(async () => {
  const win = new BrowserWindow({ width: 1360, height: 860, show: true });
  await win.loadURL("about:blank");
  await sleep(1500);
  const out = {};

  const a = await run(win, "deck", {}, false);
  out.deck = a.rows;
  // Idle tabs are let go: the same browser with a short idle time.
  a.browser.tabs.limits.idleMs = 2000;
  await sleep(2500);
  const gone = a.browser.sweep();
  await sleep(1500);
  out.afterIdle = await sample("deck: after the idle sweep", { agents: AGENTS, liveTabs: a.browser.state().tabs.filter((t) => t.live).length, discarded: gone.length });
  // One agent returns to its discarded tab: restored by its address.
  const back = await a.browser.handle("bench", { id: "x", agent: "agent1", name: "agent", kind: "claude", op: "snapshot", args: "{}", free: true, allowed: [], timeout_ms: 30000 });
  out.restored = { ok: !back.isError, firstLine: back.text.split("\n")[0] };
  a.browser.closeDeck("bench");
  a.browser.stop();
  await sleep(1500);

  // The same agents with no cap on live pages: what the cap saves.
  const c = await run(win, "throttled, no cap", { live: 100 }, false);
  out.uncapped = c.rows;
  c.browser.closeDeck("bench");
  c.browser.stop();
  await sleep(1500);

  // And with every page left running at full speed, as a browser that does
  // not slow what nobody sees would: what the throttling saves.
  const b = await run(win, "unthrottled, no cap", { live: 100 }, true);
  out.unthrottled = b.rows;
  b.browser.closeDeck("bench");
  b.browser.stop();

  const fmt = (r) => `${r.label.padEnd(30)} procs ${String(r.processes).padStart(2)} (tab ${String(r.tabProcesses).padStart(2)})  live ${String(r.liveTabs ?? "").padStart(2)}  mem ${String(r.memMB).padStart(5)} MB  rss ${String(r.rssMB).padStart(5)} MB  cpu ${String(r.cpuPct).padStart(5)} %`;
  for (const k of ["deck", "afterIdle", "uncapped", "unthrottled"]) for (const r of [].concat(out[k])) console.log(fmt(r));
  console.log("restored:", JSON.stringify(out.restored));
  if (jsonOut) writeFileSync(jsonOut, JSON.stringify(out, null, 2));
  server.close();
  app.exit(0);
});
