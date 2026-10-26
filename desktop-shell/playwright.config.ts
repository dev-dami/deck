import { defineConfig } from "@playwright/test";

// The renderer runs in Chromium against test/fake-server.ts. The page gets
// the real main-process host (test/e2e/harness.ts) in place of the preload
// bridge, so only Electron's IPC is left out. electron.spec.ts runs the
// whole app in Electron.
// PORT moves the preview server, so two checkouts can run at once;
// PW_CHROMIUM points at a Chromium already on the machine.
const port = Number(process.env["PORT"] ?? 4173);
const chromium = process.env["PW_CHROMIUM"];

export default defineConfig({
  testDir: "test/e2e",
  timeout: 30_000,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: `http://127.0.0.1:${port}`,
    ...(chromium === undefined ? {} : { launchOptions: { executablePath: chromium } }),
    viewport: { width: 1360, height: 860 },
    colorScheme: "dark",
  },
  webServer: {
    // `npm run e2e` builds first; this serves the built renderer.
    command: `npx vite preview --host 127.0.0.1 --port ${port} --strictPort`,
    url: `http://127.0.0.1:${port}`,
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
