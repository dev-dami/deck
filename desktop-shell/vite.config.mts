import { fileURLToPath } from "node:url";
import react from "@vitejs/plugin-react";
import { defineConfig } from "vitest/config";
import { RENDERER_CSP } from "./src/shared/csp";

// The phone client's sources: protocol guards, RPC client, reducer and
// markdown are shared, not copied (see README, "Shared code").
const web = fileURLToPath(new URL("../pkg/remote/web/src", import.meta.url));
const repo = fileURLToPath(new URL("..", import.meta.url));

export default defineConfig({
  base: "./",
  plugins: [
    react(),
    {
      // The dev server injects inline styles for hot reload, so only the
      // build carries the strict policy.
      name: "deck-csp",
      apply: "build",
      transformIndexHtml: (html) =>
        html.replace(
          '<meta charset="utf-8" />',
          `<meta charset="utf-8" />\n    <meta http-equiv="Content-Security-Policy" content="${RENDERER_CSP}" />`,
        ),
    },
  ],
  resolve: { alias: { "@web": web } },
  build: { outDir: "dist/renderer", emptyOutDir: true, target: "chrome130" },
  server: { port: 5173, strictPort: true, fs: { allow: [repo] } },
  preview: { port: 4173, strictPort: true },
  test: {
    include: ["src/**/*.test.ts", "test/**/*.test.ts"],
    environment: "node",
  },
});
