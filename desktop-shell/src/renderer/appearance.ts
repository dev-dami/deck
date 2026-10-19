import { useEffect } from "react";
import type { ThemePref } from "./prefs";
import { themeInfo } from "./themes";

// Picks light or dark for the window and writes it to <html data-mode>.
// "deck" follows Deck's server theme, which the shared theme.ts writes as
// data-scheme; "system" follows the operating system. A theme with a mode
// of its own (themes.ts) sets it instead; data-palette and data-accent name
// the theme and accent for styles.css.
export function useAppearance(pref: ThemePref, palette = "default", accent = "theme"): void {
  useEffect(() => {
    const root = document.documentElement;
    const media = window.matchMedia("(prefers-color-scheme: light)");
    const fixed = themeInfo(palette).mode;
    root.setAttribute("data-palette", palette);
    if (accent === "theme") root.removeAttribute("data-accent");
    else root.setAttribute("data-accent", accent);
    const apply = () => {
      if (fixed !== null) {
        root.setAttribute("data-mode", fixed);
        return;
      }
      const light =
        pref === "light" ||
        (pref === "system" && media.matches) ||
        (pref === "deck" && root.getAttribute("data-scheme") === "light");
      root.setAttribute("data-mode", light ? "light" : "dark");
    };
    apply();
    const watch = new MutationObserver(apply);
    watch.observe(root, { attributes: true, attributeFilter: ["data-scheme"] });
    media.addEventListener("change", apply);
    return () => {
      watch.disconnect();
      media.removeEventListener("change", apply);
    };
  }, [pref, palette, accent]);
}
