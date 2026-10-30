// Run after `npx tsc -p tsconfig.main.json` with the installed Electron:
//   node_modules/electron/dist/electron test/artifact-electron.cjs
// Shows hostile HTML, SVG and React artifacts in the real artifact view and
// checks that none reaches the network, the app, or another page. Uses a
// loopback fixture that counts every request, and an offscreen window.
const { app, BrowserWindow, WebContentsView, protocol, session } = require("electron");
const { createServer } = require("node:http");
const { mkdtempSync } = require("node:fs");
const { createServer: listen } = require("node:net");
const dgram = require("node:dgram");
const { tmpdir } = require("node:os");
const { join } = require("node:path");
const assert = require("node:assert/strict");

// Also verifies packaged dependency/wasm resolution when pointed at
// release/win-unpacked/resources/app.asar/dist/app.
const compiled = process.env.DECK_ARTIFACT_APP_ROOT ?? join(__dirname, "..", "dist", "app");
const { ArtifactPane, ARTIFACT_PARTITION, lockArtifactSession } = require(join(compiled, "main", "artifact.js"));
const { artifactHost } = require(join(compiled, "main", "artifactHost.js"));
const { buildArtifactReact } = require(join(compiled, "main", "artifactRuntime.js"));
const { ARTIFACT_SCHEME } = require(join(compiled, "main", "artifactProtocol.js"));

app.setPath("userData", mkdtempSync(join(tmpdir(), "deck-artifact-smoke-")));
app.disableHardwareAcceleration();
app.commandLine.appendSwitch("no-proxy-server");
// A hidden Windows test window must still schedule/render like a visible
// panel. These affect occlusion/background scheduling, not sandbox policy.
app.commandLine.appendSwitch("disable-backgrounding-occluded-windows");
app.commandLine.appendSwitch("disable-renderer-backgrounding");
app.commandLine.appendSwitch("disable-background-timer-throttling");
app.commandLine.appendSwitch("disable-features", "CalculateNativeWinOcclusion");
protocol.registerSchemesAsPrivileged([{ scheme: ARTIFACT_SCHEME, privileges: { standard: true, secure: true, supportFetchAPI: true } }]);
const timeout = setTimeout(() => {
  console.error("artifact smoke timed out");
  app.exit(1);
}, 90000);

const wait = (ms) => new Promise((done) => setTimeout(done, ms));

async function until(read, what, ms = 30000) {
  const end = Date.now() + ms;
  for (;;) {
    const v = await read();
    if (v) return v;
    if (Date.now() > end) throw new Error(`timed out waiting for ${what}`);
    await wait(100);
  }
}

// The probes run inside the artifact. Each answers true when the escape
// was refused; the page logs the answers for main to read.
const HOSTILE = (port, other) => `<!doctype html><meta charset="utf-8"><body><script>
const out = {};
const deny = (name, p) => p.then(() => (out[name] = false), () => (out[name] = true));
const loop = "http://127.0.0.1:${port}";
const violations = [];
document.addEventListener("securitypolicyviolation", (e) => violations.push(e.violatedDirective));
(async () => {
  out.noRequire = typeof require === "undefined" && typeof process === "undefined" && typeof module === "undefined";
  out.noBridge = typeof window.deck === "undefined" && typeof window.electron === "undefined" && typeof window.electronAPI === "undefined";
  await deny("fetchExternal", fetch("https://example.com/"));
  await deny("fetchLoopback", fetch(loop + "/fetch"));
  await deny("fetchSelf", fetch("/index.html"));
  await deny("fetchOtherArtifact", fetch("${other}"));
  await deny("xhr", new Promise((ok, no) => { const x = new XMLHttpRequest(); x.open("GET", loop + "/xhr"); x.onload = ok; x.onerror = no; x.send(); }));
  await deny("websocket", new Promise((ok, no) => { const w = new WebSocket("ws://127.0.0.1:${port}/ws"); w.onopen = ok; w.onerror = no; }));
  out.openRefused = window.open(loop + "/open") === null;
  const form = document.createElement("form"); form.method = "post"; form.action = loop + "/form";
  document.body.append(form); try { form.submit(); } catch {}
  const frame = document.createElement("iframe"); frame.src = loop + "/frame"; document.body.append(frame);
  const img = new Image(); img.src = loop + "/img"; document.body.append(img);
  const s = document.createElement("script"); s.src = loop + "/script.js"; document.body.append(s);
  const m = document.createElement("meta"); m.httpEquiv = "refresh"; m.content = "0;url=" + loop + "/refresh"; document.head.append(m);
  setTimeout(() => { try { location.href = loop + "/navigate"; } catch {} }, 0);
  setTimeout(() => {
    out.stayed = location.protocol === "${ARTIFACT_SCHEME}:";
    out.violations = [...new Set(violations)].sort();
    console.log("RESULT " + JSON.stringify(out));
  }, 1500);
})();
</script>`;

