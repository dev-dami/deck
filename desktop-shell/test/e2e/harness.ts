import type { Page } from "@playwright/test";
import { callHost } from "../../src/main/host";
import { HOST_METHODS, type SocketEvent } from "../../src/shared/bridge";
import { testHost } from "../bridge";

// Serves the main-process host to the page as window.deck, the way the
// preload does over IPC. Calls from the page reach the real host (pairing,
// token, socket relay) in this Node process; socket events go back in order.
export async function attachBridge(page: Page): Promise<void> {
  let chain: Promise<unknown> = Promise.resolve();
  const { host } = testHost((id: number, ev: SocketEvent) => {
    chain = chain
      .then(() =>
        page.evaluate(([i, e]) => {
          (window as unknown as { __deckEmit: (id: number, ev: unknown) => void }).__deckEmit(i, e);
        }, [id, ev] as const),
      )
      .catch(() => {});
  });
  for (const m of HOST_METHODS) {
    await page.exposeFunction(`__deck_${m}`, (...args: unknown[]) => callHost(host, m, args));
  }
  await page.addInitScript((methods: readonly string[]) => {
    const w = window as unknown as Record<string, unknown>;
    const listeners = new Set<(id: number, ev: unknown) => void>();
    const deck: Record<string, unknown> = {
      onSocket: (fn: (id: number, ev: unknown) => void) => {
        listeners.add(fn);
        return () => listeners.delete(fn);
      },
    };
    for (const m of methods) {
      deck[m] = (...args: unknown[]) => (w[`__deck_${m}`] as (...a: unknown[]) => Promise<unknown>)(...args);
    }
    w["deck"] = deck;
    w["__deckEmit"] = (id: number, ev: unknown) => {
      for (const l of listeners) l(id, ev);
    };
  }, HOST_METHODS);
}
