import type { Page } from "@playwright/test";
import { callHost } from "../../src/main/host";
import { HOST_METHODS } from "../../src/shared/bridge";
import { type TestHost, testHost } from "../bridge";

// Serves the main-process host to the page as window.deck, the way the
// preload does over IPC. Calls from the page reach the real host (pairing,
// tokens, the socket relay, local Decks over rpc) in this Node process;
// socket and rpc events go back in order.
export async function attachBridge(page: Page, pickFolder?: () => Promise<string | null>): Promise<TestHost> {
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
  );
  for (const m of HOST_METHODS) {
    await page.exposeFunction(`__deck_${m}`, (...args: unknown[]) => callHost(t.host, m, args));
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
    w["deck"] = deck;
  }, HOST_METHODS);
  return t;
}
