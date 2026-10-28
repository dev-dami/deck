import type { BrowserContext, CDPSession, Page } from "@playwright/test";
import { Browser, type BrowserPage, type PageFactory, type PageHooks } from "../../src/main/browser";
import { callHost } from "../../src/main/host";
import { type BrowserBounds, HOST_METHODS } from "../../src/shared/bridge";
import type { Cdp } from "../../src/main/pageControl";
import type { Limits, Throttle } from "../../src/main/tabs";
import { type TestHost, testHost } from "../bridge";
import type { HostDeps } from "../../src/main/host";

// A tab's page for the browser, in plain Chromium: a page of the test's own
// context, driven over the same protocol as the app's pages, so the browser's
// code (snapshots, refs, clicks, typing) runs for real. What needs Electron
// (the view laid over the window, the guard on navigations) is recorded or
// left out.
export class PwPage implements BrowserPage {
  private readonly ready: Promise<{ page: Page; session: CDPSession }>;
  private u = "";
  private t = "";
  private n = 0;
  private busy = false;
  private history_ = 0;
  private at = 0;
  placed: (BrowserBounds | null)[] = [];
  awakes: boolean[] = [];
  throttle: Throttle | null = null;
  destroyed = false;
  readonly cdp: Cdp;

  constructor(context: BrowserContext, hooks: PageHooks) {
    this.ready = (async () => {
      const page = await context.newPage();
      await page.setViewportSize({ width: 1280, height: 800 });
      const session = await context.newCDPSession(page);
      page.on("framenavigated", (f) => {
        if (f !== page.mainFrame()) return;
        this.u = f.url();
        this.n++;
        hooks.changed();
      });
      page.on("request", (r) => {
        if (r.isNavigationRequest() && r.frame() === page.mainFrame()) {
          this.busy = true;
          hooks.changed();
        }
      });
      page.on("load", () => {
        this.busy = false;
        void page.title().then(
          (t) => {
            this.t = t;
            hooks.changed();
          },
          () => {},
        );
      });
      page.on("dialog", (d) => void d.dismiss());
      return { page, session };
    })();
    this.cdp = { send: async (m, p) => (await this.ready).session.send(m as never, p as never) };
  }

  // Runs script in the page, for a test to read what an agent did.
  async evaluate<T = unknown>(js: string): Promise<T> {
    const { page } = await this.ready;
    return (await page.evaluate(js)) as T;
  }

  info() {
    return { url: this.u, title: this.t, loading: this.busy, canBack: this.at > 0, canForward: this.at < this.history_ };
  }
  url = () => this.u;
  title = () => this.t;
  loading = () => this.busy;
  epoch = () => this.n;

  async navigate(url: string) {
    const { page } = await this.ready;
    try {
      await page.goto(url, { waitUntil: "load" });
      this.history_ = ++this.at;
      this.t = await page.title();
      return { ok: true };
    } catch (e) {
      return { ok: false, error: e instanceof Error ? (/net::\w+/.exec(e.message)?.[0] ?? e.message) : String(e) };
    }
  }

  async history(dir: "back" | "forward" | "reload") {
    const { page } = await this.ready;
    if (dir === "reload") {
      await page.reload();
      return true;
    }
    const r = dir === "back" ? await page.goBack() : await page.goForward();
    this.at += r === null ? 0 : dir === "back" ? -1 : 1;
    this.u = page.url();
    this.t = await page.title();
    return r !== null;
  }

  async whenLoaded(ms: number) {
    const { page } = await this.ready;
    return page.waitForLoadState("load", { timeout: ms }).then(
      () => true,
      () => false,
    );
  }

  place(b: BrowserBounds | null) {
    this.placed.push(b);
  }
  async awake(on: boolean) {
    this.awakes.push(on);
  }
  apply(t: Throttle) {
    this.throttle = t;
  }
  destroy() {
    this.destroyed = true;
    void this.ready.then(({ page }) => page.close()).catch(() => {});
  }
}