app
  .whenReady()
  .then(async () => {
    let seq = 0;
    const hits = [];
    const fixture = createServer((req, res) => {
      hits.push(req.url);
      res.end("hit");
    });
    await new Promise((done) => fixture.listen(0, "127.0.0.1", done));
    const port = fixture.address().port;
    // A TCP listener too, for the WebSocket probe: any connection counts.
    const sockets = [];
    const raw = listen((s) => {
      sockets.push(1);
      s.destroy();
    });
    await new Promise((done) => raw.listen(0, "127.0.0.1", done));
    void raw;

    const win = new BrowserWindow({ show: false, width: 900, height: 700, webPreferences: { sandbox: true, backgroundThrottling: false } });
    await win.loadURL("about:blank");
    const ses = session.fromPartition(ARTIFACT_PARTITION);
    await lockArtifactSession(ses, ARTIFACT_SCHEME);
    const logs = [];
    const events = [];
    app.on("web-contents-created", (_e, wc) => {
      wc.on("console-message", (e) => { logs.push(e.message); if (process.env.DEBUG_LOGS) console.log("LOG", e.message); });
      if (process.env.DEBUG_LOGS) {
        wc.on("did-start-loading", () => console.log("START", wc.id, wc.getURL()));
        wc.on("did-finish-load", () => console.log("LOADED", wc.id, wc.getURL()));
        wc.on("did-fail-load", (_e, code, message, url) => console.log("LOAD_ERROR", wc.id, code, message, url));
        wc.on("render-process-gone", (_e, detail) => console.log("RENDER_GONE", wc.id, detail));
      }
    });
    const pane = new ArtifactPane({
      create: (webPreferences) => {
        // The fixture stays hidden; model a visible preview's scheduler so
        // Chromium cannot freeze its timers. All security preferences come
        // unchanged from the production pane.
        const view = new WebContentsView({ webPreferences: { ...webPreferences, backgroundThrottling: false } });
        win.contentView.addChildView(view);
        return view;
      },
      remove: (view) => win.contentView.removeChildView(view),
      zoom: () => 1,
      send: (e) => events.push(e),
      session: () => ses,
    });
    // Model the real panel's placement. A zero-size, invisible child view
    // can be frozen by Chromium and is not a usable rendering fixture.
    pane.bounds({ x: 0, y: 0, w: 900, h: 700, zoom: 1 });
    ses.protocol.handle(ARTIFACT_SCHEME, (req) => {
      if (process.env.DEBUG_LOGS) console.log("PROTOCOL", req.url);
      return pane.hosts.handle(req.url);
    });
    const host = artifactHost(pane, buildArtifactReact);

    // A second artifact whose address the first page tries to reach.
    const secret = pane.hosts.add({ kind: "html", entrypoint: "i.html", files: new Map([["i.html", "SECRET"]]) });
    const other = `${ARTIFACT_SCHEME}://${secret}/`;

    const shown = await host.show({ request: "r" + ++seq, kind: "html", entrypoint: "index.html", files: [{ path: "index.html", content: HOSTILE(port, other) }] });
    assert.deepEqual(shown, { ok: true });
    const line = await until(() => logs.find((l) => l.startsWith("RESULT ")), "the hostile page's report");
    const r = JSON.parse(line.slice("RESULT ".length));
    for (const key of ["noRequire", "noBridge", "fetchExternal", "fetchLoopback", "fetchSelf", "fetchOtherArtifact", "xhr", "websocket", "openRefused", "stayed"]) {
      assert.equal(r[key], true, `${key} must be refused: ${line}`);
    }
    for (const d of ["form-action", "frame-src", "img-src", "script-src-elem"]) {
      assert.ok(r.violations.includes(d), `CSP ${d} reported: ${line}`);
    }
    await wait(500);
    assert.deepEqual(hits, [], "the loopback server saw a request");
    assert.deepEqual(sockets, [], "the loopback socket saw a connection");
    console.log("PASS hostile HTML reached nothing");

    // Active SVG runs as a document of its own, under the same rules.
    logs.length = 0;
    const svg = `<svg xmlns="http://www.w3.org/2000/svg"><script>fetch("http://127.0.0.1:${port}/svg").catch(()=>{});console.log("RESULT svg:"+(typeof require));</script></svg>`;
    assert.deepEqual(await host.show({ request: "r" + ++seq, kind: "svg", entrypoint: "a.svg", files: [{ path: "a.svg", content: svg }] }), { ok: true });
    await until(() => logs.find((l) => l === "RESULT svg:undefined"), "the SVG's report");
    await wait(300);
    assert.deepEqual(hits, [], "the SVG reached the loopback server");
    console.log("PASS hostile SVG reached nothing");

    // React: a good app renders offline from the packaged React.
    const good = [
      { path: "App.tsx", content: `import { useState } from "react";\nimport "./app.css";\nexport default function App() { const [n] = useState(41); return <p>REACT_OK {n + 1}</p>; }` },
      { path: "app.css", content: "p { color: green }" },
    ];
    assert.deepEqual(await host.show({ request: "r" + ++seq, kind: "react", entrypoint: "App.tsx", files: good }), { ok: true });
    const view = BrowserWindow.getAllWindows()[0].contentView.children[0];
    const text = await until(async () => {
      const t = await view.webContents.executeJavaScript("document.getElementById('root') && document.getElementById('root').textContent");
      return t || null;
    }, "the React app to render");
    assert.equal(text, "REACT_OK 42");
    console.log("PASS React renders offline");

    const cancelled = new AbortController();
    const uncached = good.map((f) => f.path === "App.tsx" ? { ...f, content: f.content + "\n// uncached cancellation probe" } : f);
    const pending = buildArtifactReact("App.tsx", uncached, cancelled.signal);
    cancelled.abort();
    const cancelledResult = await pending;
    assert.equal(cancelledResult.ok, false);
    assert.match(cancelledResult.diagnostics[0].message, /cancelled/);
    console.log("PASS closing a build terminates its worker");

    // React that imports the host or a stray package fails to build, and nothing runs.
    for (const spec of ["fs", "node:child_process", "lodash", "https://example.com/x.js"]) {
      const bad = [{ path: "App.tsx", content: `import x from ${JSON.stringify(spec)};\nexport default function App() { return <i>{String(x)}</i>; }` }];
      const res = await host.show({ request: "r" + ++seq, kind: "react", entrypoint: "App.tsx", files: bad });
      assert.equal(res.ok, false, `import of ${spec} must fail the build`);
      assert.ok(res.diagnostics.length > 0);
      assert.match(res.diagnostics[0].message, /Cannot import/, "a compiler timeout is not an import-refusal result");
    }
    console.log("PASS React imports outside the list fail to build");

    // The page's address is unreachable from another host name.
    const html = await ses.fetch(`${ARTIFACT_SCHEME}://${"0".repeat(32)}/`);
    assert.equal(html.status, 404);
    console.log("PASS an unknown artifact host is a 404");

    // A nested HTML entry resolves its own relative files.
    logs.length = 0;
    const nested = [
      { path: "src/index.html", content: '<link rel="stylesheet" href="./style.css"><body><script src="./app.js"></script>' },
      { path: "src/style.css", content: "body { color: rgb(1, 2, 3) }" },
      { path: "src/app.js", content: 'addEventListener("load", () => console.log("RESULT nested:" + getComputedStyle(document.body).color));' },
    ];
    assert.deepEqual(await host.show({ request: "r" + ++seq, kind: "html", entrypoint: "src/index.html", files: nested }), { ok: true });
    if (process.env.DEBUG_LOGS) {
      const page = win.contentView.children[0].webContents;
      page.on("did-fail-load", (_e, code, desc, url) => console.log("NESTED_FAIL", code, desc, url));
      page.on("did-finish-load", () => console.log("NESTED_LOADED", page.getURL()));
      setTimeout(() => { console.log("NESTED_URL", page.getURL(), page.isLoading()); void page.executeJavaScript("JSON.stringify({state:document.readyState,html:document.documentElement.outerHTML})").then(s => console.log("NESTED_DOM", s)).catch(console.error); }, 3000);
    }
    await until(() => logs.find((l) => l === "RESULT nested:rgb(1, 2, 3)"), "the nested page's stylesheet and script");
    console.log("PASS a nested HTML entry loads its relative files");

    // WebRTC: ICE toward a UDP server on this computer must send nothing.
    const udp = [];
    const sock = dgram.createSocket("udp4");
    sock.on("message", () => udp.push(1));
    await new Promise((done) => sock.bind(0, "127.0.0.1", done));
    // A TCP listener for TURN over TCP: any connection is a leak.
    const turn = [];
    const tcp = listen((c) => {
      turn.push(1);
      c.destroy();
    });
    await new Promise((done) => tcp.listen(0, "127.0.0.1", done));
    logs.length = 0;
    const rtc = `<body><script>
      const servers = [
        { urls: "stun:127.0.0.1:${sock.address().port}" },
        { urls: "turn:127.0.0.1:${tcp.address().port}?transport=tcp", username: "u", credential: "c" },
        { urls: "turn:127.0.0.1:${sock.address().port}?transport=udp", username: "u", credential: "c" },
      ];
      const probe = (Rtc) => {
        const pc = new Rtc({ iceServers: servers });
        pc.createDataChannel("x");
        pc.createOffer().then((o) => pc.setLocalDescription(o));
      };
      probe(RTCPeerConnection);
      // A fresh frame has its own realm and its own native constructor.
      const frame = document.createElement("iframe");
      document.body.append(frame);
      probe(frame.contentWindow.RTCPeerConnection);
      setTimeout(() => console.log("RESULT rtc:done"), 3500);
    </script>`;
    assert.deepEqual(await host.show({ request: "r" + ++seq, kind: "html", entrypoint: "r.html", files: [{ path: "r.html", content: rtc }] }), { ok: true });
    await until(() => logs.find((l) => l === "RESULT rtc:done"), "the WebRTC probe", 20000);
    await wait(500);
    assert.deepEqual(udp, [], "WebRTC sent UDP (STUN/TURN) to the local server");
    assert.deepEqual(turn, [], "WebRTC opened a TCP connection (TURN) to the local server");
    sock.close();
    tcp.close();
    console.log("PASS WebRTC (STUN, TURN over UDP and TCP, own-frame constructor) sent nothing");

    // An earlier page's late error never counts for the page that replaced it.
    events.length = 0;
    const late = '<script>setTimeout(() => console.error("FROM-OLD-PAGE"), 800)</script>';
    const first = "r" + ++seq;
    await host.show({ request: first, kind: "html", entrypoint: "a.html", files: [{ path: "a.html", content: late }] });
    const second = "r" + ++seq;
    await host.show({ request: second, kind: "html", entrypoint: "b.html", files: [{ path: "b.html", content: "<p>b</p>" }] });
    await wait(1800);
    assert.ok(!events.some((e) => e.request === second && e.message.includes("FROM-OLD-PAGE")), "an old page's error was attributed to the new show");
    assert.ok(!events.some((e) => e.request === first && e.kind === "error"), "the replaced page still reported");
    console.log("PASS a replaced page's late events are dropped");

    pane.close();
    fixture.close();
    raw.close();
    clearTimeout(timeout);
    app.exit(0);
  })
  .catch((e) => {
    console.error(e);
    app.exit(1);
  });
