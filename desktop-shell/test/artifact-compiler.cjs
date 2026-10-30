// Run with Electron after compiling main. DECK_ARTIFACT_APP_ROOT can point
// at release/win-unpacked/resources/app.asar/dist/app to check packaged WASM.
// No renderer is needed: exercise cold startup, termination and recovery.
const { app } = require("electron");
const assert = require("node:assert/strict");
const { mkdtempSync } = require("node:fs");
const { tmpdir } = require("node:os");
const { join } = require("node:path");

app.setPath("userData", mkdtempSync(join(tmpdir(), "deck-artifact-compiler-")));
const deadline = setTimeout(() => { console.error("compiler probe timed out"); app.exit(1); }, 140000);

app.whenReady().then(async () => {
  const root = process.env.DECK_ARTIFACT_APP_ROOT ?? join(__dirname, "..", "dist", "app");
  const { buildArtifactReact } = require(join(root, "main", "artifactRuntime.js"));
  const files = (content) => [{ path: "App.tsx", content }];
  const good = await buildArtifactReact("App.tsx", files("export default function App(){return <p>Compiler ready</p>}"));
  assert.equal(good.ok, true, JSON.stringify(good));
  console.log("PASS initial React build");

  const abort = new AbortController();
  // A unique snapshot prevents the completed-bundle cache hiding termination.
  const pending = buildArtifactReact("App.tsx", files(`export default function App(){return <p>Cancel ${Date.now()}</p>}`), abort.signal);
  abort.abort();
  const stopped = await pending;
  assert.equal(stopped.ok, false);
  assert.match(stopped.diagnostics[0].message, /cancelled/);
  console.log("PASS uncached build cancellation");

  const refused = await buildArtifactReact("App.tsx", files('import fs from "node:fs"; export default function App(){return <p>{String(fs)}</p>}'));
  assert.equal(refused.ok, false);
  // A timeout must not masquerade as enforcement of the import allowlist.
  assert.match(JSON.stringify(refused.diagnostics), /Cannot import/);
  console.log("PASS actual forbidden-import diagnostic after cancellation");

  const next = await buildArtifactReact("App.tsx", files(`export default function App(){return <p>After cancellation ${Date.now()}</p>}`));
  assert.equal(next.ok, true, JSON.stringify(next));
  console.log("PASS subsequent React build");
  clearTimeout(deadline);
  app.exit(0);
}).catch((error) => { console.error(error); app.exit(1); });
