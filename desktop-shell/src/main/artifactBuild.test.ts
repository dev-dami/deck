import { createRequire } from "node:module";
import * as esbuild from "esbuild-wasm";
import { afterAll, beforeAll, describe, expect, test } from "vitest";
import { type BuildDeps, buildReact } from "./artifactBuild";

const req = createRequire(__filename);
const deps: BuildDeps = { esbuild, vendor: (spec) => req.resolve(spec) };

beforeAll(async () => {
  await esbuild.initialize({});
}, 30_000);
afterAll(async () => {
  await esbuild.stop();
});

const app = `import { useState } from "react";
import Counter from "./Counter";
import "./app.css";
export default function App() {
  const [n] = useState(1);
  return <main><Counter start={n} /></main>;
}`;
const counter = `export default function Counter({ start }: { start: number }) { return <b>{start}</b>; }`;

describe("buildReact", { timeout: 40_000 }, () => {
  test("bundles local TSX and CSS with the packaged React", async () => {
    const r = await buildReact(
      "App.tsx",
      [
        { path: "App.tsx", content: app },
        { path: "Counter.tsx", content: counter },
        { path: "app.css", content: "main { color: red; }" },
      ],
      deps,
    );
    expect(r.ok).toBe(true);
    if (!r.ok) return;
    expect(r.js).toContain("createRoot");
    expect(r.css).toContain("color: red");
  }, 30_000);

  test("reports a syntax error with its file and line", async () => {
    const r = await buildReact("App.tsx", [{ path: "App.tsx", content: "export default function App() {\n  return <div>;\n}" }], deps);
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.diagnostics[0]).toMatchObject({ file: "App.tsx" });
    expect(r.diagnostics[0]?.line).toBeGreaterThan(0);
    expect(r.diagnostics[0]?.column).toBeGreaterThan(0);
  });

  test("names only the artifact's own files; the rest is <build> or <generated>", async () => {
    const src = 'import x from "lodash";\nexport default () => x;';
    const r = await buildReact("App.tsx", [{ path: "App.tsx", content: src }], deps);
    expect(r.ok).toBe(false);
    if (r.ok) return;
    for (const d of r.diagnostics) expect(["App.tsx", "<build>", "<generated>", ""]).toContain(d.file);
  });

  test("keeps a report within 8 diagnostics of 512 bytes and 4 KiB in all", async () => {
    const imports = Array.from({ length: 30 }, (_, i) => `import a${i} from "é${"x".repeat(300)}${i}";`).join("\n");
    // A TypeScript file drops imports nothing uses, so each one is used.
    const uses = Array.from({ length: 30 }, (_, i) => `a${i}`).join(", ");
    const src = `${imports}\nexport default function App() { return <i>{String([${uses}])}</i>; }`;
    const r = await buildReact("App.tsx", [{ path: "App.tsx", content: src }], deps);
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.diagnostics.length).toBeLessThanOrEqual(8);
    for (const d of r.diagnostics) expect(new TextEncoder().encode(d.message).length).toBeLessThanOrEqual(512);
    expect(new TextEncoder().encode(JSON.stringify(r.diagnostics)).length).toBeLessThanOrEqual(4096);
  });

  test.each([
    ["lodash", "a package outside the list"],
    ["node:fs", "a Node built-in"],
    ["fs", "a bare Node module name"],
    ["https://example.com/x.js", "a URL"],
    ["/etc/passwd", "an absolute path"],
    ["../../secret", "a path out of the artifact"],
  ])("refuses to import %s (%s)", async (spec) => {
    const src = `import x from ${JSON.stringify(spec)};\nexport default function App() { return <i>{String(x)}</i>; }`;
    const r = await buildReact("App.tsx", [{ path: "App.tsx", content: src }], deps);
    expect(r.ok).toBe(false);
    if (r.ok) return;
    expect(r.diagnostics.length).toBeGreaterThan(0);
    expect(r.diagnostics[0]?.message).toMatch(/Cannot import|Cannot find/);
  });

  test("refuses a missing entry point", async () => {
    const r = await buildReact("Nope.tsx", [{ path: "App.tsx", content: app }], deps);
    expect(r).toMatchObject({ ok: false });
  });

  test("stops a build that outlasts the time limit", async () => {
    const r = await buildReact("App.tsx", [{ path: "App.tsx", content: counter }], deps, 0);
    expect(r).toMatchObject({ ok: false });
    if (r.ok) return;
    expect(r.diagnostics[0]?.message).toContain("took longer");
  });

  test("a build that was stopped leaves the compiler ready for the next", async () => {
    await buildReact("App.tsx", [{ path: "App.tsx", content: counter }], deps, 0);
    const r = await buildReact("App.tsx", [{ path: "App.tsx", content: counter }], deps);
    expect(r.ok).toBe(true);
  }, 30_000);
});
