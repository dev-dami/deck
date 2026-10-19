import { readFile } from "node:fs/promises";
import { hostname } from "node:os";
import { extname, join, resolve, sep } from "node:path";
import {
  app,
  BrowserWindow,
  dialog,
  ipcMain,
  protocol,
  safeStorage,
  session,
  shell,
} from "electron";
import { HOST_METHODS, hostChannel, RPC_EVENT_CHANNEL, SOCKET_EVENT_CHANNEL } from "../shared/bridge";
import { RENDERER_CSP } from "../shared/csp";
import { findOnPath, loadService, RpcDecks } from "./decks";
import { callHost, createHost } from "./host";
import { LinkHost } from "./link";
import { RecentFolders } from "./recent";
import { type Sealer, ServerStore } from "./servers";

const SCHEME = "app";
const ORIGIN = `${SCHEME}://deck`;
const RENDERER_DIR = resolve(__dirname, "..", "..", "renderer");
const smoke = process.argv.includes("--smoke");

const MIME: Record<string, string> = {
  ".html": "text/html; charset=utf-8",
  ".js": "text/javascript; charset=utf-8",
  ".css": "text/css; charset=utf-8",
  ".svg": "image/svg+xml",
  ".png": "image/png",
  ".woff2": "font/woff2",
  ".json": "application/json",
};

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
        "content-type": MIME[extname(file)] ?? "application/octet-stream",
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

// reset ends what a renderer had open: its sockets and event streams.
function createWindow(reset: () => void): BrowserWindow {
  const w = new BrowserWindow({
    width: 1320,
    height: 840,
    minWidth: 960,
    minHeight: 560,
    title: "Deck",
    backgroundColor: "#111416",
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
  w.webContents.on("did-start-loading", reset);
  w.on("closed", () => {
    reset();
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
  await app.whenReady();

  const store = new ServerStore(join(app.getPath("userData"), "servers.json"), sealer);
  const links = new LinkHost(store, (id, ev) => win?.webContents.send(SOCKET_EVENT_CHANNEL, id, ev));
  const deckPath = findOnPath("deck");
  // build copies proto/deck/v1/deck.proto to dist/app/proto.
  const Service = loadService(join(__dirname, "..", "proto", "deck.proto"));
  const decks = new RpcDecks(deckPath, Service, (id, ev) => win?.webContents.send(RPC_EVENT_CHANNEL, id, ev));
  const reset = () => {
    links.closeAll();
    decks.cancelStreams();
  };
  const host = createHost({
    store,
    links,
    decks,
    recent: new RecentFolders(join(app.getPath("userData"), "recent.json")),
    deckPath,
    deviceName: `Deck desktop (${hostname()})`.slice(0, 64),
    pickFolder: async () => {
      const opts = { properties: ["openDirectory" as const] };
      const r = win === null ? await dialog.showOpenDialog(opts) : await dialog.showOpenDialog(win, opts);
      return r.canceled ? null : (r.filePaths[0] ?? null);
    },
  });

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
      if (win === null || e.sender !== win.webContents || !e.senderFrame?.url.startsWith(devURL() ?? ORIGIN)) {
        throw new Error("unknown sender");
      }
      return callHost(host, m, args);
    });
  }

  win = createWindow(reset);
  if (smoke) runSmoke(win);

  app.on("second-instance", () => {
    if (win?.isMinimized()) win.restore();
    win?.focus();
  });
  app.on("activate", () => {
    if (win === null) win = createWindow(reset);
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
