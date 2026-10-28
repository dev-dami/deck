import { useEffect, useMemo, useSyncExternalStore } from "react";
import type { BrowserState, BrowserTab, DeckBridge } from "../../shared/bridge";

// The shared browser's tabs as main last told the window: the person's own
// and every agent's. One store for the app; each project's Preview panel
// reads its own Deck's tabs.

const EMPTY: BrowserState = { tabs: [], shown: null };
let state: BrowserState = EMPTY;
const listeners = new Set<() => void>();

const set = (s: BrowserState) => {
  state = s;
  for (const l of listeners) l();
};

const subscribe = (l: () => void) => {
  listeners.add(l);
  return () => {
    listeners.delete(l);
  };
};

// Follows the browser while the shell is up, so the Preview tab shows when
// an agent opens a page and not only when a dev server answers.
export function useWatchBrowser(bridge: DeckBridge): void {
  useEffect(() => {
    let live = true;
    void bridge.browserState().then((s) => {
      if (live) set(s);
    });
    const off = bridge.onBrowser(set);
    return () => {
      live = false;
      off();
    };
  }, [bridge]);
}

export function useBrowserState(): BrowserState {
  return useSyncExternalStore(subscribe, () => state);
}

// The tabs of one local Deck, in the order they were opened.
export function useBrowserTabs(deck: string | undefined): BrowserTab[] {
  const s = useBrowserState();
  return useMemo(() => (deck === undefined ? [] : s.tabs.filter((t) => t.deck === deck)), [s, deck]);
}

// Ctrl or Cmd+L: the window asks the Preview panel to focus its address
// field. The panel may not be mounted yet, so the ask waits for it.
let wanted = false;
const askers = new Set<() => void>();

export function askAddressFocus(): void {
  wanted = true;
  for (const f of askers) f();
}

// Whether an ask is waiting; taking it clears it.
export function takeAddressAsk(): boolean {
  const w = wanted;
  wanted = false;
  return w;
}

export function onAddressAsk(f: () => void): () => void {
  askers.add(f);
  return () => {
    askers.delete(f);
  };
}

// The URL each Deck's Preview panel last opened as the person, so a panel
// that mounts again does not take the person's page away.
const opened = new Map<string, string>();
export const lastOpened = {
  get: (deck: string) => opened.get(deck),
  set: (deck: string, url: string) => void opened.set(deck, url),
};
