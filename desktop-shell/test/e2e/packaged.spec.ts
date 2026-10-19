import { execFileSync } from "node:child_process";
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { basename, join } from "node:path";
import { _electron as electron, expect, test } from "@playwright/test";

// The packaged app (npm run dist, or electron-builder --dir) runs the
// engine it ships in resources/bin, with no deck on PATH, and finds an
// agent CLI in ~/.local/bin though the app was started with a bare PATH,
// as from the Dock or a desktop launcher.
const root = join(__dirname, "..", "..");
const exe = join(root, "release", "linux-unpacked", "deck-desktop");
const display = process.env["DISPLAY"] !== undefined;

test("the packaged app opens a folder with its own engine", async () => {
  test.skip(process.platform !== "linux" || !existsSync(exe) || !display, "needs electron-builder --linux dir and a display");
  const folder = mkdtempSync(join(tmpdir(), "deck-packaged-"));
  execFileSync("git", ["init", "-q", "-b", "main"], { cwd: folder });
  const userData = mkdtempSync(join(tmpdir(), "deck-desktop-"));
  writeFileSync(join(userData, "recent.json"), JSON.stringify([folder]));
  const args = [`--user-data-dir=${userData}`];
  if (process.getuid?.() === 0) args.push("--no-sandbox");
  const env = Object.fromEntries(Object.entries(process.env).filter((e): e is [string, string] => e[1] !== undefined));
  delete env["DECK_BIN"];
  const home = mkdtempSync(join(tmpdir(), "deck-home-"));
  mkdirSync(join(home, ".local", "bin"), { recursive: true });
  writeFileSync(join(home, ".local", "bin", "claude"), "#!/bin/sh\nexit 0\n", { mode: 0o755 });
  const app = await electron.launch({
    executablePath: exe,
    args,
    env: { ...env, PATH: "/usr/bin:/bin", HOME: home, SHELL: "/bin/sh" },
  });
  try {
    const win = await app.firstWindow();
    await expect(win.locator(".link")).toHaveText("Connected", { timeout: 20_000 });
    await expect(win.locator(".project")).toHaveText(basename(folder));
  } finally {
    await app.close();
  }
});
