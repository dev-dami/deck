import { defineConfig } from "@playwright/test";

// The renderer runs in Chromium against test/fake-server.ts. The page gets
// the real main-process host (test/e2e/harness.ts) in place of the preload
// bridge, so only Electron's IPC is left out. electron.spec.ts runs the
// whole app in Electron.
export default defineConfig({
  testDir: "test/e2e",
  timeout: 30_000,
  workers: 1,
  reporter: [["list"]],
  use: {
    baseURL: "http://127.0.0.1:4173",
    viewport: { width: 1360, height: 860 },
    colorScheme: "dark",
  },
  webServer: {
    // `npm run e2e` builds first; this serves the built renderer.
    command: "npx vite preview --host 127.0.0.1 --port 4173 --strictPort",
    url: "http://127.0.0.1:4173",
    reuseExistingServer: false,
    timeout: 120_000,
  },
});
