import type { Page } from "@playwright/test";
import type { Session } from "electron";
import { callHost } from "../../src/main/host";
import { HOST_METHODS, type PreviewBounds, type PreviewNav } from "../../src/shared/bridge";
import type { HostDeps } from "../../src/main/host";
import { judgeUrl, Preview } from "../../src/main/preview";
import { type TestHost, testHost } from "../bridge";

export type PreviewCall = ["open", string] | ["nav", PreviewNav] | ["bounds", PreviewBounds] | ["hide"] | ["close"];

// The preview's view needs Electron. In Chromium, main's preview probes for
// real and records what the page asks of the view; an open answers as
// main would.
export function stubPreview(): { calls: PreviewCall[]; preview: HostDeps["preview"] } {
  const calls: PreviewCall[] = [];
  const real = new Preview({
    create: () => null,
    remove: () => {},
    zoom: () => 1,
    openExternal: () => {},
    send: () => {},
    session: () => ({}) as Session,
  });
  return {
    calls,
    preview: {
      probe: (ports) => real.probe(ports),
      load: (url) => {
        calls.push(["open", url]);
        return judgeUrl(url, real.open) === "load";
      },
      nav: (n) => void calls.push(["nav", n]),
      bounds: (b) => void calls.push(["bounds", b]),
      hide: () => void calls.push(["hide"]),
      close: () => void calls.push(["close"]),
    },
  };
}

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
      ["onPreview", "__deckPreviewEmit"],
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
    deck["filePath"] = () => "";
    w["deck"] = deck;
  }, HOST_METHODS);
  return t;
}