// The browser of the app, with Chromium pages for its tabs. The renderer's
// Preview panel hears of the tabs as it would from main.
export function stubBrowser(page: Page, limits: Partial<Limits> = {}): { browser: Browser; pages: PwPage[] } {
  const pages: PwPage[] = [];
  const factory: PageFactory = {
    create(_tab, hooks) {
      const p = new PwPage(page.context(), hooks);
      pages.push(p);
      return p;
    },
  };
  const browser = new Browser({
    factory,
    limits: { live: 8, perAgent: 6, total: 40, idleMs: 300_000, ...limits },
    send: (s) => {
      void page
        .evaluate((st) => (window as unknown as Record<string, (id: unknown, ev: unknown) => void>)["__deckBrowserEmit"]?.(st, undefined), s)
        .catch(() => {});
    },
  });
  return { browser, pages };
}

export type { HostDeps };

// Serves the main-process host to the page as window.deck, the way the
// preload does over IPC. Calls from the page reach the real host (pairing,
// tokens, the socket relay, local Decks over rpc) in this Node process;
// socket and rpc events go back in order.
// Electron's IPC hands the window a Buffer as a Uint8Array; Playwright
// carries a Uint8Array but not a Buffer, so the harness turns one into the
// other.
function plain(v: unknown): unknown {
  if (Buffer.isBuffer(v)) return new Uint8Array(v);
  if (Array.isArray(v)) return v.map(plain);
  if (typeof v === "object" && v !== null) return Object.fromEntries(Object.entries(v).map(([k, x]) => [k, plain(x)]));
  return v;
}

export async function attachBridge(
  page: Page,
  pickFolder?: () => Promise<string | null>,
  deps: Partial<HostDeps> = {},
): Promise<TestHost> {
  let chain: Promise<unknown> = Promise.resolve();
  const send = (fn: string, id: number | string, ev: unknown) => {
    chain = chain
      .then(() =>
        page.evaluate(([f, i, e]) => {
          (window as unknown as Record<string, (id: unknown, ev: unknown) => void>)[f]?.(i, e);
        }, [fn, id, ev] as const),
      )
      .catch(() => {});
  };
  const t = testHost(
    (id, ev) => send("__deckSocketEmit", id, ev),
    (id, ev) => send("__deckRpcEmit", id, ev),
    pickFolder,
    deps,
  );
  for (const m of HOST_METHODS) {
    await page.exposeFunction(`__deck_${m}`, (...args: unknown[]) => callHost(t.host, m, args).then(plain));
  }
  await page.addInitScript((methods: readonly string[]) => {
    const w = window as unknown as Record<string, unknown>;
    const deck: Record<string, unknown> = {};
    for (const m of methods) {
      deck[m] = (...args: unknown[]) => (w[`__deck_${m}`] as (...a: unknown[]) => Promise<unknown>)(...args);
    }
    for (const [on, emit] of [
      ["onSocket", "__deckSocketEmit"],
      ["onRpc", "__deckRpcEmit"],
      // Main's menu, link and update events; a test may emit them.
      ["onMenu", "__deckMenuEmit"],
      ["onLink", "__deckLinkEmit"],
      ["onUpdate", "__deckUpdateEmit"],
      ["onNotificationClick", "__deckNotificationEmit"],
      ["onBrowser", "__deckBrowserEmit"],
    ] as const) {
      const listeners = new Set<(id: unknown, ev: unknown) => void>();
      deck[on] = (fn: (id: unknown, ev: unknown) => void) => {
        listeners.add(fn);
        return () => listeners.delete(fn);
      };
      w[emit] = (id: unknown, ev: unknown) => {
        for (const l of listeners) l(id, ev);
      };
    }
    // A page in Chromium has no file paths.
    deck["folderPath"] = () => "";
    w["deck"] = deck;
  }, HOST_METHODS);
  return t;
}
