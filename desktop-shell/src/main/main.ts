import { execFile } from "node:child_process";
import { statSync } from "node:fs";
import { readFile } from "node:fs/promises";
import { hostname } from "node:os";
import { join, resolve, sep } from "node:path";
import {
  app,
  BrowserWindow,
  clipboard,
  dialog,
  ipcMain,
  Menu,
  type NativeImage,
  nativeImage,
  nativeTheme,
  protocol,
  safeStorage,
  screen,
  session,
  shell,
  WebContentsView,
} from "electron";
import {
  type AppIconImage,
  type AppIconResult,
  HOST_METHODS,
  hostChannel,
  LINK_EVENT_CHANNEL,
  type LinkEvent,
  MENU_EVENT_CHANNEL,
  NOTIFICATION_EVENT_CHANNEL,
  PREVIEW_EVENT_CHANNEL,
  RPC_EVENT_CHANNEL,
  SOCKET_EVENT_CHANNEL,
  type ThreadChoice,
  type ThreadMenuInput,
  UPDATE_EVENT_CHANNEL,
} from "../shared/bridge";
import { RENDERER_CSP } from "../shared/csp";
import { AppIconFile, representations } from "./appIcon";
import { applyLauncherIcon, type LauncherDeps, linuxLaunchers, windowsShortcuts } from "./launcherIcon";
import { contextMenuTemplate, threadMenuTemplate } from "./contextMenu";
import { findAgentClis, findDeck, loadService, RpcDecks } from "./decks";
import { enginePath } from "./shellPath";
import { callHost, createHost, fromApp } from "./host";
import { type Arrival, linkFromUrl, linksFromArgs, mustConfirm } from "./launch";
import { LinkHost } from "./link";
import { guardProcess, openLogs, setupLog, writeLog } from "./log";
import { menuTemplate } from "./menu";
import { mimeOf } from "./mime";
import { Notifier } from "./notify";
import { execRun, PhoneLink, readRelayHint, type Run } from "./phone";
import { lockSession, Preview, PREVIEW_PARTITION } from "./preview";
import { Browser, BROWSER_PARTITION } from "./browser";
import { createHash } from "node:crypto";
import { RecentFolders } from "./recent";
import { type Sealer, ServerStore } from "./servers";
import { Updater } from "./update";
import { MIN_SIZE, nextZoom, placeWindow, trackWindow, WindowStateFile } from "./windowState";

const SCHEME = "app";
const ORIGIN = `${SCHEME}://deck`;
const RENDERER_DIR = resolve(__dirname, "..", "..", "renderer");
const smoke = process.argv.includes("--smoke");
// package.json's build.appId, which the installer gives its shortcuts.
const APP_USER_MODEL_ID = "dev.deck.desktop";

// The renderer is served from app://deck, not file://, so it gets an origin
// of its own and a CSP header.
protocol.registerSchemesAsPrivileged([
  { scheme: SCHEME, privileges: { standard: true, secure: true, supportFetchAPI: true } },
]);

async function serveRenderer(req: Request): Promise<Response> {
  const url = new URL(req.url);
  const rel = decodeURIComponent(url.pathname).replace(/^\/+/, "") || "index.html";
  const file = resolve(RENDERER_DIR, rel);
  if (url.host !== "deck" || !file.startsWith(RENDERER_DIR + sep)) {
    return new Response("not found", { status: 404 });
  }
  try {
    return new Response(await readFile(file), {
      headers: {
        "content-type": mimeOf(file),
        "content-security-policy": `${RENDERER_CSP}; frame-ancestors 'none'`,
        "x-content-type-options": "nosniff",
      },
    });
  } catch {
    return new Response("not found", { status: 404 });
  }
}

// Linux without a keychain gives safeStorage a fixed key ("basic_text"),
// which protects nothing; the token then stays in memory.
const sealer: Sealer = {
  available: () =>
    safeStorage.isEncryptionAvailable() &&
    (process.platform !== "linux" || safeStorage.getSelectedStorageBackend() !== "basic_text"),
  seal: (plain) => safeStorage.encryptString(plain).toString("base64"),
  open: (sealed) => safeStorage.decryptString(Buffer.from(sealed, "base64")),
};

// Dev mode loads the Vite server; only a loopback http URL is accepted.
function devURL(): string | null {
  const u = process.env["DECK_DESKTOP_DEV_URL"];
  return u !== undefined && /^http:\/\/(localhost|127\.0\.0\.1):\d+\/?$/.test(u) ? u : null;
}

