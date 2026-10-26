// Keeps the app's icon on the one the prefs and the theme pick: when either
// changes, the window draws the icon and hands the PNGs to main. It reads
// the theme from <html data-palette data-mode>, which appearance.ts writes,
// so it follows the mode the window resolved (Deck's, the system's or the
// person's), and it needs no React.

import type { DeckBridge } from "../../shared/bridge";
import { getPrefs, subscribePrefs } from "../prefs";
import { appIconInfo, resolveAppIcon } from "./catalog";
import { currentOs, type HostOs } from "./platform";
import { renderAppIcon } from "./render";

export interface IconSyncDeps {
  // The icon id to show now, or null before the theme is known.
  want: () => string | null;
  // Draws the icon and sends it to main.
  show: (id: string) => Promise<void>;
  onError: (e: unknown) => void;
}

// Shows the wanted icon once per change. A change while an icon is being
// drawn waits for it, then the latest one is drawn; one that fails is not
// retried until the choice changes.
export function iconSync(d: IconSyncDeps): () => void {
  let shown: string | null = null;
  let busy = false;
  const update = (): void => {
    const id = d.want();
    if (busy || id === null || id === shown) return;
    busy = true;
    shown = id;
    d.show(id)
      .catch(d.onError)
      .finally(() => {
        busy = false;
        update();
      });
  };
  return update;
}

// The icon the theme on <html> wears, or null before appearance.ts set it.
export function themeOnPage(root: Element = document.documentElement): { palette: string; mode: "light" | "dark" } | null {
  const palette = root.getAttribute("data-palette");
  if (palette === null) return null;
  return { palette, mode: root.getAttribute("data-mode") === "light" ? "light" : "dark" };
}

// Starts following the prefs and the theme; returns the stop.
export function startAppIconSync(bridge: Pick<DeckBridge, "setAppIcon" | "logFromRenderer">, os: HostOs = currentOs()): () => void {
  const root = document.documentElement;
  // index.html starts with a placeholder theme; the icon waits for
  // appearance.ts to write the real one, so it does not flick through the
  // placeholder's icon at every start.
  let applied = false;
  const update = iconSync({
    want: () => {
      const t = applied ? themeOnPage(root) : null;
      return t === null ? null : resolveAppIcon(getPrefs().appIcon, t.palette, t.mode);
    },
    show: async (id) => {
      await bridge.setAppIcon(await renderAppIcon(appIconInfo(id), os));
    },
    onError: (e) => void bridge.logFromRenderer("warn", `app icon: ${e instanceof Error ? e.message : String(e)}`).catch(() => {}),
  });
  const watch = new MutationObserver(() => {
    applied = true;
    update();
  });
  watch.observe(root, { attributes: true, attributeFilter: ["data-palette", "data-mode"] });
  const off = subscribePrefs(update);
  update();
  return () => {
    watch.disconnect();
    off();
  };
}
