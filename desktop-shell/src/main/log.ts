import { mkdirSync } from "node:fs";
import { dirname } from "node:path";
import { app, type BrowserWindow, shell } from "electron";
import log from "electron-log/main";
import type { LogLevel } from "../shared/bridge";

// The app's log file: main.log in the app's logs folder (~/Library/Logs/Deck
// on a Mac, logs/ in the app's data folder elsewhere). The console gets
// warnings and errors only, so a terminal run stays readable.
export function setupLog(): void {
  app.setAppLogsPath();
  log.transports.file.level = "info";
  log.transports.console.level = "warn";
  log.info(`Deck ${app.getVersion()} (Electron ${process.versions.electron}) on ${process.platform}`);
}

export function writeLog(level: LogLevel, message: string): void {
  log[level](message);
}

export function logsFolder(): string {
  return dirname(log.transports.file.getFile().path);
}

export async function openLogs(): Promise<void> {
  const dir = logsFolder();
  mkdirSync(dir, { recursive: true });
  const err = await shell.openPath(dir);
  if (err !== "") log.warn(`could not open the logs folder: ${err}`);
}

// A bug in main is logged and the app goes on, rather than quit with a
// dialog. A renderer that crashes is reloaded, once in a while at most, so
// a page that crashes on load does not spin.
export function guardProcess(window: () => BrowserWindow | null): void {
  process.on("uncaughtException", (e) => log.error("uncaught exception:", e));
  process.on("unhandledRejection", (r) => log.error("unhandled rejection:", r));
  app.on("child-process-gone", (_e, d) => {
    log.error(`${d.type} process gone (${d.reason}, exit ${d.exitCode})${d.name === undefined ? "" : `: ${d.name}`}`);
  });
  let lastReload = 0;
  app.on("render-process-gone", (_e, wc, d) => {
    log.error(`renderer gone (${d.reason}, exit ${d.exitCode})`);
    const w = window();
    if (d.reason === "clean-exit" || w === null || w.webContents !== wc || Date.now() - lastReload < 30_000) return;
    lastReload = Date.now();
    wc.reload();
  });
}