let win: BrowserWindow | null = null;
// The icon the window chose (renderer/appIcon), for windows made later; null
// keeps the app's own.
let appIcon: NativeImage | null = null;
// Whether the window's page has loaded, so its preload listens for links.
let loaded = false;
const pendingLinks: LinkEvent[] = [];

const isDir = (p: string): boolean => {
  try {
    return statSync(p).isDirectory();
  } catch {
    return false;
  }
};

// The arguments after the executable, without the app path that
// `electron .` passes in development.
function launchArgs(argv: string[], cwd: string): string[] {
  return argv.slice(1).filter((a) => !process.defaultApp || resolve(cwd, a) !== app.getAppPath());
}

// Hands a link to the window, or keeps it until the page has loaded.
function deliver(link: LinkEvent): void {
  if (win !== null && loaded) win.webContents.send(LINK_EVENT_CHANNEL, link);
  else pendingLinks.push(link);
  if (win?.isMinimized()) win.restore();
  win?.focus();
}

// Delivers what reached the app; a folder from a deck:// link only after
// the person says Open.
async function arrive(a: Arrival): Promise<void> {
  if (mustConfirm(a) && a.link.type === "open") {
    const opts = {
      type: "question" as const,
      buttons: ["Open", "Cancel"],
      defaultId: 1,
      cancelId: 1,
      message: `Open ${a.link.path} in Deck?`,
      detail: "A deck:// link asks to open this folder. Deck starts its engine there.",
    };
    const r = win === null ? await dialog.showMessageBox(opts) : await dialog.showMessageBox(win, opts);
    if (r.response !== 0) return;
  }
  deliver(a.link);
}

// The window's menu for a thread row: Rename, Archive, or Delete after a dialog.
async function threadMenu(t: ThreadMenuInput): Promise<ThreadChoice> {
  const w = win;
  if (w === null) return null;
  const picked = await new Promise<ThreadChoice>((done) => {
    const menu = Menu.buildFromTemplate(threadMenuTemplate(t.archive, done));
    // On Linux and Windows the menu closes before its click runs.
    menu.popup({ window: w, callback: () => setTimeout(() => done(null), 100) });
  });
  if (picked !== "delete") return picked;
  const r = await dialog.showMessageBox(w, {
    type: "warning",
    buttons: ["Delete", "Cancel"],
    defaultId: 1,
    cancelId: 1,
    message: `Delete "${t.title}"?`,
    detail: "Deck removes the thread and its messages.",
  });
  return r.response === 0 ? "delete" : null;
}

// One native image of the icon's sizes; null when the system cannot decode
// it. The images were checked by isAppIcon.
function iconImage(images: AppIconImage[]): NativeImage | null {
  const img = nativeImage.createEmpty();
  for (const r of representations(images)) img.addRepresentation({ scaleFactor: r.scaleFactor, buffer: Buffer.from(r.png) });
  return img.isEmpty() ? null : img;
}

// Shows the icon while the app runs: on the Dock on a Mac, on the window
// (its title bar, taskbar button and switcher) elsewhere. The installed
// app's shortcuts and launcher are launcherIcon.ts's.
function showIcon(img: NativeImage): void {
  appIcon = img;
  if (process.platform === "darwin") app.dock?.setIcon(img);
  else if (win !== null && !win.isDestroyed()) win.setIcon(img);
}

