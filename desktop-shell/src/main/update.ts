import { app } from "electron";
import log from "electron-log/main";
import { autoUpdater } from "electron-updater";
import type { UpdateEvent } from "../shared/bridge";

const FIRST_CHECK_MS = 10_000;
const CHECK_EVERY_MS = 6 * 60 * 60 * 1000;

// Updates from the GitHub releases named in the build config (publish).
// Only a packaged app updates. An update downloads in the background and
// installs when the app quits, or at once through install(). Errors go to
// the log; only a check the person asked for answers in the window.
export class Updater {
  // The version that is downloaded and waits for a restart.
  ready: string | null = null;

  constructor(private readonly send: (ev: UpdateEvent) => void) {}

  start(): void {
    if (!app.isPackaged) return;
    autoUpdater.logger = log;
    autoUpdater.autoDownload = true;
    autoUpdater.autoInstallOnAppQuit = true;
    autoUpdater.on("error", (e) => log.error("update failed:", e));
    autoUpdater.on("update-downloaded", (info) => {
      this.ready = info.version;
      this.send({ type: "ready", version: info.version });
    });
    setTimeout(() => void this.check(false), FIRST_CHECK_MS);
    setInterval(() => void this.check(false), CHECK_EVERY_MS);
  }

  async check(asked: boolean): Promise<void> {
    const say = (message: string) => {
      if (asked) this.send({ type: "status", message });
    };
    if (!app.isPackaged) {
      say("Updates are off in a development build.");
      return;
    }
    if (this.ready !== null) {
      say(`Deck ${this.ready} is ready. Restart to install it.`);
      return;
    }
    try {
      const r = await autoUpdater.checkForUpdates();
      if (r === null) say("Updates are off for this build.");
      else if (r.isUpdateAvailable) say(`Downloading Deck ${r.updateInfo.version}…`);
      else say(`Deck ${app.getVersion()} is the latest version.`);
    } catch (e) {
      log.error("update check failed:", e);
      say("Could not check for updates. The log has the details.");
    }
  }

  install(): void {
    if (this.ready !== null) autoUpdater.quitAndInstall();
  }
}