// reset ends what a renderer had open: its sockets and event streams.
function createWindow(reset: () => void, state: WindowStateFile, updater: Updater): BrowserWindow {
  const w = new BrowserWindow({
    ...placeWindow(state.state, screen.getAllDisplays().map((d) => d.workArea)),
    minWidth: MIN_SIZE.width,
    minHeight: MIN_SIZE.height,
    title: "Deck",
    // The renderer draws its own title strips (the "drag" class). On a Mac the
    // traffic lights sit over the sidebar, centred in its 52px toolbar, and the
    // window carries the system's sidebar material for the Apple theme to show
    // through; it follows the window's focus and Reduce transparency.
    // Elsewhere the menu bar hides until Alt, and the first frame takes the
    // canvas of the system's mode.
    ...(process.platform === "darwin"
      ? {
          titleBarStyle: "hiddenInset" as const,
          trafficLightPosition: { x: 18, y: 19 },
          vibrancy: "sidebar" as const,
          visualEffectState: "followWindow" as const,
          backgroundColor: "#00000000",
        }
      : {
          autoHideMenuBar: true,
          backgroundColor: nativeTheme.shouldUseDarkColors ? "#1e1e1e" : "#ffffff",
          ...(appIcon === null ? {} : { icon: appIcon }),
        }),
    webPreferences: {
      preload: join(__dirname, "..", "preload", "preload.js"),
      contextIsolation: true,
      nodeIntegration: false,
      sandbox: true,
      webSecurity: true,
    },
  });
  // The window shows Deck and nothing else: links open in the browser.
  w.webContents.on("will-navigate", (e) => e.preventDefault());
  w.webContents.setWindowOpenHandler(({ url }) => {
    if (/^https?:\/\//i.test(url)) void shell.openExternal(url);
    return { action: "deny" };
  });
  // A reload starts a new renderer state; the old streams have no reader.
  w.webContents.on("did-start-loading", () => {
    loaded = false;
    reset();
  });
  w.webContents.on("did-finish-load", () => {
    w.webContents.setZoomLevel(state.state.zoom);
    loaded = true;
    for (const l of pendingLinks.splice(0)) w.webContents.send(LINK_EVENT_CHANNEL, l);
    if (updater.ready !== null) w.webContents.send(UPDATE_EVENT_CHANNEL, { type: "ready", version: updater.ready });
  });
  w.webContents.on("context-menu", (_e, params) => {
    const items = contextMenuTemplate(params, {
      replaceMisspelling: (word) => w.webContents.replaceMisspelling(word),
      copyText: (text) => clipboard.writeText(text),
      openExternal: (url) => void shell.openExternal(url),
      copyImage: (x, y) => w.webContents.copyImageAt(x, y),
    });
    if (items.length > 0) Menu.buildFromTemplate(items).popup({ window: w });
  });
  if (state.state.maximized) w.maximize();
  trackWindow(w, state);
  w.on("closed", () => {
    reset();
    loaded = false;
    if (win === w) win = null;
  });
  void w.loadURL(devURL() ?? `${ORIGIN}/index.html`);
  return w;
}

function runSmoke(w: BrowserWindow): void {
  const fail = (why: string) => {
    console.error(`smoke: ${why}`);
    app.exit(1);
  };
  const timer = setTimeout(() => fail("timed out"), 30_000);
  w.webContents.once("did-fail-load", (_e, code, desc) => fail(`load failed: ${code} ${desc}`));
  w.webContents.once("did-finish-load", async () => {
    // The app root renders once React mounts; give it a moment.
    await new Promise((r) => setTimeout(r, 1000));
    const mounted: unknown = await w.webContents.executeJavaScript(
      "document.querySelector('[data-app]') !== null && typeof window.deck === 'object'",
    );
    clearTimeout(timer);
    if (mounted !== true) {
      fail("the app did not mount");
      return;
    }
    console.log("smoke: window loaded and the app mounted");
    app.exit(0);
  });
}

async function main(): Promise<void> {
  if (!app.requestSingleInstanceLock()) {
    app.quit();
    return;
  }
  guardProcess(() => win);
  // macOS hands a deck:// link to the running app, and to one it starts for
  // the link before it is ready.
  app.on("open-url", (e, url) => {
    e.preventDefault();
    const link = linkFromUrl(url, isDir);
    // A dialog needs the app to be ready.
    if (link !== null) void app.whenReady().then(() => arrive({ link, fromLink: true }));
  });
  await app.whenReady();
  setupLog();
  // Only an installed app claims deck://; a checkout would point the system
  // at its development build.
  if (app.isPackaged) app.setAsDefaultProtocolClient("deck");
  // Windows shows notifications only for an app with a user model id; the
  // installer sets the same one.
  if (process.platform === "win32") app.setAppUserModelId(APP_USER_MODEL_ID);

  // The icon chosen last, before the window loads: the Dock and the first
  // frame show it, not the default for a moment.
  const iconFile = new AppIconFile(join(app.getPath("userData"), "app-icon.json"));
  const saved = iconFile.load();
  const savedImage = saved === null ? null : iconImage(saved);
  if (savedImage !== null) showIcon(savedImage);
  // The installer's shortcuts are named for the product, and start
  // process.execPath; a per-machine install's are everyone's.
  const launcherDeps: LauncherDeps = {
    platform: process.platform,
    packaged: app.isPackaged,
    dir: join(app.getPath("userData"), "icons"),
    exe: process.execPath,
    windows: {
      links: process.platform === "win32" ? windowsShortcuts(process.env, app.getPath("desktop")) : [],
      shell: {
        read: (p) => shell.readShortcutLink(p),
        // Only shortcuts that start this exe are written, so the target stays.
        write: (p, icon) =>
          shell.writeShortcutLink(p, "update", { target: process.execPath, icon, iconIndex: 0, appUserModelId: APP_USER_MODEL_ID }),
      },
      // Explorer keeps shortcut icons in a cache; this asks it to look
      // again. Best effort: the icon shows at the next sign-in otherwise.
      refresh: () => execFile("ie4uinit.exe", ["-show"], { windowsHide: true }, () => {}),
    },
    linux: linuxLaunchers(process.env, app.getPath("home")),
  };

  const store = new ServerStore(join(app.getPath("userData"), "servers.json"), sealer);
  const links = new LinkHost(store, (id, ev) => win?.webContents.send(SOCKET_EVENT_CHANNEL, id, ev));
  // The bundled engine: resources/bin in the packaged app, bin/ beside
  // package.json in a checkout.
  const bundledDir = app.isPackaged ? join(process.resourcesPath, "bin") : join(app.getAppPath(), "bin");
  const deckPath = findDeck(bundledDir);
  // build copies proto/deck/v1/deck.proto to dist/app/proto.
  const Service = loadService(join(__dirname, "..", "proto", "deck.proto"));
  // Started now, ready by the first open: the login shell's PATH.
  const path = enginePath();
  const decks = new RpcDecks(deckPath, Service, (id, ev) => win?.webContents.send(RPC_EVENT_CHANNEL, id, ev), path, writeLog);
  // The phone link runs the engine's `mobile connect`; the relay it starts
  // needs Node, which the same PATH finds.
  const phoneRun: Run | null =
    deckPath === null ? null : async (args) => execRun(deckPath, { ...process.env, PATH: await path })(args);
  const phone = new PhoneLink(phoneRun, readRelayHint(join(bundledDir, "relay-dir.txt")));
  const previewSession = session.fromPartition(PREVIEW_PARTITION);
  lockSession(previewSession);
  const preview = new Preview({
    create: (webPreferences) => {
      if (win === null) return null;
      const view = new WebContentsView({ webPreferences });
      win.contentView.addChildView(view);
      return view;
    },
    remove: (view) => {
      if (win !== null && !win.isDestroyed()) win.contentView.removeChildView(view);
    },
    zoom: () => win?.webContents.getZoomFactor() ?? 1,
    openExternal: (url) => void shell.openExternal(url),
    send: (s) => win?.webContents.send(PREVIEW_EVENT_CHANNEL, s),
    session: () => previewSession,
  });
  const browser = new Browser({
    create: webPreferences => {
      if (win === null) return null;
      const view = new WebContentsView({ webPreferences });
      win.contentView.addChildView(view);
      return view;
    },
    remove: view => { if (win !== null && !win.isDestroyed()) win.contentView.removeChildView(view); },
    zoom: () => win?.webContents.getZoomFactor() ?? 1,
    // Profiles persist per project, without sharing sign-ins across folders.
    session: id => session.fromPartition(`${BROWSER_PARTITION}-${createHash("sha256").update(decks.directory(id) ?? id).digest("hex").slice(0, 24)}`),
    changed: (id, state) => { void decks.browserUpdate(id, state).catch(() => {}); },
    approve: (id, request, origin) => decks.browserOrigin(id, request, origin),
  });
  decks.setBrowser(browser);
  browser.start();
  // A reload or a closed window ends what the renderer had open, the
  // preview's page with it.
  const reset = () => {
    links.closeAll();
    decks.cancelStreams();
    preview.close();
    browser.close();
  };
  const windowState = new WindowStateFile(join(app.getPath("userData"), "window.json"));
  const updater = new Updater((ev) => win?.webContents.send(UPDATE_EVENT_CHANNEL, ev));
  // A click on a notification brings the window up on that room.
  const notifier = new Notifier(
    () => win,
    (tag) => {
      if (win === null) return;
      if (win.isMinimized()) win.restore();
      win.show();
      win.focus();
      if (loaded) win.webContents.send(NOTIFICATION_EVENT_CHANNEL, { tag });
    },
  );
  const host = createHost({
    store,
    links,
    decks,
    phone,
    recent: new RecentFolders(join(app.getPath("userData"), "recent.json")),
    deckPath,
    deviceName: `Deck desktop (${hostname()})`.slice(0, 64),
    pickFolder: async () => {
      const opts = { properties: ["openDirectory" as const] };
      const r = win === null ? await dialog.showOpenDialog(opts) : await dialog.showOpenDialog(win, opts);
      return r.canceled ? null : (r.filePaths[0] ?? null);
    },
    appInfo: {
      version: app.getVersion(),
      electron: process.versions.electron,
      chromium: process.versions.chrome,
      platform: `${process.platform} ${process.arch}`,
    },
    log: writeLog,
    openLogs,
    installUpdate: () => updater.install(),
    threadMenu,
    notify: (n) => notifier.notify(n),
    badge: (count) => notifier.badge(count),
    focused: () => notifier.focused(),
    agentClis: async () => findAgentClis(await path),
    preview,
    browserView: async input => { browser.present(input); },
    setAppIcon: (images, standard) => {
      const img = iconImage(images);
      if (img === null) return { shown: false, launchers: "none" };
      try {
        iconFile.save(images);
      } catch (e) {
        writeLog("warn", `app icon not saved: ${e instanceof Error ? e.message : String(e)}`);
      }
      let launchers: AppIconResult["launchers"] = "failed";
      let file: string | null = null;
      try {
        ({ state: launchers, file } = applyLauncherIcon(images, standard, launcherDeps));
      } catch (e) {
        writeLog("warn", `app icon not given to the launchers: ${e instanceof Error ? e.message : String(e)}`);
      }
      if (launchers === "denied" || launchers === "failed") writeLog("warn", `app icon: launchers ${launchers}`);
      // Windows reads every size of an .ico for the title bar and taskbar.
      const ico = process.platform === "win32" && file !== null ? nativeImage.createFromPath(file) : null;
      showIcon(ico !== null && !ico.isEmpty() ? ico : img);
      return { shown: true, launchers };
    },
  });
  Menu.setApplicationMenu(
    Menu.buildFromTemplate(
      menuTemplate(
        { isMac: process.platform === "darwin", packaged: app.isPackaged, name: "Deck" },
        {
          send: (command) => win?.webContents.send(MENU_EVENT_CHANNEL, command),
          zoom: (step) => {
            if (win === null) return;
            const zoom = nextZoom(win.webContents.getZoomLevel(), step);
            win.webContents.setZoomLevel(zoom);
            windowState.set({ zoom });
          },
          openLogs: () => void openLogs(),
          checkForUpdates: () => void updater.check(true),
        },
      ),
    ),
  );

  const ses = session.defaultSession;
  protocol.handle(SCHEME, serveRenderer);
  ses.setPermissionRequestHandler((_wc, _perm, cb) => cb(false));
  ses.setPermissionCheckHandler(() => false);
  // Main opens every connection to Deck and pins it in Node (pin.ts). The
  // renderer opens none (connect-src 'none'), so Chromium trusts only a
  // certificate that matches a paired server's pin, and nothing else.
  ses.setCertificateVerifyProc((req, cb) => {
    const fp = `SHA256:${req.certificate.fingerprint.replace(/^sha256\//, "").replace(/=+$/, "")}`;
    const pinned = store.list().some((s) => s.tls && s.host === req.hostname && s.fingerprint === fp);
    cb(pinned ? 0 : -2);
  });

  for (const m of HOST_METHODS) {
    ipcMain.handle(hostChannel(m), (e, ...args: unknown[]) => {
      if (!fromApp(e.sender, e.senderFrame?.url, win?.webContents ?? null, devURL() ?? ORIGIN)) {
        throw new Error("unknown sender");
      }
      return callHost(host, m, args);
    });
  }

  win = createWindow(reset, windowState, updater);
  if (smoke) runSmoke(win);
  for (const a of linksFromArgs(launchArgs(process.argv, process.cwd()), process.cwd(), isDir)) void arrive(a);
  updater.start();

  // Windows and Linux start a second instance for a deck:// link or a
  // folder; it hands its arguments here and quits.
  app.on("second-instance", (_e, argv, cwd) => {
    if (win?.isMinimized()) win.restore();
    win?.focus();
    for (const a of linksFromArgs(launchArgs(argv, cwd), cwd, isDir)) void arrive(a);
  });
  app.on("activate", () => {
    if (win === null) win = createWindow(reset, windowState, updater);
  });
  app.on("window-all-closed", () => {
    if (process.platform !== "darwin") app.quit();
  });
  // Every local Deck stops with the app: its stdin closes.
  app.on("before-quit", () => {
    links.closeAll();
    decks.closeAll();
  });
}

void main();
